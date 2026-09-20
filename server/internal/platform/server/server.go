// Package server 负责 HTTP 服务的装配与优雅停机。
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"kidstudy/internal/config"
	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/middleware"
	"kidstudy/internal/platform/response"
)

// NewRouter 组装中间件链与路由。
// 中间件顺序是有意为之：RealIP → RequestID → Recoverer → Logger → Security → CORS。
func NewRouter(cfg config.Config, log *slog.Logger, register func(chi.Router)) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RealIP(cfg.TrustedProxy))
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer(log))
	r.Use(middleware.Logger(log))
	r.Use(middleware.SecurityHeaders(cfg.IsProd()))
	r.Use(middleware.CORS(cfg.CORSAllowedOrigins, cfg.IsProd()))

	// 未匹配路由与方法：统一返回规范错误体，而不是 chi 默认纯文本
	loggedNotFound := func(w http.ResponseWriter, r *http.Request) {
		response.Error(w, r, log, apperr.NotFound("请求的资源不存在"))
	}
	r.NotFound(loggedNotFound)
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		response.Error(w, r, log, apperr.MethodNotAllowed("不支持的请求方法"))
	})

	if register != nil {
		register(r)
	}
	return r
}

// Run 启动 HTTP 服务并阻塞，直到 ctx 取消或监听失败；退出前按 ShutdownTimout 优雅停机。
func Run(ctx context.Context, cfg config.Config, log *slog.Logger, handler http.Handler) error {
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("服务启动", "addr", cfg.HTTPAddr, "env", cfg.AppEnv, "version", cfg.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimout)
		defer cancel()
		log.Info("收到退出信号，开始优雅停机", "timeout", cfg.ShutdownTimout.String())
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return errors.Join(err, srv.Close())
		}
		log.Info("服务已停止")
		return nil
	}
}
