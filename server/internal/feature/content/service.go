package content

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
)

// Service 承载内容模块的读取规则。
//
// 它不 import net/http，也不感知 JSON —— 因此可以被 practice / report / print
// 等模块直接注入复用。
type Service struct {
	repo *Repository
	log  *slog.Logger
}

// NewService 构造内容服务。
func NewService(repo *Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// 状态取值：列表默认只看已发布内容，家长端要查待审内容时显式传 status。
const (
	StatusPublished = "published"
	StatusPending   = "pending"
	StatusRejected  = "rejected"
	StatusAll       = "all"
)

var validSubjects = map[string]bool{"chinese": true, "math": true, "english": true}

// NormalizeStatus 把 status 参数收敛成 SQL 过滤值：all → 空串（不过滤）。
func NormalizeStatus(raw string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", StatusPublished:
		return StatusPublished, nil
	case StatusPending:
		return StatusPending, nil
	case StatusRejected:
		return StatusRejected, nil
	case StatusAll:
		return "", nil
	default:
		return "", apperr.ValidationFailed("状态参数有误", []map[string]string{
			{"field": "status", "reason": "可选 published|pending|rejected|all"},
		})
	}
}

// ListStages 列出学习阶段。
func (s *Service) ListStages(ctx context.Context, subject string) ([]StageView, error) {
	subject = strings.TrimSpace(strings.ToLower(subject))
	if subject != "" && !validSubjects[subject] {
		return nil, apperr.ValidationFailed("学科参数有误", []map[string]string{
			{"field": "subject", "reason": "可选 chinese|math|english"},
		})
	}

	stages, err := s.repo.ListStages(ctx, subject)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]StageView, 0, len(stages))
	for _, st := range stages {
		out = append(out, st.ToView())
	}
	return out, nil
}

// ListHanzi 分页列出汉字。
func (s *Service) ListHanzi(ctx context.Context, f HanziFilter, offset, limit int) ([]HanziView, int64, error) {
	status, err := NormalizeStatus(f.Status)
	if err != nil {
		return nil, 0, err
	}
	f.Status = status
	f.StageCode = strings.TrimSpace(strings.ToUpper(f.StageCode))

	list, total, err := s.repo.ListHanzi(ctx, f, offset, limit)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	out := make([]HanziView, 0, len(list))
	for _, h := range list {
		out = append(out, h.ToView())
	}
	return out, total, nil
}

// GetHanzi 按 UUID 或单个汉字取详情（含笔顺字形数据）。
func (s *Service) GetHanzi(ctx context.Context, idOrChar string) (HanziDetailView, error) {
	key := strings.TrimSpace(idOrChar)
	if key == "" {
		return HanziDetailView{}, apperr.NotFound("汉字不存在")
	}

	var (
		id   uuid.UUID
		char string
	)
	if len([]rune(key)) == 1 {
		char = key
	} else {
		parsed, err := uuid.Parse(key)
		if err != nil {
			return HanziDetailView{}, apperr.NotFound("汉字不存在")
		}
		id = parsed
	}

	h, err := s.repo.GetHanzi(ctx, id, char)
	if err != nil {
		if IsNoRows(err) {
			return HanziDetailView{}, apperr.NotFound("汉字不存在")
		}
		return HanziDetailView{}, apperr.Internal(err)
	}
	return h.ToDetailView(), nil
}

// ListEnWords 分页列出英语词。
func (s *Service) ListEnWords(ctx context.Context, f EnWordFilter, offset, limit int) ([]EnWordView, int64, error) {
	status, err := NormalizeStatus(f.Status)
	if err != nil {
		return nil, 0, err
	}
	f.Status = status
	f.LevelCode = strings.TrimSpace(strings.ToUpper(f.LevelCode))

	list, total, err := s.repo.ListEnWords(ctx, f, offset, limit)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	out := make([]EnWordView, 0, len(list))
	for _, w := range list {
		out = append(out, w.ToView())
	}
	return out, total, nil
}

// ListStories 分页列出故事。
func (s *Service) ListStories(ctx context.Context, f StoryFilter, offset, limit int) ([]StoryView, int64, error) {
	lang := strings.TrimSpace(strings.ToLower(f.Lang))
	if lang != "" && lang != "zh" && lang != "en" {
		return nil, 0, apperr.ValidationFailed("语言参数有误", []map[string]string{
			{"field": "lang", "reason": "可选 zh|en"},
		})
	}
	status, err := NormalizeStatus(f.Status)
	if err != nil {
		return nil, 0, err
	}

	f.Lang = lang
	f.Status = status
	f.LevelCode = strings.TrimSpace(strings.ToUpper(f.LevelCode))
	f.Category = strings.TrimSpace(f.Category)

	list, total, err := s.repo.ListStories(ctx, f, offset, limit)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	out := make([]StoryView, 0, len(list))
	for _, st := range list {
		out = append(out, st.ToView())
	}
	return out, total, nil
}

// GetStory 取故事详情。
func (s *Service) GetStory(ctx context.Context, id uuid.UUID) (StoryDetailView, error) {
	st, err := s.repo.GetStory(ctx, id)
	if err != nil {
		if IsNoRows(err) {
			return StoryDetailView{}, apperr.NotFound("故事不存在")
		}
		return StoryDetailView{}, apperr.Internal(err)
	}
	return st.ToDetailView(), nil
}

// GetStoryPair 取双语配对；没有配对时返回 404，由前端退回单语阅读。
func (s *Service) GetStoryPair(ctx context.Context, id uuid.UUID) (StoryPairView, error) {
	pair, err := s.repo.GetStoryPair(ctx, id)
	if err != nil {
		if IsNoRows(err) {
			return StoryPairView{}, apperr.NotFound("这篇故事没有双语配对")
		}
		return StoryPairView{}, apperr.Internal(err)
	}
	return StoryPairView{
		Slug:     pair.Slug,
		AgeGroup: pair.AgeGroup,
		ZH:       pair.ZH.ToDetailView(),
		EN:       pair.EN.ToDetailView(),
	}, nil
}

// PublishStory 供审核模块调用：故事转 published。返回是否发生变更。
func (s *Service) PublishStory(ctx context.Context, id uuid.UUID) (bool, error) {
	changed, err := s.repo.PublishStory(ctx, id)
	if err != nil {
		return false, apperr.Internal(err)
	}
	return changed, nil
}

// RejectStory 供审核模块调用：故事转 rejected。返回是否发生变更。
func (s *Service) RejectStory(ctx context.Context, id uuid.UUID) (bool, error) {
	changed, err := s.repo.RejectStory(ctx, id)
	if err != nil {
		return false, apperr.Internal(err)
	}
	return changed, nil
}
