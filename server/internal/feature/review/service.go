package review

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
)

// Service 承载审核规则。
type Service struct {
	repo *Repository
	log  *slog.Logger
}

// NewService 构造审核服务。
func NewService(repo *Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// List 分页列出队列条目。status 为空默认只看待审。
func (s *Service) List(ctx context.Context, status, contentType string, offset, limit int) ([]ItemView, int64, error) {
	status = strings.TrimSpace(strings.ToLower(status))
	switch status {
	case "", "pending":
		status = "pending"
	case "approved", "rejected", "all":
		if status == "all" {
			status = ""
		}
	default:
		return nil, 0, apperr.ValidationFailed("状态参数有误", []map[string]string{
			{"field": "status", "reason": "可选 pending|approved|rejected|all"},
		})
	}

	contentType = strings.TrimSpace(strings.ToLower(contentType))
	switch contentType {
	case "", "story", "hanzi_word", "en_word":
	default:
		return nil, 0, apperr.ValidationFailed("内容类型有误", []map[string]string{
			{"field": "content_type", "reason": "可选 story|hanzi_word|en_word"},
		})
	}

	items, total, err := s.repo.List(ctx, status, contentType, offset, limit)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	out := make([]ItemView, 0, len(items))
	for _, it := range items {
		out = append(out, it.ToView())
	}
	return out, total, nil
}

// Decide 处理单条：approve 为真表示通过，否则驳回。
func (s *Service) Decide(ctx context.Context, id, reviewerID uuid.UUID, approve bool) (Decision, error) {
	decision, ok, err := s.repo.Decide(ctx, id, reviewerID, approve)
	if err != nil {
		return Decision{}, apperr.Internal(err)
	}
	if !ok {
		return Decision{}, apperr.NotFound("该内容不存在或已被处理")
	}
	return decision, nil
}

// DecideBatch 批量处理。逐条独立成事务：一条失败不影响其余，结果里给出跳过数。
func (s *Service) DecideBatch(ctx context.Context, ids []uuid.UUID, reviewerID uuid.UUID, action string) (BatchResult, error) {
	approve, err := parseAction(action)
	if err != nil {
		return BatchResult{}, err
	}
	if len(ids) == 0 {
		return BatchResult{}, apperr.ValidationFailed("批量审核参数有误", []map[string]string{
			{"field": "ids", "reason": "至少提供一条内容 ID"},
		})
	}
	if len(ids) > 500 {
		return BatchResult{}, apperr.ValidationFailed("批量审核参数有误", []map[string]string{
			{"field": "ids", "reason": "单次最多 500 条"},
		})
	}

	res := BatchResult{Requested: len(ids)}
	for _, id := range ids {
		_, ok, err := s.repo.Decide(ctx, id, reviewerID, approve)
		if err != nil {
			// 单条失败只记日志，不打断整批（家长点一次「全选通过」不该被一条坏数据卡住）
			s.log.Warn("批量审核单条失败", "review_id", id.String(), "error", err)
			res.Skipped++
			continue
		}
		if !ok {
			res.Skipped++
			continue
		}
		res.Applied++
	}
	return res, nil
}

// parseAction 校验批量动作。
func parseAction(action string) (bool, error) {
	switch strings.TrimSpace(strings.ToLower(action)) {
	case DecisionApprove:
		return true, nil
	case DecisionReject:
		return false, nil
	default:
		return false, apperr.ValidationFailed("批量审核参数有误", []map[string]string{
			{"field": "action", "reason": "可选 approve|reject"},
		})
	}
}
