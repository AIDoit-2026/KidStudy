package practice

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

// errNotFound 越权与不存在共用，避免泄露某个 ID 是否真实存在。
var errNotFound = apperr.NotFound("内容不存在或无权访问")

// ChildGuard 校验孩子归属，由 children.Service 实现。
type ChildGuard interface {
	EnsureOwned(ctx context.Context, parentID, childID uuid.UUID) (children.Child, error)
}

// Handler 处理练习相关的 HTTP 请求。只做解析与格式化。
type Handler struct {
	svc   *Service
	guard ChildGuard
	log   *slog.Logger
}

// NewHandler 构造练习 handler。
func NewHandler(svc *Service, guard ChildGuard, log *slog.Logger) *Handler {
	return &Handler{svc: svc, guard: guard, log: log}
}

// Register 挂载 /practice 路由。
func (h *Handler) Register(r chi.Router) {
	r.Route("/practice", func(r chi.Router) {
		r.Get("/today", h.today)
		r.Post("/session", h.startSession)
		r.Get("/session/{sessionId}", h.getSession)
		r.Post("/session/{sessionId}/answer", h.answer)
		r.Post("/session/{sessionId}/skip", h.skip)
		r.Post("/session/{sessionId}/finish", h.finish)
		r.Post("/session/{sessionId}/confirm", h.confirm)

		r.Get("/math/templates", h.mathTemplates)
		r.Get("/math/preview", h.mathPreview)
	})
}

// today GET /practice/today?child_id=&subject=
func (h *Handler) today(w http.ResponseWriter, r *http.Request) {
	parentID, child, ok := h.child(w, r)
	if !ok {
		return
	}
	plan, err := h.svc.Plan(r.Context(), parentID, child.ID, deref(child.StageCode), splitSubjects(r.URL.Query().Get("subject")))
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, plan)
}

// startSession POST /practice/session
func (h *Handler) startSession(w http.ResponseWriter, r *http.Request) {
	parentID, child, ok := h.childFromBody(w, r)
	if !ok {
		return
	}
	var req struct {
		ChildID    string `json:"child_id"`
		DeviceType string `json:"device_type"`
		Subject    string `json:"subject"`
	}
	if !decode(w, r, h.log, &req) {
		return
	}
	view, err := h.svc.StartSession(r.Context(), parentID, child.ID, deref(child.StageCode), req.DeviceType, req.Subject)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusCreated, view)
}

// getSession GET /practice/session/{sessionId}?child_id=
func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	_, childID, ok := h.target(w, r)
	if !ok {
		return
	}
	sessionID, err := uuid.Parse(chi.URLParam(r, "sessionId"))
	if err != nil {
		response.Error(w, r, h.log, errNotFound)
		return
	}
	view, err := h.svc.GetSession(r.Context(), childID, sessionID)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// answer POST /practice/session/{sessionId}/answer
func (h *Handler) answer(w http.ResponseWriter, r *http.Request) {
	childID, sessionID, ok := h.sessionTarget(w, r)
	if !ok {
		return
	}
	var req struct {
		ItemID    string          `json:"item_id"`
		Answer    json.RawMessage `json:"answer"`
		UsedHint  bool            `json:"used_hint"`
		ElapsedMS int             `json:"elapsed_ms"`
	}
	if !decode(w, r, h.log, &req) {
		return
	}
	itemID, err := uuid.Parse(req.ItemID)
	if err != nil {
		response.Error(w, r, h.log, apperr.BadRequest("item_id 不是合法的 ID"))
		return
	}
	answer, err := ParseAnswer(req.Answer)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	result, err := h.svc.Grade(r.Context(), childID, sessionID, itemID, answer, req.UsedHint, req.ElapsedMS)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, result)
}

// skip POST /practice/session/{sessionId}/skip
func (h *Handler) skip(w http.ResponseWriter, r *http.Request) {
	childID, sessionID, ok := h.sessionTarget(w, r)
	if !ok {
		return
	}
	var req struct {
		ItemID string `json:"item_id"`
	}
	if !decode(w, r, h.log, &req) {
		return
	}
	itemID, err := uuid.Parse(req.ItemID)
	if err != nil {
		response.Error(w, r, h.log, apperr.BadRequest("item_id 不是合法的 ID"))
		return
	}
	result, err := h.svc.Skip(r.Context(), childID, sessionID, itemID)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, result)
}

// finish POST /practice/session/{sessionId}/finish
func (h *Handler) finish(w http.ResponseWriter, r *http.Request) {
	childID, sessionID, ok := h.sessionTarget(w, r)
	if !ok {
		return
	}
	summary, err := h.svc.Finish(r.Context(), childID, sessionID)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, summary)
}

// confirm POST /practice/session/{sessionId}/confirm
func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	childID, sessionID, ok := h.sessionTarget(w, r)
	if !ok {
		return
	}
	var req ConfirmRequest
	if !decode(w, r, h.log, &req) {
		return
	}
	view, err := h.svc.Confirm(r.Context(), childID, sessionID, req)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// mathTemplates GET /practice/math/templates
