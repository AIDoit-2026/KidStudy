package report

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// badgeRule 是 badges.rule 的 Go 映射。
//
// 规则放库里（而不是写死在代码里）的原因：门槛调整不该走一次发版；
// 家长端还能直接把 rule 渲染成「掌握 10 个汉字」这类可读文案。
type badgeRule struct {
	Type      string `json:"type"`
	Subject   string `json:"subject"`
	Threshold int64  `json:"threshold"`
}

// badgeFacts 一次算全的评测素材。
type badgeFacts struct {
	SessionCount     int64
	PerfectSessions  int64
	StarTotal        int64
	MasteredTotal    int64
	MasteredChinese  int64
	MasteredMath     int64
	MasteredEnglish  int64
	PassedStreakDays int64
}

// EvaluateBadges 评测并授予孩子应得的徽章，返回本次新授予的项。
//
// 幂等：child_badges 的 unique(child_id,badge_id) + ON CONFLICT DO NOTHING，
// 重复调用不会重复发；调用时机有两个 —— 会话结算后（即时反馈）与日汇总时（补连续类）。
func (s *Service) EvaluateBadges(ctx context.Context, childID uuid.UUID) ([]EarnedBadge, error) {
	catalog, err := s.repo.ListBadges(ctx)
	if err != nil {
		return nil, err
	}
	if len(catalog) == 0 {
		return nil, nil
	}
	earned, err := s.repo.ListChildBadges(ctx, childID)
	if err != nil {
		return nil, err
	}
	have := make(map[uuid.UUID]struct{}, len(earned))
	for _, e := range earned {
		have[e.BadgeID] = struct{}{}
	}

	facts, err := s.collectBadgeFacts(ctx, childID)
	if err != nil {
		return nil, err
	}

	var out []EarnedBadge
	for _, b := range catalog {
		if _, ok := have[b.ID]; ok {
			continue
		}
		var rule badgeRule
		if err := json.Unmarshal(b.Rule, &rule); err != nil {
			s.log.Warn("徽章规则无法解析，跳过", "badge", b.Code, "error", err)
			continue
		}
		ok, snapshot := rule.satisfied(facts)
		if !ok {
			continue
		}
		raw, _ := json.Marshal(snapshot)
		awarded, err := s.repo.AwardBadge(ctx, childID, b.ID, raw)
		if err != nil {
			return out, fmt.Errorf("授予徽章 %s 失败: %w", b.Code, err)
		}
		if awarded {
			out = append(out, EarnedBadge{Code: b.Code, Name: b.Name})
			s.log.Info("授予徽章", "child_id", childID, "badge", b.Code)
		}
	}
	return out, nil
}

// AwardChild 评测并授予单个孩子的成就，返回本次新授予数量。
//
// 这个方法是给别的模块（practice 会话结算）通过接口调的：只回数量不回徽章对象，
// 调用方就不需要 import report 的类型，跨模块依赖仍是单向的。
func (s *Service) AwardChild(ctx context.Context, childID uuid.UUID) (int, error) {
	earned, err := s.EvaluateBadges(ctx, childID)
	if err != nil {
		return 0, err
	}
	return len(earned), nil
}

// AwardAll 批量评测成就，返回本次新授予的数量（worker 用）。
func (s *Service) AwardAll(ctx context.Context, childIDs []uuid.UUID) (int, error) {
	children, err := s.childrenFor(ctx, childIDs)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, c := range children {
		earned, err := s.EvaluateBadges(ctx, c.ID)
		if err != nil {
			return total, err
		}
		total += len(earned)
	}
	return total, nil
}

// collectBadgeFacts 汇总评测素材。
func (s *Service) collectBadgeFacts(ctx context.Context, childID uuid.UUID) (badgeFacts, error) {
	var f badgeFacts

	sess, err := s.repo.BadgeSessionStats(ctx, childID)
	if err != nil {
		return f, err
	}
	f.SessionCount, f.PerfectSessions, f.StarTotal = sess.SessionCount, sess.PerfectSessions, sess.StarTotal

	ms, err := s.repo.BadgeMasteryStats(ctx, childID)
	if err != nil {
		return f, err
	}
	f.MasteredTotal = ms.MasteredTotal
	f.MasteredChinese, f.MasteredMath, f.MasteredEnglish = ms.MasteredChinese, ms.MasteredMath, ms.MasteredEnglish

	streak, err := s.PassedStreak(ctx, childID)
	if err != nil {
		return f, err
	}
	f.PassedStreakDays = int64(streak)
	return f, nil
}

