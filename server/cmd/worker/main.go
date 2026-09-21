// Command worker 是后台日汇总进程：重算 daily_stats 的节奏与达标列，并评测成就。
//
// 为什么要独立进程（而不是塞进 API 进程的定时器）：
// 日汇总要按天重算、可以跑几分钟，放在 API 进程里会与请求抢连接池，
// 也违背「不在请求处理器中执行长任务」的原则；独立进程还能单独重启、单独限流。
//
// 用法（在 server/ 目录下）：
//
//	go run ./cmd/worker                  # 重算最近 3 天（含今天），结束即退出
//	go run ./cmd/worker -days 30         # 重算最近 30 天
//	go run ./cmd/worker -date 2026-09-20 # 只重算指定一天
//	go run ./cmd/worker -child <uuid>    # 只处理某个孩子
//	go run ./cmd/worker -loop -at 03:30  # 常驻：每天 03:30 跑一次
//
// 幂等：同一 (孩子, 日期, 学科) 是覆盖式重算，可随时反复执行。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"kidstudy/internal/config"
	"kidstudy/internal/feature/report"
	"kidstudy/internal/platform/logger"
	"kidstudy/internal/platform/postgres"
)

func main() {
	var (
		dateOnly = flag.String("date", "", "只重算这一天（YYYY-MM-DD），默认今天")
		days     = flag.Int("days", 3, "从 -date 往前重算的天数（含 -date）")
		childID  = flag.String("child", "", "只处理指定孩子（uuid），默认全部")
		loop     = flag.Bool("loop", false, "常驻运行，每天在 -at 指定的时刻跑一次")
		at       = flag.String("at", "03:30", "常驻模式下每天的运行时刻（HH:MM，本地时间）")
		noAward  = flag.Bool("no-award", false, "跳过成就评测")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		early := logger.New(config.Config{LogFormat: "text"})
		early.Error("配置加载失败", "error", err)
		os.Exit(1)
	}
	log := logger.New(cfg)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		log.Error("数据库连接失败", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	svc := report.NewService(report.NewRepository(db.Pool()), log)

	var only []uuid.UUID
	if *childID != "" {
		id, err := uuid.Parse(*childID)
		if err != nil {
			log.Error("非法的 -child 参数", "value", *childID)
			os.Exit(1)
		}
		only = []uuid.UUID{id}
	}

	runOnce := func() {
		to := time.Now()
		if *dateOnly != "" {
			d, err := time.ParseInLocation("2006-01-02", *dateOnly, time.Local)
			if err != nil {
				log.Error("非法的 -date 参数", "value", *dateOnly, "want", "YYYY-MM-DD")
				os.Exit(1)
			}
			to = d
		}
		from := to.AddDate(0, 0, -(*days - 1))

		start := time.Now()
		rep, err := svc.Rollup(ctx, report.RollupOptions{ChildIDs: only, From: from, To: to})
		if err != nil {
			log.Error("日汇总失败", "error", err, "duration_ms", time.Since(start).Milliseconds())
			os.Exit(1)
		}
		summary, _ := json.Marshal(rep)
		log.Info("日汇总完成",
			"from", from.Format("2006-01-02"), "to", to.Format("2006-01-02"),
			"summary", string(summary), "duration_ms", time.Since(start).Milliseconds())

		// 成就评测放在汇总之后：连续达标这类规则依赖当日 passed 已经算出来
		if !*noAward {
			n, err := svc.AwardAll(ctx, only)
			if err != nil {
				log.Error("成就评测失败", "error", err)
				os.Exit(1)
			}
			log.Info("成就评测完成", "granted", n)
		}
	}

	if !*loop {
		runOnce()
		return
	}

	next, err := nextRunAt(*at, time.Now())
	if err != nil {
		log.Error("非法的 -at 参数", "value", *at, "want", "HH:MM")
		os.Exit(1)
	}
	log.Info("worker 常驻启动", "at", *at, "next_run", next.Format(time.RFC3339))
	for {
		select {
		case <-ctx.Done():
			log.Info("worker 收到退出信号，正在停止")
			return
		case <-time.After(time.Until(next)):
			runOnce()
			next = next.AddDate(0, 0, 1)
			log.Info("下一次日汇总", "next_run", next.Format(time.RFC3339))
		}
	}
}

// nextRunAt 计算今天或明天的 hh:mm（本地时间）。
func nextRunAt(hhmm string, now time.Time) (time.Time, error) {
	t, err := time.ParseInLocation("15:04", hhmm, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next, nil
}
