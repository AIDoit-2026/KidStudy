package parent

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

// Handler 处理家长控制项的读写。
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler 构造家长 handler。
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Register 挂载 /parent 路由。
func (h *Handler) Register(r chi.Router) {
	r.Route("/parent", func(r chi.Router) {
		r.Get("/settings", h.getSettings)
		r.Put("/settings", h.updateSettings)
	})
}

// getSettings GET /parent/settings
func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	parentID, ok := h.currentParent(w, r)
	if !ok {
		return
	}
	st, err := h.svc.Get(r.Context(), parentID)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, st)
}

// updateSettings PUT /parent/settings
func (h *Handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	parentID, ok := h.currentParent(w, r)
	if !ok {
		return
	}
	var req UpdateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	st, err := h.svc.Update(r.Context(), parentID, req)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, st)
}

// currentParent 取当前登录家长。
func (h *Handler) currentParent(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return uuid.Nil, false
	}
	return parentID, true
}

// decodeBody 解析 JSON 请求体，失败时已写好错误响应。
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
