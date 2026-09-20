// Command api 是 KidStudy 的 API 服务入口。
//
// 启动流程：加载配置（失败即退出）→ 可选迁移 → 连接数据库 → 装配路由 → 运行并等待退出信号。
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"

	"kidstudy/internal/config"
	"kidstudy/internal/feature/auth"
	"kidstudy/internal/feature/children"
	"kidstudy/internal/feature/system"
	platformauth "kidstudy/internal/platform/auth"
	"kidstudy/internal/platform/logger"
	"kidstudy/internal/platform/middleware"
	"kidstudy/internal/platform/postgres"
	httpsrv "kidstudy/internal/platform/server"
	migrations "kidstudy/migrations"
)

// registerFeatures 装配各业务模块。
//
// 数据库不可达时逐个 return）：/health、/ready 仍要能回答，便于排障时先让探针说话。
func registerFeatures(r chi.Router, cfg config.Config, log *slog.Logger, db *postgres.DB) {
	if db == nil {
		return
	}

	tokens := platformauth.NewTokenService(cfg.AuthSecret, cfg.AccessTokenTTL, cfg.UnlockTokenTTL)
	requireAuth := middleware.RequireAuth(tokens, log)

	authSvc := auth.NewService(auth.NewRepository(db.Pool()), tokens, cfg, log)
	authH := auth.NewHandler(authSvc, cfg, log)

	childrenH := children.NewHandler(
		children.NewService(children.NewRepository(db.Pool())),
		log,
	)

	// 业务 API 统一走 /api/v1；健康检查留在根路径，供编排直接探活
	r.Route("/api/v1", func(r chi.Router) {
		authH.Register(r, requireAuth)

		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			childrenH.Register(r)
		})
	})
}

func main() {
	var (
		runMigrate = flag.Bool("migrate", false, "启动前执行数据库迁移")
		rollback   = flag.Bool("rollback", false, "回退一步数据库迁移后退出")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		// 配置错误必须 fail-fast：宁可起不来，也不要带着错误配置运行
		early := logger.New(config.Config{LogFormat: "text"})
		early.Error("配置加载失败", "error", err)
		os.Exit(1)
	}

	log := logger.New(cfg)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *rollback {
		if err := postgres.Rollback(cfg.DatabaseURL, ".", migrations.FS, log); err != nil {
			log.Error("迁移回退失败", "error", err)
			os.Exit(1)
		}
		return
	}
	if *runMigrate {
		if err := postgres.RunUp(cfg.DatabaseURL, ".", migrations.FS, log); err != nil {
			log.Error("迁移执行失败", "error", err)
			os.Exit(1)
		}
	}

	db, dbErr := postgres.Open(ctx, cfg)
	if dbErr != nil {
		// 数据库不可达时不退出：进程照常起来，由 /ready 返回 503 让编排层感知
		log.Warn("数据库连接失败，服务以未就绪状态启动", "error", dbErr)
	}
	if db != nil {
		defer db.Close()
	}

	var checker postgres.HealthChecker
	if db != nil {
		checker = db
	}
	health := system.New(cfg.Version, checker)

	handler := httpsrv.NewRouter(cfg, log, func(r chi.Router) {
		health.Register(r)
		registerFeatures(r, cfg, log, db)
	})

	if err := httpsrv.Run(ctx, cfg, log, handler); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("服务异常退出", "error", err)
		os.Exit(1)
	}
}