// satisfied 判断规则是否达成，并返回事实快照（存进 child_badges.progress）。
func (r badgeRule) satisfied(f badgeFacts) (bool, map[string]any) {
	switch r.Type {
	case "session_count":
		return f.SessionCount >= r.Threshold, map[string]any{
			"session_count": f.SessionCount, "threshold": r.Threshold,
		}
	case "perfect_sessions":
		return f.PerfectSessions >= r.Threshold, map[string]any{
			"perfect_sessions": f.PerfectSessions, "threshold": r.Threshold,
		}
	case "star_total":
		return f.StarTotal >= r.Threshold, map[string]any{
			"star_total": f.StarTotal, "threshold": r.Threshold,
		}
	case "streak_days":
		return f.PassedStreakDays >= r.Threshold, map[string]any{
			"streak_days": f.PassedStreakDays, "threshold": r.Threshold,
		}
	case "mastery_count":
		got := f.MasteredTotal
		switch r.Subject {
		case "chinese":
			got = f.MasteredChinese
		case "math":
			got = f.MasteredMath
		case "english":
			got = f.MasteredEnglish
		}
		return got >= r.Threshold, map[string]any{
			"mastered": got, "subject": r.Subject, "threshold": r.Threshold,
		}
	default:
		return false, nil
	}
}

// PassedStreak 从今天（或昨天）往回数的连续达标天数。
//
// 今天还没学不算断：孩子白天还没开始，不能让「连续 7 天」在早上清零。
func (s *Service) PassedStreak(ctx context.Context, childID uuid.UUID) (int, error) {
	rows, err := s.repo.PassedDaysDesc(ctx, childID, int(PassStreakThreshold*4))
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	streak := 0
	expected := today
	for i, d := range rows {
		if !d.Valid {
			break
		}
		day := time.Date(d.Time.Year(), d.Time.Month(), d.Time.Day(), 0, 0, 0, 0, today.Location())
		if i == 0 {
			// 首条既不是今天也不是昨天，说明中间断过
			if !day.Equal(today) && !day.Equal(today.AddDate(0, 0, -1)) {
				return 0, nil
			}
			expected = day
		}
		if !day.Equal(expected) {
			break
		}
		streak++
		expected = expected.AddDate(0, 0, -1)
	}
	return streak, nil
}

// Badges 返回徽章目录 + 已获得情况（只读，不授予）。
func (s *Service) Badges(ctx context.Context, childID uuid.UUID) (BadgesView, error) {
	catalog, err := s.repo.ListBadges(ctx)
	if err != nil {
		return BadgesView{}, err
	}
	earned, err := s.repo.ListChildBadges(ctx, childID)
	if err != nil {
		return BadgesView{}, err
	}
	type earnedInfo struct {
		at       time.Time
		progress []byte
	}
	got := make(map[uuid.UUID]earnedInfo, len(earned))
	for _, e := range earned {
		var at time.Time
		if e.EarnedAt.Valid {
			at = e.EarnedAt.Time
		}
		got[e.BadgeID] = earnedInfo{at: at, progress: e.Progress}
	}

	view := BadgesView{ChildID: childID.String(), Total: len(catalog), Items: make([]BadgeItem, 0, len(catalog))}
	for _, b := range catalog {
		item := BadgeItem{
			Code: b.Code, Name: b.Name, Category: b.Category,
			Description: b.Description, Icon: b.Icon,
			Rule: jsonMap(b.Rule),
		}
		if info, ok := got[b.ID]; ok {
			item.Earned = true
			s := info.at.Format(time.RFC3339)
			item.EarnedAt = &s
			item.Progress = jsonMap(info.progress)
			view.Earned++
		}
		view.Items = append(view.Items, item)
	}
	return view, nil
}

// jsonMap 把 jsonb 字节转成 map，解析失败返回空 map（不阻断读接口）。
func jsonMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}