func (h *Handler) mathTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.MathTemplates(r.Context())
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, list)
}

// mathPreview GET /practice/math/preview?template=M3_ADD20&count=20&seed=42
//
// 与打印中心同源：同一 (template, count, seed) 必得同一批题。
func (h *Handler) mathPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code := q.Get("template")
	if code == "" {
		response.Error(w, r, h.log, apperr.BadRequest("template 不能为空"))
		return
	}
	count, _ := strconv.Atoi(q.Get("count"))
	seed, err := strconv.ParseUint(q.Get("seed"), 10, 64)
	if err != nil {
		// 没传 seed 就固定用 1：宁可 predictable 也不要每次都变
		seed = 1
	}
	questions, err := h.svc.MathPreview(r.Context(), code, count, seed)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, questions)
}

// ------------------------------------------------------------------ 请求解析

// child 从 query 取 child_id 并校验归属。
func (h *Handler) child(w http.ResponseWriter, r *http.Request) (uuid.UUID, children.Child, bool) {
	parentID, childID, ok := h.target(w, r)
	if !ok {
		return uuid.Nil, children.Child{}, false
	}
	c, err := h.guard.EnsureOwned(r.Context(), parentID, childID)
	if err != nil {
		response.Error(w, r, h.log, errNotFound)
		return uuid.Nil, children.Child{}, false
	}
	return parentID, c, true
}

// childFromBody 从请求体取 child_id（POST 场景）。
func (h *Handler) childFromBody(w http.ResponseWriter, r *http.Request) (uuid.UUID, children.Child, bool) {
	var probe struct {
		ChildID string `json:"child_id"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		response.Error(w, r, h.log, apperr.BadRequest("请求体读取失败"))
		return uuid.Nil, children.Child{}, false
	}
	if len(body) == 0 {
		response.Error(w, r, h.log, apperr.BadRequest("请求体不能为空"))
		return uuid.Nil, children.Child{}, false
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		response.Error(w, r, h.log, apperr.BadRequest("请求体不是合法的 JSON"))
		return uuid.Nil, children.Child{}, false
	}
	// 把 body 放回去，业务解析还要再读一次
	r.Body = io.NopCloser(bytes.NewReader(body))
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return uuid.Nil, children.Child{}, false
	}
	childID, err := uuid.Parse(probe.ChildID)
	if err != nil {
		// 缺 child_id 是请求写错了（422），不是「没这个孩子」（404）
		response.Error(w, r, h.log, apperr.BadRequest("child_id 缺失或不是合法的 ID"))
		return uuid.Nil, children.Child{}, false
	}
	c, err := h.guard.EnsureOwned(r.Context(), parentID, childID)
	if err != nil {
		response.Error(w, r, h.log, errNotFound)
		return uuid.Nil, children.Child{}, false
	}
	return parentID, c, true
}

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
		response.Error(w, r, h.log, errNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	return parentID, childID, true
}

// sessionTarget 解析会话作用域请求的目标：session 来自路径，child 先看 query 再看请求体。
//
// 两个位置都认是为了让 GET（只能用 query）与 POST（习惯放 body）保持同一套调用方式；
// 读 body 时会原样放回去，后面的业务解析还能再读一次。
func (h *Handler) sessionTarget(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return uuid.Nil, uuid.Nil, false
	}
	sessionID, err := uuid.Parse(chi.URLParam(r, "sessionId"))
	if err != nil {
		response.Error(w, r, h.log, errNotFound)
		return uuid.Nil, uuid.Nil, false
	}

	childID, perr := uuid.Parse(r.URL.Query().Get("child_id"))
	if perr != nil {
		if fromBody := childIDFromBody(r); fromBody != uuid.Nil {
			childID = fromBody
		}
	}
	c, err := h.guard.EnsureOwned(r.Context(), parentID, childID)
	if err != nil || c.ID != childID {
		response.Error(w, r, h.log, errNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	return childID, sessionID, true
}

// childIDFromBody 从请求体里取 child_id，读完把 body 放回去。
func childIDFromBody(r *http.Request) uuid.UUID {
	if r.Body == nil {
		return uuid.Nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return uuid.Nil
	}
	var probe struct {
		ChildID string `json:"child_id"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return uuid.Nil
	}
	id, err := uuid.Parse(probe.ChildID)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func decode(w http.ResponseWriter, r *http.Request, log *slog.Logger, dst any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		response.Error(w, r, log, apperr.BadRequest("请求体读取失败"))
		return false
	}
	if len(body) == 0 {
		response.Error(w, r, log, apperr.BadRequest("请求体不能为空"))
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		response.Error(w, r, log, apperr.BadRequest("请求体不是合法的 JSON"))
		return false
	}
	return true
}
