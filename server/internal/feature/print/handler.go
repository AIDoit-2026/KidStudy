package print

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
	"kidstudy/internal/platform/middleware"
	"kidstudy/internal/platform/requestctx"
	"kidstudy/internal/platform/response"
	"kidstudy/internal/platform/storage"
)

// Handler 处理打印中心的读写（§4.6）。
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler 构造打印 handler。
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Register 挂载 /print 与 /parent/print-jobs 路由。
//
// /parent/print-jobs 由本模块注册而不是 parent 模块：它是打印记录列表，
// 数据与口径都在这里，放过去只会多一层转发。
//
// 只读端点不限流；建任务/预览/出 PDF 是重活（查内容装 payload、渲染 HTML、
// 触发 worker 的 Chromium 渲染），统一走打印档限流。
func (h *Handler) Register(r chi.Router, limiters *middleware.Limiters) {
	r.Get("/print/templates", h.templates)
	r.Get("/print/jobs/{id}", h.getJob)
	r.Get("/print/jobs/{id}/data", h.jobData)
	r.Get("/print/jobs/{id}/pdf", h.downloadPDF)
	r.Get("/parent/print-jobs", h.listJobs)
	r.Post("/print/jobs/{id}/mark-done", h.markDone)

	r.Group(func(r chi.Router) {
		r.Use(limiters.Print)
		r.Post("/print/jobs", h.createJob)
		r.Get("/print/jobs/{id}/preview", h.preview)
		r.Post("/print/jobs/{id}/pdf", h.queuePDF)
	})
}

// templates GET /print/templates
func (h *Handler) templates(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.parent(w, r); !ok {
		return
	}
	response.JSON(w, r, http.StatusOK, map[string]any{"templates": h.svc.Templates()})
}

// createJob POST /print/jobs
func (h *Handler) createJob(w http.ResponseWriter, r *http.Request) {
	parentID, ok := h.parent(w, r)
	if !ok {
		return
	}
	var body struct {
		TemplateCode string          `json:"template_code"`
		ChildID      string          `json:"child_id"`
		Params       json.RawMessage `json:"params"`
	}
	if !h.decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.TemplateCode) == "" {
		response.Error(w, r, h.log, apperr.BadRequest("请选择打印模板"))
		return
	}

	req := CreateRequest{TemplateCode: body.TemplateCode, Params: Params{}}
	if strings.TrimSpace(body.ChildID) != "" {
		childID, err := uuid.Parse(strings.TrimSpace(body.ChildID))
		if err != nil {
			response.Error(w, r, h.log, apperr.BadRequest("孩子 ID 格式不正确"))
			return
		}
		req.ChildID = &childID
	}
	if len(body.Params) > 0 {
		if err := json.Unmarshal(body.Params, &req.Params); err != nil {
			response.Error(w, r, h.log, apperr.BadRequest("打印参数必须是 JSON 对象"))
			return
		}
	}

	view, err := h.svc.CreateJob(r.Context(), parentID, req)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusCreated, view)
}

// getJob GET /print/jobs/{id}
func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	parentID, id, ok := h.target(w, r)
	if !ok {
		return
	}
	view, err := h.svc.GetJob(r.Context(), parentID, id)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// jobData GET /print/jobs/{id}/data —— 返回渲染数据快照。
func (h *Handler) jobData(w http.ResponseWriter, r *http.Request) {
	parentID, id, ok := h.target(w, r)
	if !ok {
		return
	}
	payload, err := h.svc.Payload(r.Context(), parentID, id)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, payload)
}

// preview GET /print/jobs/{id}/preview —— 返回可直接打印的 HTML（无壳布局）。
func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	parentID, id, ok := h.target(w, r)
	if !ok {
		return
	}
	html, err := h.svc.PreviewHTML(r.Context(), parentID, id)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, html)
}

