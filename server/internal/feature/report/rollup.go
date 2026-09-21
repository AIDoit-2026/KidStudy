package report

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"kidstudy/internal/dbgen"
)

// RollupOptions 日汇总的入参。
type RollupOptions struct {
	ChildIDs []uuid.UUID // 空 = 全部孩子
	From     time.Time
	To       time.Time
}

// RollupReport 汇总结果，供 worker 打印与冒烟断言。
type RollupReport struct {
	Children int `json:"children"`
	Days     int `json:"days"`
	Rows     int `json:"rows"`
	Retries  int `json:"retries"`
	Failures int `json:"failures"`
}

// rollupMaxAttempts 单天重试次数：日汇总只读源表、纯幂等，重试代价极低。
const rollupMaxAttempts = 3

// Rollup 重算 [From, To] 每一天的日汇总。
//
// 为什么 worker 要有权覆盖 daily_stats 的节奏列：M3 在会话结束时只做「增量」更新，
// 那是为了让家长当场看到数字；而计划量、累计偏差、当日是否达标这些量随时间推进
// 每天都会变（哪怕孩子当天没学，偏差也会变大），只能由按天重算的 worker 负责。
// 这里对同一 (child, day, subject) 是「覆盖式重算」，所以可以反复跑。
func (s *Service) Rollup(ctx context.Context, opts RollupOptions) (RollupReport, error) {
	var rep RollupReport

	children, err := s.childrenFor(ctx, opts.ChildIDs)
	if err != nil {
		return rep, err
	}
	days := enumerateDays(opts.From, opts.To)
	rep.Children = len(children)
	rep.Days = len(days)

	settingsCache := map[uuid.UUID]bool{} // parent_id -> require_parent_confirm

	for _, child := range children {
		requireConfirm, ok := settingsCache[child.ParentID]
		if !ok {
			requireConfirm = s.requireParentConfirm(ctx, child.ParentID)
			settingsCache[child.ParentID] = requireConfirm
		}
		for _, day := range days {
			attempts := 0
			var derr error
			for attempts < rollupMaxAttempts {
				attempts++
				derr = s.rollupDay(ctx, child.ID, day, requireConfirm, &rep)
				if derr == nil {
					break
				}
			}
			if derr != nil {
				rep.Failures++
				s.log.Error("日汇总失败，跳过该天",
					"child_id", child.ID, "date", day.Format("2006-01-02"),
					"attempts", attempts, "error", derr)
				continue
			}
			if attempts > 1 {
				rep.Retries += attempts - 1
			}
		}
	}
	return rep, nil
}

