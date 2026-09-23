package report

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
)

// 建议动作类型，与 SuggestionAction.Type 一一对应（§4.7 的四条规则）。
const (
	ActionReviewDay       = "review_day"       // 设为复习日
	ActionTuneQuota       = "tune_quota"       // 调整每日量
	ActionAssignPractice  = "assign_practice"  // 生成专项练习
	ActionLowerDifficulty = "lower_difficulty" // 降一档
	ActionBalanceSubjects = "balance_subjects" // 均衡安排
)

// 动作执行需要的跨模块能力。各由拥有数据的模块实现（practice / mastery / parent 服务），
// report 只依赖接口，且**签名只用 uuid / string 这类基础类型** —— 这样 report 不必
// import practice，维持「report 不认识 practice」的单向约定（§2.2）。
type (
	// AssignmentWriter 由 practice.Service 实现。
	AssignmentWriter interface {
		AssignKP(ctx context.Context, parentID, childID, kpID uuid.UUID, reason string) error
		AssignNextOfSubject(ctx context.Context, parentID, childID uuid.UUID, subject, reason string) (uuid.UUID, string, error)
	}
	// DifficultyLowerer 由 mastery.Service 实现。
	DifficultyLowerer interface {
		LowerDifficulty(ctx context.Context, childID, kpID uuid.UUID) (int, error)
	}
	// PaceModeSetter 由 parent.Service 实现。
	PaceModeSetter interface {
		SetPaceMode(ctx context.Context, parentID uuid.UUID, mode string) error
	}
)

// errActionsUnavailable 动作依赖未注入（只在 worker 进程可能出现；API 进程一定注入了）。
var errActionsUnavailable = apperr.New(apperr.CodeInternal, http.StatusServiceUnavailable, "该操作暂时不可用")

// WithActions 后置注入动作依赖。
//
// 为什么不用构造函数：report 与 practice **互为依赖** —— report 用 practice 执行专项指派，
// practice 用 report 评测成就（BadgeAwarder）。构造顺序无法同时满足，所以动作依赖后置注入。
func (s *Service) WithActions(assigner AssignmentWriter, difficulty DifficultyLowerer, pace PaceModeSetter) *Service {
	s.assigner, s.difficulty, s.pace = assigner, difficulty, pace
	return s
}

// ApplyAction 执行一条建议附带的动作（§4.7）。
func (s *Service) ApplyAction(ctx context.Context, parentID, childID uuid.UUID, req SuggestionActionRequest) (SuggestionActionResult, error) {
	switch req.Type {
	case ActionAssignPractice:
		return s.applyAssignPractice(ctx, parentID, childID, req)
	case ActionLowerDifficulty:
		return s.applyLowerDifficulty(ctx, childID, req)
	case ActionBalanceSubjects:
		return s.applyBalanceSubjects(ctx, parentID, childID, req)
	case ActionReviewDay, ActionTuneQuota:
		return s.applyPace(ctx, parentID, req.Type)
	default:
		return SuggestionActionResult{}, apperr.BadRequest("不支持的建议动作：" + req.Type)
	}
}

// applyAssignPractice 「生成专项练习」：把指定的知识点抬进孩子的专项队列。
func (s *Service) applyAssignPractice(ctx context.Context, parentID, childID uuid.UUID, req SuggestionActionRequest) (SuggestionActionResult, error) {
	if s.assigner == nil {
		return SuggestionActionResult{}, errActionsUnavailable
	}
	kpID, err := parseKPID(req.KPID)
	if err != nil {
		return SuggestionActionResult{}, err
	}
	if err := s.assigner.AssignKP(ctx, parentID, childID, kpID, "报表建议：专项练习"); err != nil {
		return SuggestionActionResult{}, err
	}
	return SuggestionActionResult{
		Type:    ActionAssignPractice,
		Applied: true,
		Detail:  fmt.Sprintf("已把「%s」加入专项练习，会优先出现在今日任务", s.kpName(ctx, kpID)),
		Data:    map[string]any{"kp_id": kpID.String()},
	}, nil
}

