package mastery

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"kidstudy/internal/feature/children"
	"kidstudy/internal/pkg/pagination"
	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/requestctx"
	"kidstudy/internal/platform/response"
)

// errNotFound 统一 404 文案：越权与不存在都用它，避免暴露某个 ID 是否真实存在。
var errNotFound = apperr.NotFound("内容不存在或无权访问")

// ChildGuard 用来确认「这个孩子属于当前家长」，由 children.Service 实现。
//
// 走接口而不是直接依赖 children 包的具体类型，是为了让 mastery 在测试里能塞一个假实现。
type ChildGuard interface {
	EnsureOwned(ctx context.Context, parentID, childID uuid.UUID) (children.Child, error)
}

// Handler 处理掌握度相关的 HTTP 请求。
type Handler struct {
	svc   *Service
	guard ChildGuard
	log   *slog.Logger
}

// NewHandler 构造掌握度 handler。
func NewHandler(svc *Service, guard ChildGuard, log *slog.Logger) *Handler {
	return &Handler{svc: svc, guard: guard, log: log}
}

// Register 挂载 /mastery 路由。
func (h *Handler) Register(r chi.Router) {
	r.Route("/mastery", func(r chi.Router) {
		r.Get("/review-queue", h.reviewQueue)
		r.Get("/wrong-book", h.wrongBook)
		r.Delete("/wrong-book/{entryId}", h.removeWrong)
		r.Post("/{kpId}/reset", h.reset)
	})
}

// reviewQueue GET /mastery/review-queue?child_id=&subject=&limit=
func (h *Handler) reviewQueue(w http.ResponseWriter, r *http.Request) {
	_, childID, ok := h.target(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	queue, err := h.svc.ReviewQueue(r.Context(), childID, r.URL.Query().Get("subject"), limit)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, queue)
}

// wrongBook GET /mastery/wrong-book?child_id=&all=true
func (h *Handler) wrongBook(w http.ResponseWriter, r *http.Request) {
	_, childID, ok := h.target(w, r)
	if !ok {
		return
	}
	page, err := pagination.Parse(r)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	// 默认只看未移出的；?all=true 才把历史一并列出
	openOnly := r.URL.Query().Get("all") != "true"
	result, err := h.svc.WrongBook(r.Context(), childID, openOnly, page.Offset, page.Limit)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSONPaged(w, r, http.StatusOK, result.Items, response.Page{
		Offset: page.Offset, Limit: page.Limit, Total: result.Total,
	})
}

// removeWrong DELETE /mastery/wrong-book/{entryId}?child_id=
func (h *Handler) removeWrong(w http.ResponseWriter, r *http.Request) {
	_, childID, ok := h.target(w, r)
	if !ok {
		return
	}
	entryID, err := uuid.Parse(chi.URLParam(r, "entryId"))
	if err != nil {
		response.Error(w, r, h.log, errNotFound)
		return
	}
	if err := h.svc.RemoveWrongEntry(r.Context(), childID, entryID); err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, map[string]any{"removed": true})
}

// reset POST /mastery/{kpId}/reset
func (h *Handler) reset(w http.ResponseWriter, r *http.Request) {
	_, childID, ok := h.target(w, r)
	if !ok {
		return
	}
	kpID, err := uuid.Parse(chi.URLParam(r, "kpId"))
	if err != nil {
		response.Error(w, r, h.log, errNotFound)
		return
	}
	if err := h.svc.Reset(r.Context(), childID, kpID); err != nil {
		if errors.Is(err, ErrNotFound) {
			response.Error(w, r, h.log, errNotFound)
			return
		}
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, map[string]any{"reset": true})
}

// target 解析 child_id 并校验归属。校验失败已写好响应。
func (h *Handler) target(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return uuid.Nil, uuid.Nil, false
	}
	childID, err := uuid.Parse(r.URL.Query().Get("child_id"))
	if err != nil {
		response.Error(w, r, h.log, errNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	if _, err := h.guard.EnsureOwned(r.Context(), parentID, childID); err != nil {
		// 越权与不存在都回 404：不泄露「这个 ID 存在但不属于你」
		response.Error(w, r, h.log, errNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	return parentID, childID, true
}
