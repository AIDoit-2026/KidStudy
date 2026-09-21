// Command worker 是后台任务进程，目前承担两件事：
//
//  1. 日汇总：重算 daily_stats 的节奏与达标列，并评测成就（M4）。
//  2. 打印渲染：消费 print_jobs 里排队中的任务，用 Chromium 出 PDF（M5）。
//
// 为什么要独立进程（而不是塞进 API 进程的定时器）：
// 日汇总要按天重算、可以跑几分钟；PDF 渲染要拉一个 Chromium 实例、单份数秒。
// 两者放在 API 进程里都会与请求抢连接池与 CPU，也违背「不在请求处理器中执行长任务」的原则。
//
// 用法（在 server/ 目录下）：
//
//	go run ./cmd/worker                     # 重算最近 3 天（含今天），结束即退出
//	go run ./cmd/worker -days 30            # 重算最近 30 天
//	go run ./cmd/worker -date 2026-09-20    # 只重算指定一天
//	go run ./cmd/worker -child <uuid>       # 只处理某个孩子
//	go run ./cmd/worker -loop -at 03:30     # 常驻：每天 03:30 做一次日汇总
//	go run ./cmd/worker -print              # 只跑一轮打印渲染，结束即退出
//	go run ./cmd/worker -print-loop 5s      # 常驻消费打印队列（每 5 秒看一眼）
//	go run ./cmd/worker -purge              # 清理超过 PRINT_RETENTION 的 PDF
//	go run ./cmd/worker -loop -print-loop 5s -purge-loop 1h   # 一个进程全包
//
// 幂等：日汇总是覆盖式重算；打印渲染靠 print_jobs 的状态机 + 文件覆盖写，重复跑不会出双份。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"kidstudy/internal/config"
	"kidstudy/internal/feature/content"
	"kidstudy/internal/feature/mastery"
	"kidstudy/internal/feature/print"
	"kidstudy/internal/feature/report"
	"kidstudy/internal/platform/logger"
	"kidstudy/internal/platform/postgres"
	"kidstudy/internal/platform/storage"
)

