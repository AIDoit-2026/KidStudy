package review

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"kidstudy/internal/pkg/pagination"
	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/requestctx"
	"kidstudy/internal/platform/response"
)

// Handler 处理内容审核的 HTTP 请求。
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler 构造审核 handler。
func NewHandler(svc *Service, log *slog.Logger) *Handler { return &Handler{svc: svc, log: log} }

// Register 挂载 /review 路由（需登录；上线动作属于家长后台）。
func (h *Handler) Register(r chi.Router) {
	r.Route("/review", func(r chi.Router) {
		r.Get("/queue", h.queue)
		r.Post("/batch", h.batch)
		r.Post("/{reviewId}/approve", h.approve)
		r.Post("/{reviewId}/reject", h.reject)
	})
}

// queue GET /review/queue
func (h *Handler) queue(w http.ResponseWriter, r *http.Request) {
	parentID, ok := h.currentParent(w, r)
	if !ok {
		return
	}
	_ = parentID // 队列是全站内容，不按家长隔离；保留校验表示必须登录

	page, err := pagination.Parse(r)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}

	q := r.URL.Query()
	list, total, err := h.svc.List(r.Context(), q.Get("status"), q.Get("content_type"), page.Offset, page.Limit)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSONPaged(w, r, http.StatusOK, list, response.Page{
		Offset: page.Offset, Limit: page.Limit, Total: total,
	})
}

// approve POST /review/{reviewId}/approve
func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, true)
}

// reject POST /review/{reviewId}/reject
func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, false)
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request, approve bool) {
	parentID, ok := h.currentParent(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "reviewId")))
	if err != nil {
		response.Error(w, r, h.log, apperr.NotFound("该内容不存在或已被处理"))
		return
	}

	decision, err := h.svc.Decide(r.Context(), id, parentID, approve)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, decision)
}

// batchRequest POST /review/batch 请求体。
type batchRequest struct {
	IDs    []string `json:"ids"`
	Action string   `json:"action"` // approve | reject
}

// batch POST /review/batch
func (h *Handler) batch(w http.ResponseWriter, r *http.Request) {
	parentID, ok := h.currentParent(w, r)
	if !ok {
		return
	}
	var req batchRequest
	if !decodeBody(w, r, &req) {
		return
	}

	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, raw := range req.IDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			response.Error(w, r, h.log, apperr.ValidationFailed("批量审核参数有误", []map[string]string{
				{"field": "ids", "reason": "包含非法的内容 ID"},
			}))
			return
		}
		ids = append(ids, id)
	}

	res, err := h.svc.DecideBatch(r.Context(), ids, parentID, req.Action)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, res)
}

func (h *Handler) currentParent(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return uuid.Nil, false
	}
	return parentID, true
}

// decodeBody 解析并限制请求体大小。
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	const maxBody = 1 << 20 // 1MB

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		response.Error(w, r, slog.Default(), apperr.BadRequest("请求体读取失败"))
		return false
	}
	if len(body) == 0 {
		response.Error(w, r, slog.Default(), apperr.BadRequest("请求体不能为空"))
		return false
	}
	var uerr *json.UnmarshalTypeError
	if err := json.Unmarshal(body, dst); err != nil {
		if errors.As(err, &uerr) {
			response.Error(w, r, slog.Default(), apperr.ValidationFailed("请求参数类型有误", []map[string]string{
				{"field": uerr.Field, "reason": "类型不正确"},
			}))
			return false
		}
		response.Error(w, r, slog.Default(), apperr.BadRequest("请求体不是合法的 JSON"))
		return false
	}
	return true
}