// rollupDay 重算「一个孩子一天」的全部分学科行与合计行。
func (s *Service) rollupDay(ctx context.Context, childID uuid.UUID, day time.Time, requireConfirm bool, rep *RollupReport) error {
	rq := s.repo.RollupQueries()
	d := date(day)

	var (
		sumPlanned, sumActual, sumCumPlanned, sumCumActual, sumRepeat int64
		anyPassed                                                     bool
		plannedSubjects                                               int
		devTotal                                                      float64
	)

	for _, subject := range subjectOrder {
		planned, err := rq.PlannedForDate(ctx, childID, subject, d)
		if err != nil {
			return fmt.Errorf("取计划量失败: %w", err)
		}
		cumPlanned, err := rq.CumPlanned(ctx, childID, subject, d)
		if err != nil {
			return fmt.Errorf("取累计计划失败: %w", err)
		}
		actual, err := rq.DailyMastered(ctx, childID, subject, d)
		if err != nil {
			return fmt.Errorf("取当日新掌握失败: %w", err)
		}
		cumActual, err := rq.CumMastered(ctx, childID, subject, d)
		if err != nil {
			return fmt.Errorf("取累计掌握失败: %w", err)
		}
		repeat, err := rq.RepeatCount(ctx, childID, subject, d)
		if err != nil {
			return fmt.Errorf("取重复练习量失败: %w", err)
		}
		answers, err := rq.DailyAnswers(ctx, childID, subject, d)
		if err != nil {
			return fmt.Errorf("取当日作答失败: %w", err)
		}

		dev, err := s.deviationDays(ctx, childID, subject, cumActual, day)
		if err != nil {
			return fmt.Errorf("算偏差失败: %w", err)
		}
		passed := answers.Attempts > 0 && float64(answers.CorrectCount)/float64(answers.Attempts) >= PassAccuracy
		if passed {
			anyPassed = true
		}
		if cumPlanned > 0 {
			plannedSubjects++
			devTotal += dev
		}

		if err := rq.Write(ctx, dbgen.UpsertDailyRollupParams{
			ChildID:         childID,
			StatDate:        d,
			SubjectCode:     subject,
			NewMastered:     int32(actual),
			PlannedNew:      int32(planned),
			ActualNew:       int32(actual),
			RepeatCount:     int32(repeat),
			CumPlanned:      int32(cumPlanned),
			CumActual:       int32(cumActual),
			DeviationDays:   dev,
			Passed:          passed,
			ParentConfirmed: false,
		}); err != nil {
			return fmt.Errorf("写分学科汇总失败: %w", err)
		}
		rep.Rows++

		sumPlanned += planned
		sumActual += actual
		sumCumPlanned += cumPlanned
		sumCumActual += cumActual
		sumRepeat += repeat
	}

	// 合计行（subject_code = ''）：M4 起约定它为「当天全天」，passed 才代表当日达标。
	parentConfirmed, err := rq.ParentConfirmed(ctx, childID, d)
	if err != nil {
		return fmt.Errorf("取家长确认失败: %w", err)
	}
	// require_parent_confirm 开启时，达标必须由家长确认点亮（§4.11）
	dayPassed := anyPassed && (!requireConfirm || parentConfirmed)

	var devAvg float64
	if plannedSubjects > 0 {
		devAvg = devTotal / float64(plannedSubjects)
	}

	if err := rq.Write(ctx, dbgen.UpsertDailyRollupParams{
		ChildID:         childID,
		StatDate:        d,
		SubjectCode:     "",
		NewMastered:     int32(sumActual),
		PlannedNew:      int32(sumPlanned),
		ActualNew:       int32(sumActual),
		RepeatCount:     int32(sumRepeat),
		CumPlanned:      int32(sumCumPlanned),
		CumActual:       int32(sumCumActual),
		DeviationDays:   devAvg,
		Passed:          dayPassed,
		ParentConfirmed: parentConfirmed,
	}); err != nil {
		return fmt.Errorf("写合计汇总失败: %w", err)
	}
	rep.Rows++
	return nil
}

// deviationDays 当前进度落在计划顺序上的那一天，与当日的差（正=落后，负=超前）。
//
// 没铺基准线的学科返回 0：没有参照线就没有偏差可言，硬给个数反而是误导。
func (s *Service) deviationDays(ctx context.Context, childID uuid.UUID, subject string, cumActual int64, day time.Time) (float64, error) {
	planned, err := s.repo.PlannedDateAtProgress(ctx, childID, subject, cumActual)
	if err != nil {
		return 0, err
	}
	if planned == nil {
		return 0, nil
	}
	return day.Sub(*planned).Hours() / 24, nil
}

// childrenFor 取要处理的孩子：显式给出就用它，否则全量。
func (s *Service) childrenFor(ctx context.Context, ids []uuid.UUID) ([]dbgen.ListChildIDsRow, error) {
	all, err := s.repo.ListChildren(ctx)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return all, nil
	}
	want := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	out := make([]dbgen.ListChildIDsRow, 0, len(ids))
	for _, c := range all {
		if _, ok := want[c.ID]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// requireParentConfirm 读家长设置；查不到用默认（关闭）。
func (s *Service) requireParentConfirm(ctx context.Context, parentID uuid.UUID) bool {
	st, err := s.repo.GetParentSettings(ctx, parentID)
	if err != nil {
		s.log.Warn("读家长设置失败，按默认值处理", "parent_id", parentID, "error", err)
		return false
	}
	return st != nil && st.RequireParentConfirm
}

// enumerateDays 展开 [from, to] 的每一天（按 from 的时区口径，保留 0 点）。
func enumerateDays(from, to time.Time) []time.Time {
	if to.Before(from) {
		return nil
	}
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
	var out []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}