// queuePDF POST /print/jobs/{id}/pdf —— 排队渲染。
//
// 这里只入队就返回 202：Chromium 渲染是秒级长任务，放在请求处理器里会占着
// 连接与工作协程（§8 后台任务约定）。真正的渲染由 cmd/worker 消费。
func (h *Handler) queuePDF(w http.ResponseWriter, r *http.Request) {
	parentID, id, ok := h.target(w, r)
	if !ok {
		return
	}
	view, err := h.svc.QueuePDF(r.Context(), parentID, id)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusAccepted, view)
}

// downloadPDF GET /print/jobs/{id}/pdf
func (h *Handler) downloadPDF(w http.ResponseWriter, r *http.Request) {
	parentID, id, ok := h.target(w, r)
	if !ok {
		return
	}
	rel, notReady, err := h.svc.PDFPath(r.Context(), parentID, id)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	if notReady {
		response.Error(w, r, h.log, apperr.Conflict("PDF 还没生成好，请稍后再试或先打印当前预览页"))
		return
	}

	rc, err := h.svc.store.Get(r.Context(), rel)
	if err != nil {
		if errors.Is(err, storage.ErrNotExist) {
			// 库里有记录但文件不在：多半是过了保留期被清理，提示重新生成
			response.Error(w, r, h.log, apperr.NotFound("PDF 文件已过期，请重新生成"))
			return
		}
		response.Error(w, r, h.log, apperr.Internal(err))
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/pdf")
	// inline：家长在浏览器里直接看到，需要的话再另存
	w.Header().Set("Content-Disposition", `inline; filename="kidstudy-`+id.String()+`.pdf"`)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if _, err := io.Copy(w, rc); err != nil {
		// 响应头已经发出去了，这里只能记日志
		h.log.Warn("写出 PDF 失败", "job_id", id, "error", err)
	}
}

// markDone POST /print/jobs/{id}/mark-done —— 纸质补录（§4.6 步骤 6）。
func (h *Handler) markDone(w http.ResponseWriter, r *http.Request) {
	parentID, id, ok := h.target(w, r)
	if !ok {
		return
	}
	// body 允许为空（表示整张都做对了），所以不强制要求非空 JSON
	var req MarkDoneRequest
	if r.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			response.Error(w, r, h.log, apperr.BadRequest("请求体读取失败"))
			return
		}
		if len(strings.TrimSpace(string(raw))) > 0 {
			if err := json.Unmarshal(raw, &req); err != nil {
				response.Error(w, r, h.log, apperr.BadRequest("请求体不是合法的 JSON"))
				return
			}
		}
	}

	res, err := h.svc.MarkDone(r.Context(), parentID, id, req)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, res)
}

// listJobs GET /parent/print-jobs?childId=&offset=&limit=
func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	parentID, ok := h.parent(w, r)
	if !ok {
		return
	}
	page, err := pagination.Parse(r)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	childID := uuid.Nil
	if raw := strings.TrimSpace(r.URL.Query().Get("childId")); raw != "" {
		parsed, perr := uuid.Parse(raw)
		if perr != nil {
			response.Error(w, r, h.log, apperr.BadRequest("孩子 ID 格式不正确"))
			return
		}
		childID = parsed
	}

	list, total, err := h.svc.List(r.Context(), parentID, childID, page.Limit, page.Offset)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSONPaged(w, r, http.StatusOK, list, response.Page{
		Offset: page.Offset, Limit: page.Limit, Total: total,
	})
}

// ---------------------------------------------------------------- 小工具

// parent 取当前登录家长，失败时已写好 401。
func (h *Handler) parent(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return uuid.Nil, false
	}
	return parentID, true
}

// target 解析 {id} 并返回家长 id。
func (h *Handler) target(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	parentID, ok := h.parent(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	id, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		// 非法 id 与「不属于我」返回同样的 404，不暴露格式细节
		response.Error(w, r, h.log, apperr.NotFound("打印任务不存在"))
		return uuid.Nil, uuid.Nil, false
	}
	return parentID, id, true
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		response.Error(w, r, h.log, apperr.BadRequest("请求体读取失败"))
		return false
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		response.Error(w, r, h.log, apperr.BadRequest("请求体不能为空"))
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		response.Error(w, r, h.log, apperr.BadRequest("请求体不是合法的 JSON"))
		return false
	}
	return true
}
