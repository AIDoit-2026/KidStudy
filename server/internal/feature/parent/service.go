package parent

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
)

// Service 负责设置项的默认值、校验与合并。
type Service struct {
	repo *Repository
	log  *slog.Logger
}

// NewService 构造成长设置服务。
func NewService(repo *Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Get 取设置；没有记录时返回默认值（不落库，等家长第一次保存再写）。
func (s *Service) Get(ctx context.Context, parentID uuid.UUID) (Settings, error) {
	st, err := s.repo.Get(ctx, parentID)
	if err != nil {
		return Settings{}, apperr.Internal(err)
	}
	if st == nil {
		return Settings{
			DailyLimitMin:   DefaultDailyLimitMin,
			SessionLimitMin: DefaultSessionLimitMin,
			RestIntervalMin: DefaultRestIntervalMin,
			PaceMode:        "standard",
			// 对比模块默认开启（§4.10），家长可关
			CompareChildren: true,
		}, nil
	}
	return *st, nil
}

// Update 部分更新：先合并再校验，最后整体 upsert。
func (s *Service) Update(ctx context.Context, parentID uuid.UUID, req UpdateRequest) (Settings, error) {
	cur, err := s.Get(ctx, parentID)
	if err != nil {
		return Settings{}, err
	}

	if req.DailyLimitMin != nil {
		cur.DailyLimitMin = *req.DailyLimitMin
	}
	if req.SessionLimitMin != nil {
		cur.SessionLimitMin = *req.SessionLimitMin
	}
	if req.RestIntervalMin != nil {
		cur.RestIntervalMin = *req.RestIntervalMin
	}
	if req.RequireParentConfirm != nil {
		cur.RequireParentConfirm = *req.RequireParentConfirm
	}
	if req.PaceMode != nil {
		cur.PaceMode = *req.PaceMode
	}
	if req.CompareChildren != nil {
		cur.CompareChildren = *req.CompareChildren
	}

	if err := validate(cur); err != nil {
		return Settings{}, err
	}

	saved, err := s.repo.Upsert(ctx, parentID, cur)
	if err != nil {
		return Settings{}, apperr.Internal(err)
	}
	s.log.Info("家长设置已更新",
		"parent_id", parentID,
		"daily_limit_min", saved.DailyLimitMin,
		"require_parent_confirm", saved.RequireParentConfirm,
		"pace_mode", saved.PaceMode)
	return saved, nil
}

// SetPaceMode 单独切换节奏模式（报表建议「设为复习日」「调整每日量」的一键动作）。
// 直接复用 Update 的部分更新与校验路径，避免动作端点绕过设置校验写库。
func (s *Service) SetPaceMode(ctx context.Context, parentID uuid.UUID, mode string) error {
	_, err := s.Update(ctx, parentID, UpdateRequest{PaceMode: &mode})
	return err
}

// validate 范围校验。边界值都给了明确文案，前端直接把 message 展示给家长。
func validate(s Settings) error {
	if s.DailyLimitMin < 0 || s.DailyLimitMin > 480 {
		return apperr.BadRequest("每日总时长需在 0 到 480 分钟之间（0 表示不限）")
	}
	if s.SessionLimitMin < 5 || s.SessionLimitMin > 120 {
		return apperr.BadRequest("单次会话时长需在 5 到 120 分钟之间")
	}
	if s.RestIntervalMin < 0 || s.RestIntervalMin > 120 {
		return apperr.BadRequest("休息间隔需在 0 到 120 分钟之间（0 表示不强制）")
	}
	if s.DailyLimitMin > 0 && s.SessionLimitMin > s.DailyLimitMin {
		return apperr.BadRequest("单次会话时长不能大于每日总时长")
	}
	switch s.PaceMode {
	case "standard", "fast", "review":
	default:
		return apperr.BadRequest("节奏模式只能是 standard / fast / review")
	}
	return nil
}