// applyLowerDifficulty 「降一档」：把指定知识点的难度降一档（已在最低档则原地不动）。
func (s *Service) applyLowerDifficulty(ctx context.Context, childID uuid.UUID, req SuggestionActionRequest) (SuggestionActionResult, error) {
	if s.difficulty == nil {
		return SuggestionActionResult{}, errActionsUnavailable
	}
	kpID, err := parseKPID(req.KPID)
	if err != nil {
		return SuggestionActionResult{}, err
	}
	name := s.kpName(ctx, kpID)
	level, err := s.difficulty.LowerDifficulty(ctx, childID, kpID)
	if err != nil {
		return SuggestionActionResult{}, err
	}
	detail := fmt.Sprintf("「%s」的难度已降一档（现在第 %d 档）", name, level)
	if level <= 1 {
		detail = fmt.Sprintf("「%s」已经在最低档了，改从「生成专项练习」入手更合适", name)
	}
	return SuggestionActionResult{
		Type:    ActionLowerDifficulty,
		Applied: true,
		Detail:  detail,
		Data:    map[string]any{"kp_id": kpID.String(), "difficulty": level},
	}, nil
}

// applyBalanceSubjects 「安排一点」：为该学科挑一个知识点指派，把学习面拉回来。
func (s *Service) applyBalanceSubjects(ctx context.Context, parentID, childID uuid.UUID, req SuggestionActionRequest) (SuggestionActionResult, error) {
	if s.assigner == nil {
		return SuggestionActionResult{}, errActionsUnavailable
	}
	if !validSubject(req.Subject) {
		return SuggestionActionResult{}, errBadSubject
	}
	kpID, name, err := s.assigner.AssignNextOfSubject(ctx, parentID, childID, req.Subject, "报表建议：均衡安排")
	if err != nil {
		return SuggestionActionResult{}, err
	}
	return SuggestionActionResult{
		Type:    ActionBalanceSubjects,
		Applied: true,
		Detail:  fmt.Sprintf("已为%s安排「%s」，会进今日任务", subjectNames[req.Subject], name),
		Data:    map[string]any{"subject": req.Subject, "kp_id": kpID.String(), "name": name},
	}, nil
}

// applyPace 处理两个「改节奏模式」的动作：复习日 → review；调整每日量 → fast。
//
// 动作改的是家长设置里的 pace_mode（迁移 0007 起 review 与 fast 都真正参与编排），
// 不是临时开关 —— 文案里说明「可在设置里改回」，避免家长以为是一次性动作。
func (s *Service) applyPace(ctx context.Context, parentID uuid.UUID, action string) (SuggestionActionResult, error) {
	if s.pace == nil {
		return SuggestionActionResult{}, errActionsUnavailable
	}
	mode := "review"
	detail := "已设为复习日：今天不再安排新学，先把学过的过一遍（可在设置里改回标准节奏）"
	if action == ActionTuneQuota {
		mode = "fast"
		detail = "已上调每日新学量（节奏模式：加快，可在设置里改回标准节奏）"
	}
	if err := s.pace.SetPaceMode(ctx, parentID, mode); err != nil {
		return SuggestionActionResult{}, err
	}
	return SuggestionActionResult{
		Type:    action,
		Applied: true,
		Detail:  detail,
		Data:    map[string]any{"pace_mode": mode},
	}, nil
}

// kpName 取知识点名称用于文案；查不到时退回中性说法，不因为「取名字失败」让动作整体失败。
func (s *Service) kpName(ctx context.Context, kpID uuid.UUID) string {
	rows, err := s.repo.KPsByIDs(ctx, []uuid.UUID{kpID})
	if err != nil || len(rows) == 0 {
		return "该知识点"
	}
	return rows[0].Name
}

func parseKPID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, apperr.BadRequest("kp_id 需要是合法的 UUID")
	}
	return id, nil
}
