package report

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"kidstudy/internal/feature/children"
	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/requestctx"
	"kidstudy/internal/platform/response"
)

// ChildGuard 确认「这个孩子属于当前家长」，由 children.Service 实现。
type ChildGuard interface {
	EnsureOwned(ctx context.Context, parentID, childID uuid.UUID) (children.Child, error)
}

// errNotFound 统一 404 文案：越权与不存在都用它，避免暴露某个 ID 是否真实存在。
var errNotFound = apperr.NotFound("资源不存在或无权访问")

// Handler 处理报表与成就的 HTTP 请求。
type Handler struct {
	svc   *Service
	guard ChildGuard
	log   *slog.Logger
}

// NewHandler 构造报表 handler。
func NewHandler(svc *Service, guard ChildGuard, log *slog.Logger) *Handler {
	return &Handler{svc: svc, guard: guard, log: log}
}

// Register 挂载 /reports 路由。
func (h *Handler) Register(r chi.Router) {
	r.Route("/reports", func(r chi.Router) {
		r.Get("/compare", h.compare)
		r.Route("/{childId}", func(r chi.Router) {
			r.Get("/overview", h.overview)
			r.Get("/trend", h.trend)
			r.Get("/subject/{subject}", h.subject)
			r.Get("/suggestions", h.suggestions)
			r.Get("/pace", h.pace)
			r.Get("/growth", h.growth)
			r.Get("/badges", h.badges)
			r.Get("/export", h.export)
		})
	})
}

// target 解析并校验 {childId} 的归属。
func (h *Handler) target(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return uuid.Nil, false
	}
	childID, err := uuid.Parse(chi.URLParam(r, "childId"))
	if err != nil {
		response.Error(w, r, h.log, errNotFound)
		return uuid.Nil, false
	}
	if _, err := h.guard.EnsureOwned(r.Context(), parentID, childID); err != nil {
		response.Error(w, r, h.log, errNotFound)
		return uuid.Nil, false
	}
	return childID, true
}

// overview GET /reports/{childId}/overview
func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	childID, ok := h.target(w, r)
	if !ok {
		return
	}
	view, err := h.svc.Overview(r.Context(), childID)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// trend GET /reports/{childId}/trend?days=30&subject=
func (h *Handler) trend(w http.ResponseWriter, r *http.Request) {
	childID, ok := h.target(w, r)
	if !ok {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	view, err := h.svc.Trend(r.Context(), childID, days, r.URL.Query().Get("subject"))
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// subject GET /reports/{childId}/subject/{subject}
func (h *Handler) subject(w http.ResponseWriter, r *http.Request) {
	childID, ok := h.target(w, r)
	if !ok {
		return
	}
	subject := chi.URLParam(r, "subject")
	if !validSubject(subject) {
		response.Error(w, r, h.log, errBadSubject)
		return
	}
	view, err := h.svc.Subject(r.Context(), childID, subject)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// suggestions GET /reports/{childId}/suggestions
func (h *Handler) suggestions(w http.ResponseWriter, r *http.Request) {
	childID, ok := h.target(w, r)
	if !ok {
		return
	}
	view, err := h.svc.Suggestions(r.Context(), childID)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// pace GET /reports/{childId}/pace?days=90&subject=
func (h *Handler) pace(w http.ResponseWriter, r *http.Request) {
	childID, ok := h.target(w, r)
	if !ok {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	view, err := h.svc.Pace(r.Context(), childID, days, r.URL.Query().Get("subject"))
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// growth GET /reports/{childId}/growth
func (h *Handler) growth(w http.ResponseWriter, r *http.Request) {
	childID, ok := h.target(w, r)
	if !ok {
		return
	}
	view, err := h.svc.Growth(r.Context(), childID)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// badges GET /reports/{childId}/badges
//
// 读之前先跑一次评测：孩子刚拿到成就就能看到，不必等夜里的 worker。
// 授予本身是幂等的（unique + ON CONFLICT DO NOTHING），失败也不影响读。
func (h *Handler) badges(w http.ResponseWriter, r *http.Request) {
	childID, ok := h.target(w, r)
	if !ok {
		return
	}
	if _, err := h.svc.EvaluateBadges(r.Context(), childID); err != nil {
		h.log.Warn("成就评测失败，仅返回已授予结果", "child_id", childID, "error", err)
	}
	view, err := h.svc.Badges(r.Context(), childID)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// export GET /reports/{childId}/export?format=csv&days=90&subject=
func (h *Handler) export(w http.ResponseWriter, r *http.Request) {
	childID, ok := h.target(w, r)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" {
		// PDF 走 M5 的打印模块（复用周报告模板），这里只出 CSV
		response.Error(w, r, h.log, apperr.BadRequest("目前仅支持 format=csv"))
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	data, err := h.svc.ExportCSV(r.Context(), childID, days, r.URL.Query().Get("subject"))
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}

	// CSV 是文件下载，不走统一 JSON 信封
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		h.log.Warn("写 CSV 响应失败", "child_id", childID, "error", err)
	}
}

// compare GET /reports/compare?childIds=a,b&align=session|calendar&subject=
func (h *Handler) compare(w http.ResponseWriter, r *http.Request) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return
	}
	ids, err := ParseUUIDList(r.URL.Query().Get("childIds"))
	if err != nil {
		response.Error(w, r, h.log, apperr.BadRequest(err.Error()))
		return
	}
	align := r.URL.Query().Get("align")
	subject := r.URL.Query().Get("subject")
	if subject != "" && !validSubject(subject) {
		response.Error(w, r, h.log, errBadSubject)
		return
	}
	view, err := h.svc.Compare(r.Context(), parentID, ids, align, subject)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}
