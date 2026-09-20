package children

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/requestctx"
	"kidstudy/internal/platform/response"
)

// Handler 处理孩子档案的 HTTP 请求。
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler 构造 children handler。
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Register 挂载 /children 路由，全部需要登录。
func (h *Handler) Register(r chi.Router) {
	r.Route("/children", func(r chi.Router) {
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Route("/{childId}", func(r chi.Router) {
			r.Get("/", h.get)
			r.Patch("/", h.update)
			r.Delete("/", h.archive)
		})
	})
}

// list GET /children
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	parentID, ok := h.currentParent(w, r)
	if !ok {
		return
	}
	// 归档的档案默认不出现在列表里，家长要看显式加 include_archived
	includeArchived := r.URL.Query().Get("include_archived") == "true"

	list, err := h.svc.List(r.Context(), parentID, includeArchived)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, list)
}

// create POST /children
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	parentID, ok := h.currentParent(w, r)
	if !ok {
		return
	}
	var req CreateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	view, err := h.svc.Create(r.Context(), parentID, req)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusCreated, view)
}

// get GET /children/{childId}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	parentID, childID, ok := h.target(w, r)
	if !ok {
		return
	}
	view, err := h.svc.Get(r.Context(), parentID, childID)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// update PATCH /children/{childId}
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	parentID, childID, ok := h.target(w, r)
	if !ok {
		return
	}
	var req UpdateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	view, err := h.svc.Update(r.Context(), parentID, childID, req)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// archive DELETE /children/{childId}
func (h *Handler) archive(w http.ResponseWriter, r *http.Request) {
	parentID, childID, ok := h.target(w, r)
	if !ok {
		return
	}
	if err := h.svc.Archive(r.Context(), parentID, childID); err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, map[string]any{"archived": true, "id": childID.String()})
}

// currentParent 取当前登录家长 ID。
func (h *Handler) currentParent(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return uuid.Nil, false
	}
	return parentID, true
}

// target 解析路径上的孩子 ID 并附带家长归属。
func (h *Handler) target(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	parentID, ok := h.currentParent(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	childID, err := uuid.Parse(chi.URLParam(r, "childId"))
	if err != nil {
		// 格式错误直接 404，不告诉对方「格式不对」，避免探测有效 ID
		response.Error(w, r, h.log, errNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	return parentID, childID, true
}

// decodeBody 解析并限制请求体大小，失败时已写好错误响应。
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