func main() {
	var (
		dateOnly = flag.String("date", "", "只重算这一天（YYYY-MM-DD），默认今天")
		days     = flag.Int("days", 3, "从 -date 往前重算的天数（含 -date）")
		childID  = flag.String("child", "", "只处理指定孩子（uuid），默认全部")
		loop     = flag.Bool("loop", false, "常驻运行，每天在 -at 指定的时刻做日汇总")
		at       = flag.String("at", "03:30", "常驻模式下日汇总的运行时刻（HH:MM，本地时间）")
		noAward  = flag.Bool("no-award", false, "跳过成就评测")

		renderOnce  = flag.Bool("print", false, "跑一轮打印渲染后退出")
		renderEvery = flag.Duration("print-loop", 0, "常驻消费打印队列的间隔（如 5s），0 表示不跑")
		printBatch  = flag.Int("print-batch", 5, "每轮最多渲染几个打印任务")
		staleAfter  = flag.Duration("print-stale", 10*time.Minute, "渲染中超过这个时长视为进程崩溃遗留，退回队列")

		purgeOnce  = flag.Bool("purge", false, "清理一批过期 PDF 后退出")
		purgeEvery = flag.Duration("purge-loop", 0, "常驻清理过期 PDF 的间隔，0 表示不跑")
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

	reportSvc := report.NewService(report.NewRepository(db.Pool()), log)
	contentSvc := content.NewService(content.NewRepository(db.Pool()), log)
	masterySvc := mastery.NewService(mastery.NewRepository(db.Pool()), log)

	store, err := storage.NewLocal(cfg.StorageDir)
	if err != nil {
		log.Error("初始化对象存储失败", "error", err, "storage_dir", cfg.StorageDir)
		os.Exit(1)
	}
	renderer := print.NewPDFRenderer(cfg.ChromePath, cfg.PDFRenderTimeout)
	defer renderer.Close()

	printSvc := print.NewService(print.NewRepository(db.Pool()), contentSvc, masterySvc,
		reportSvc, store, renderer, log)

	var only []uuid.UUID
	if *childID != "" {
		id, err := uuid.Parse(*childID)
		if err != nil {
			log.Error("非法的 -child 参数", "value", *childID)
			os.Exit(1)
		}
		only = []uuid.UUID{id}
	}

	// ---------------------------------------------------------------- 日汇总

	runRollup := func() {
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
		rep, err := reportSvc.Rollup(ctx, report.RollupOptions{ChildIDs: only, From: from, To: to})
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
			n, err := reportSvc.AwardAll(ctx, only)
			if err != nil {
				log.Error("成就评测失败", "error", err)
				os.Exit(1)
			}
			log.Info("成就评测完成", "granted", n)
		}
	}

	// ---------------------------------------------------------------- 打印渲染

	runRenderPass := func() {
		start := time.Now()
		batch, err := printSvc.RenderPending(ctx, *printBatch)
		if err != nil {
			log.Error("打印渲染失败", "error", err)
			return
		}
		if batch.Claimed == 0 {
			return
		}
		log.Info("打印渲染完成",
			"claimed", batch.Claimed, "ready", batch.Ready, "failed", batch.Failed,
			"duration_ms", time.Since(start).Milliseconds())
	}

	// 渲染器在启动时报出用的是哪个 Chromium：找不到内核时 worker 不会崩，
	// 但队列会一直堆着，日志里必须能一眼看出原因。
	if *renderOnce || *renderEvery > 0 {
		if path := renderer.ChromePath(); path == "" {
			log.Warn("未找到 Chromium 内核，打印渲染会失败；请设置 CHROME_PATH")
		} else {
			log.Info("打印渲染已就绪", "chrome", path)
		}
		if n, err := printSvc.RequeueStale(ctx, *staleAfter); err != nil {
			log.Error("退回卡住的渲染任务失败", "error", err)
		} else if n > 0 {
			log.Info("已退回卡住的渲染任务", "count", n)
		}
	}

	runPurge := func() {
		before := time.Now().Add(-cfg.PrintRetention)
		n, err := printSvc.PurgeExpired(ctx, before, 200)
		if err != nil {
			log.Error("清理过期 PDF 失败", "error", err)
			return
		}
		log.Info("过期 PDF 清理完成", "purged", n, "before", before.Format(time.RFC3339))
	}

	// ---------------------------------------------------------------- 调度

	needRollup := *loop
	needRender := *renderEvery > 0
	needPurge := *purgeEvery > 0

	// 非常驻：把请求的一次性任务做完就退出
	if !needRollup && !needRender && !needPurge {
		if *renderOnce {
			runRenderPass()
			return
		}
		if *purgeOnce {
			runPurge()
			return
		}
		runRollup()
		return
	}

	// 常驻：各项任务各自起一个 goroutine，互不阻塞（渲染是秒级、汇总可能几分钟）
	errCh := make(chan error, 3)

	if needRollup {
		next, err := nextRunAt(*at, time.Now())
		if err != nil {
			log.Error("非法的 -at 参数", "value", *at, "want", "HH:MM")
			os.Exit(1)
		}
		log.Info("日汇总调度已启动", "at", *at, "next_run", next.Format(time.RFC3339))
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Until(next)):
					runRollup()
					next = next.AddDate(0, 0, 1)
					log.Info("下一次日汇总", "next_run", next.Format(time.RFC3339))
				}
			}
		}()
	}

	if needRender {
		log.Info("打印渲染循环已启动", "interval", renderEvery.String(), "batch", *printBatch)
		go func() {
			ticker := time.NewTicker(*renderEvery)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runRenderPass()
				}
			}
		}()
	}

	if needPurge {
		log.Info("过期 PDF 清理循环已启动", "interval", purgeEvery.String(), "retention", cfg.PrintRetention.String())
		go func() {
			ticker := time.NewTicker(*purgeEvery)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runPurge()
				}
			}
		}()
	}

	select {
	case <-ctx.Done():
		log.Info("worker 收到退出信号，正在停止")
	case err := <-errCh:
		log.Error("worker 异常退出", "error", err)
		os.Exit(1)
	}
	// 等一等正在收尾的任务，但别无限等
	time.Sleep(200 * time.Millisecond)
	fmt.Fprintln(os.Stderr, "")
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
