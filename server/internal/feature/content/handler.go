package content

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"kidstudy/internal/pkg/pagination"
	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/response"
)

// Handler 处理内容检索的 HTTP 请求。只做解析与格式化，规则全在 service。
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler 构造内容 handler。
func NewHandler(svc *Service, log *slog.Logger) *Handler { return &Handler{svc: svc, log: log} }

// Register 挂载 /content 路由。
func (h *Handler) Register(r chi.Router) {
	r.Route("/content", func(r chi.Router) {
		r.Get("/stages", h.stages)

		r.Get("/hanzi", h.listHanzi)
		r.Get("/hanzi/{idOrChar}", h.getHanzi)

		r.Get("/words", h.listWords)

		r.Get("/stories", h.listStories)
		r.Get("/stories/{storyId}", h.getStory)
		r.Get("/stories/{storyId}/pair", h.getStoryPair)
	})
}

// stages GET /content/stages
func (h *Handler) stages(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListStages(r.Context(), r.URL.Query().Get("subject"))
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, list)
}

// listHanzi GET /content/hanzi
func (h *Handler) listHanzi(w http.ResponseWriter, r *http.Request) {
	page, err := pagination.Parse(r)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	q := r.URL.Query()

	list, total, err := h.svc.ListHanzi(r.Context(), HanziFilter{
		StageCode:    q.Get("stage"),
		Status:       q.Get("status"),
		IncludeWords: q.Get("include_words") == "true",
	}, page.Offset, page.Limit)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSONPaged(w, r, http.StatusOK, list, response.Page{
		Offset: page.Offset, Limit: page.Limit, Total: total,
	})
}

// getHanzi GET /content/hanzi/{idOrChar}
func (h *Handler) getHanzi(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.GetHanzi(r.Context(), chi.URLParam(r, "idOrChar"))
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// listWords GET /content/words
func (h *Handler) listWords(w http.ResponseWriter, r *http.Request) {
	page, err := pagination.Parse(r)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	q := r.URL.Query()

	list, total, err := h.svc.ListEnWords(r.Context(), EnWordFilter{
		LevelCode: q.Get("level"),
		Topic:     q.Get("topic"),
		Status:    q.Get("status"),
	}, page.Offset, page.Limit)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSONPaged(w, r, http.StatusOK, list, response.Page{
		Offset: page.Offset, Limit: page.Limit, Total: total,
	})
}

// listStories GET /content/stories
func (h *Handler) listStories(w http.ResponseWriter, r *http.Request) {
	page, err := pagination.Parse(r)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	q := r.URL.Query()

	list, total, err := h.svc.ListStories(r.Context(), StoryFilter{
		Lang:         q.Get("lang"),
		LevelCode:    q.Get("level"),
		Category:     q.Get("category"),
		Status:       q.Get("status"),
		SuitableOnly: q.Get("suitable_only") == "true",
	}, page.Offset, page.Limit)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSONPaged(w, r, http.StatusOK, list, response.Page{
		Offset: page.Offset, Limit: page.Limit, Total: total,
	})
}

// getStory GET /content/stories/{storyId}
func (h *Handler) getStory(w http.ResponseWriter, r *http.Request) {
	id, ok := h.storyID(w, r)
	if !ok {
		return
	}
	view, err := h.svc.GetStory(r.Context(), id)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// getStoryPair GET /content/stories/{storyId}/pair
func (h *Handler) getStoryPair(w http.ResponseWriter, r *http.Request) {
	id, ok := h.storyID(w, r)
	if !ok {
		return
	}
	view, err := h.svc.GetStoryPair(r.Context(), id)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// storyID 解析路径上的故事 ID。格式非法按 404 处理，不告诉对方「格式不对」。
func (h *Handler) storyID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "storyId"))
	id, err := uuid.Parse(raw)
	if err != nil {
		response.Error(w, r, h.log, apperr.NotFound("故事不存在"))
		return uuid.Nil, false
	}
	return id, true
}
