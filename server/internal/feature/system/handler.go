// Package system 提供健康检查端点：/health（进程存活）与 /ready（依赖就绪）。
package system

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"kidstudy/internal/platform/response"
)

// Handler 持有版本与启动时间，并对数据库做轻量就绪探测。
type Handler struct {
	version   string
	startedAt time.Time
	checker   interface{ Ping(context.Context) error }
}

// New 构造健康检查处理器。checker 为 nil 时 /ready 一律返回 503。
func New(version string, checker interface{ Ping(context.Context) error }) *Handler {
	return &Handler{version: version, startedAt: time.Now(), checker: checker}
}

type healthData struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

type readyData struct {
	Status string         `json:"status"`
	Checks map[string]any `json:"checks"`
}

// Health 返回进程存活状态，不触碰任何外部依赖。
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, r, http.StatusOK, healthData{
		Status:        "ok",
		Version:       h.version,
		UptimeSeconds: int64(time.Since(h.startedAt).Seconds()),
	})
}

// Ready 检查数据库连通性。未就绪返回 503，供编排系统在流量进入前拦截。
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	checks := map[string]any{}
	ready := true

	if h.checker == nil {
		ready = false
		checks["database"] = "not_initialized"
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := h.checker.Ping(ctx); err != nil {
			ready = false
			// 只给状态，不泄漏连接细节
			checks["database"] = "down"
		} else {
			checks["database"] = "up"
		}
	}

	status := http.StatusOK
	dataStatus := "ready"
	if !ready {
		status = http.StatusServiceUnavailable
		dataStatus = "not_ready"
	}
	response.JSON(w, r, status, readyData{Status: dataStatus, Checks: checks})
}

// Register 注册健康检查路由。刻意置于认证与限流之外，供容器/编排直接探活。
func (h *Handler) Register(r chi.Router) {
	r.Get("/health", h.Health)
	r.Get("/ready", h.Ready)
}
