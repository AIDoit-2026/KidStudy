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
	"kidstudy/internal/feature/content"
	"kidstudy/internal/feature/mastery"
	"kidstudy/internal/feature/parent"
	"kidstudy/internal/feature/practice"
	"kidstudy/internal/feature/print"
	"kidstudy/internal/feature/report"
	"kidstudy/internal/feature/review"
	"kidstudy/internal/feature/system"
	platformauth "kidstudy/internal/platform/auth"
	"kidstudy/internal/platform/logger"
	"kidstudy/internal/platform/middleware"
	"kidstudy/internal/platform/postgres"
	httpsrv "kidstudy/internal/platform/server"
	"kidstudy/internal/platform/storage"
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

	// 限流三档：登录（凭据猜测）/扫码（可被脚本刷）/打印（触发渲染的重活）。
	limiters := middleware.NewLimiters(
		cfg.RateLimitEnabled,
		middleware.LimitSpec{PerMin: cfg.RateLimitLoginPerMin, Burst: cfg.RateLimitLoginBurst},
		middleware.LimitSpec{PerMin: cfg.RateLimitQRPerMin, Burst: cfg.RateLimitQRBurst},
		middleware.LimitSpec{PerMin: cfg.RateLimitPrintPerMin, Burst: cfg.RateLimitPrintBurst},
		log,
	)

	authSvc := auth.NewService(auth.NewRepository(db.Pool()), tokens, cfg, log)
	authH := auth.NewHandler(authSvc, cfg, log)

	childrenSvc := children.NewService(children.NewRepository(db.Pool()))
	childrenH := children.NewHandler(childrenSvc, log)

	contentSvc := content.NewService(content.NewRepository(db.Pool()), log)
	contentH := content.NewHandler(contentSvc, log)

	reviewH := review.NewHandler(
		review.NewService(review.NewRepository(db.Pool()), log),
		log,
	)

	// mastery 先于 practice 构造：practice 判分时要调它的状态机
	masterySvc := mastery.NewService(mastery.NewRepository(db.Pool()), log)
	masteryH := mastery.NewHandler(masterySvc, childrenSvc, log)

	// report 不依赖 practice，但 practice 结算后要调它的成就评测，
	// 所以先构造 report，再把 reportSvc 当作 BadgeAwarder 注入 practice。
	reportSvc := report.NewService(report.NewRepository(db.Pool()), log)
	reportH := report.NewHandler(reportSvc, childrenSvc, log)

	practiceSvc := practice.NewService(practice.NewRepository(db.Pool()), masterySvc, contentSvc, reportSvc, log)
	practiceH := practice.NewHandler(practiceSvc, childrenSvc, log)

	parentSvc := parent.NewService(parent.NewRepository(db.Pool()), log)
	parentH := parent.NewHandler(parentSvc, log)

	// 报表建议的一键动作要落到 practice / mastery / parent 上。report 与 practice
	// 互为依赖（report 用 practice 指派专项、practice 用 report 评测成就），构造顺序
	// 无法同时满足，所以在两者都就绪后后置注入（见 report.WithActions）。
	reportSvc.WithActions(practiceSvc, masterySvc, parentSvc)

	// storage 是配置项，目录不可用说明部署配错了——这种错重启也改不掉，
	// 与其让 print 端点零星报 500，不如不注册，让 /ready 与日志把问题说清楚。
	store, err := storage.NewLocal(cfg.StorageDir)
	if err != nil {
		log.Error("初始化对象存储失败，打印模块不会挂载", "error", err, "storage_dir", cfg.StorageDir)
		return
	}
	// API 进程不渲染 PDF（渲染在 worker），这里的 renderer 只是打印服务的占位：
	// 它是懒启动的，构造不会拉起 Chromium，也不会因为这台机器没装浏览器而拖垮 API；
	// 既然从未初始化，进程退出时也无需 Close。
	renderer := print.NewPDFRenderer(cfg.ChromePath, cfg.PDFRenderTimeout)

	printSvc := print.NewService(print.NewRepository(db.Pool()), contentSvc, masterySvc, reportSvc, store, renderer, log)
	printH := print.NewHandler(printSvc, log)

	// 报表 PDF 复用 M5 的周报模板：export?format=pdf 会经由这里建打印任务并排队。
	// 依赖方向是 print → report，report 只持有接口，故在 print 就绪后后置注入。
	reportSvc.WithPDFExporter(printSvc)

	// 业务 API 统一走 /api/v1；健康检查留在根路径，供编排直接探活
	r.Route("/api/v1", func(r chi.Router) {
		authH.Register(r, requireAuth, limiters)

		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			childrenH.Register(r)
			contentH.Register(r)
			reviewH.Register(r)
			masteryH.Register(r)
			practiceH.Register(r)
			reportH.Register(r)
			parentH.Register(r)
			printH.Register(r, limiters)
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
