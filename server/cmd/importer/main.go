// Command importer 是一次性内容导入器：把 var/ 下的采集与构建产物灌进 PostgreSQL。
//
// 用法（在 server/ 目录下）：
//
//	go run ./cmd/importer                     # 用默认数据目录 ../var
//	go run ./cmd/importer -data ../var        # 指定数据目录
//	go run ./cmd/importer -only hanzi         # 只跑某一步（stages|hanzi|words|stories|plans）
//
// 幂等：可反复重跑，不会产生重复行，也不会覆盖家长已做的判定。
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

	"kidstudy/internal/config"
	"kidstudy/internal/feature/content"
	"kidstudy/internal/platform/logger"
	"kidstudy/internal/platform/postgres"
)

func main() {
	var (
		dataRoot = flag.String("data", "../var", "数据资产目录（仓库根的 var）")
		only     = flag.String("only", "", "只跑指定步骤：stages|hanzi|words|stories|plans")
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

	imp := content.NewImporter(content.NewRepository(db.Pool()), log, *dataRoot)

	start := time.Now()
	var rep content.Report
	if *only != "" {
		rep, err = imp.RunOnly(ctx, *only)
	} else {
		rep, err = imp.Run(ctx)
	}
	if err != nil {
		log.Error("内容导入失败", "error", err, "duration_ms", time.Since(start).Milliseconds())
		os.Exit(1)
	}

	// 结果用一行 JSON 输出，方便直接肉眼核对「字数对得上」
	summary, _ := json.Marshal(map[string]any{
		"stages":        rep.Stages,
		"hanzi":         rep.Hanzi,
		"hanzi_words":   rep.HanziWords,
		"en_words":      rep.EnWords,
		"stories":       rep.Stories,
		"story_pairs":   rep.StoryPairs,
		"review_items":  rep.ReviewItems,
		"plan_children": rep.PlanChildren,
		"plan_rows":     rep.PlanRows,
		"duration_ms":   time.Since(start).Milliseconds(),
	})
	log.Info("内容导入完成", "only", *only, "summary", string(summary))
}
