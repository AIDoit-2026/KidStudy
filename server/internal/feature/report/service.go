package report

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
)

// 参数类错误的统一出口：文案对家长可读，不让 500 冒出来。
var (
	errBadSubject      = apperr.BadRequest("学科必须是 chinese / math / english 之一")
	errNoChild         = apperr.BadRequest("至少要指定一个孩子")
	errTooManyChildren = apperr.BadRequest("一次最多对比 4 个孩子")
	// 对比时混入别人的孩子：与其它接口一致，回 404 而不是 403，不泄露 ID 是否存在
	errChildNotOwned = apperr.NotFound("孩子不存在或不属于当前家长")
)

// Service 承载报表口径、建议规则与措辞。
//
// 文案纪律（§4.9 / §4.10）：只做中性描述，不出现「落后」「跟不上」这类负向标签；
// 多孩对比只并列不排名。所有对家长的结论都从这里的 note / title / detail 出，
// 前端直接展示，不再自己拼话术。
type Service struct {
	repo *Repository
	log  *slog.Logger
}

// NewService 构造报表服务。
func NewService(repo *Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Repo 暴露仓储，供 cmd/worker 直接用（worker 不做 HTTP，不需要 handler 层）。
func (s *Service) Repo() *Repository { return s.repo }

// ---------------------------------------------------------------- 总览

// Overview 家长端首页的汇总卡片。
func (s *Service) Overview(ctx context.Context, childID uuid.UUID) (OverviewView, error) {
	now := time.Now()

	mt, err := s.repo.MasteryTotals(ctx, childID)
	if err != nil {
		return OverviewView{}, err
	}
	totals, err := s.repo.Totals(ctx, childID)
	if err != nil {
		return OverviewView{}, err
	}
	sess, err := s.repo.SessionTotals(ctx, childID)
	if err != nil {
		return OverviewView{}, err
	}
	prog, err := s.repo.SubjectProgress(ctx, childID, now)
	if err != nil {
		return OverviewView{}, err
	}
	streak, err := s.PassedStreak(ctx, childID)
	if err != nil {
		return OverviewView{}, err
	}
	badges, err := s.Badges(ctx, childID)
	if err != nil {
		return OverviewView{}, err
	}
	eff, err := s.repo.EfficiencyTotal(ctx, childID, "")
	if err != nil {
		return OverviewView{}, err
	}
	devAvg, err := s.overallDeviation(ctx, childID, now)
	if err != nil {
		return OverviewView{}, err
	}

	var dueTotal int64
	subjects := make([]SubjectProgress, 0, len(prog))
	for _, p := range prog {
		subjects = append(subjects, SubjectProgress{
			SubjectCode: p.SubjectCode, SubjectName: p.SubjectName,
			Mastered: p.Mastered, PlannedTotal: p.PlannedTotal, Due: p.Due,
		})
		dueTotal += p.Due
	}

	accuracy := 0.0
	if totals.QuestionCount > 0 {
		accuracy = float64(totals.CorrectCount) / float64(totals.QuestionCount)
	}
	return OverviewView{
		ChildID:       childID.String(),
		MasteredTotal: mt.MasteredTotal,
		Subjects:      subjects,
		Totals: OverviewTotals{
			DurationSec:   totals.DurationSec,
			QuestionCount: totals.QuestionCount,
			CorrectCount:  totals.CorrectCount,
			Accuracy:      round4(accuracy),
			StarCount:     totals.StarCount,
			ActiveDays:    totals.ActiveDays,
			SessionCount:  sess.SessionCount,
			AvgAccuracy:   round4(sess.AvgAccuracy),
		},
		DueTotal:           dueTotal,
		StreakDays:         streak,
		BadgesEarned:       int64(badges.Earned),
		BadgesTotal:        int64(badges.Total),
		DeviationDays:      devAvg,
		AttemptsPerMastery: round4(eff.AttemptsPerMastery),
		GeneratedAt:        now,
	}, nil
}

// overallDeviation 有基准线的学科偏差均值；一个都没有则返回 0。
//
// 整孩偏差取均值而不是取最差：报表的目的是看整体节奏，逐科的偏差在 pace 里单独看。
func (s *Service) overallDeviation(ctx context.Context, childID uuid.UUID, now time.Time) (float64, error) {
	prog, err := s.repo.SubjectProgress(ctx, childID, now)
	if err != nil {
		return 0, err
	}
	var sum float64
	var n int
	for _, p := range prog {
		if p.PlannedTotal == 0 {
			continue
		}
		dev, err := s.repo.plannedDeviation(ctx, childID, p.SubjectCode, p.Mastered, now)
		if err != nil {
			return 0, err
		}
		if dev == nil {
			continue
		}
		sum += *dev
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return round4(sum / float64(n)), nil
}

// ---------------------------------------------------------------- 趋势

// Trend 逐日趋势；缺失的日期补零，前端画折线不会断点。
func (s *Service) Trend(ctx context.Context, childID uuid.UUID, days int, subject string) (TrendView, error) {
	days = clampDays(days, 30, daysTrendMax)
	now := time.Now()
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := to.AddDate(0, 0, -(days - 1))

	rows, err := s.repo.DailySeries(ctx, childID, from, to, subject)
	if err != nil {
		return TrendView{}, err
	}
	byDate := make(map[string]TrendPoint, len(rows))
	for _, r := range rows {
		key := fmtDate(r.StatDate)
		// 不指定学科时只看合计行（'' 行），避免把三个学科重复叠加
		if subject == "" && r.SubjectCode != "" {
			continue
		}
		acc := 0.0
		if r.QuestionCount > 0 {
			acc = float64(r.CorrectCount) / float64(r.QuestionCount)
		}
		byDate[key] = TrendPoint{
			Date: key, DurationSec: r.DurationSec, QuestionCount: r.QuestionCount,
			CorrectCount: r.CorrectCount, Accuracy: round4(acc), NewMastered: r.NewMastered,
			StarCount: r.StarCount, PlannedNew: r.PlannedNew, ActualNew: r.ActualNew,
			RepeatCount: r.RepeatCount, CumPlanned: r.CumPlanned, CumActual: r.CumActual,
			DeviationDays: r.DeviationDays, Passed: r.Passed, ParentConfirmed: r.ParentConfirmed,
		}
	}

	points := make([]TrendPoint, 0, days)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if p, ok := byDate[key]; ok {
			points = append(points, p)
			continue
		}
		points = append(points, TrendPoint{Date: key})
	}

	view := TrendView{
		Days: days, From: from.Format("2006-01-02"), To: to.Format("2006-01-02"),
		Points: points,
	}
	if len(rows) == 0 {
		view.Note = "这段时间还没有学习记录。日汇总每天夜里重算，刚学完的数据也会当日先显示。"
	}
	return view, nil
}

// ---------------------------------------------------------------- 学科明细

// Subject 单学科明细：阶段进度、薄弱知识点、薄弱题型、效率。
func (s *Service) Subject(ctx context.Context, childID uuid.UUID, subject string) (SubjectView, error) {
	if !validSubject(subject) {
		return SubjectView{}, errBadSubject
	}
	now := time.Now()
	since := now.AddDate(0, 0, -90)

	stages, err := s.repo.StageProgress(ctx, childID, subject)
	if err != nil {
		return SubjectView{}, err
	}
	weakKPs, err := s.repo.WeakKPs(ctx, childID, since, subject, 10)
	if err != nil {
		return SubjectView{}, err
	}
	weakTypes, err := s.repo.WeakQuestionTypes(ctx, childID, since, 10)
	if err != nil {
		return SubjectView{}, err
	}
	eff, err := s.repo.EfficiencyTotal(ctx, childID, subject)
	if err != nil {
		return SubjectView{}, err
	}
	prog, err := s.repo.SubjectProgress(ctx, childID, now)
	if err != nil {
		return SubjectView{}, err
	}
	last, err := s.repo.LastStudyDate(ctx, childID, subject)
	if err != nil {
		return SubjectView{}, err
	}

	view := SubjectView{
		ChildID:           childID.String(),
		SubjectCode:       subject,
		SubjectName:       subjectNames[subject],
		Stages:            make([]StageProgress, 0, len(stages)),
		WeakKPs:           make([]WeakKP, 0, len(weakKPs)),
		WeakQuestionTypes: make([]WeakQuestionType, 0, len(weakTypes)),
		Efficiency: Efficiency{
			MasteredCount: eff.MasteredCount, AttemptsSum: eff.AttemptsSum,
			AttemptsPerMastery: round4(eff.AttemptsPerMastery),
		},
	}
	for _, p := range prog {
		if p.SubjectCode == subject {
			view.Mastered, view.PlannedTotal, view.Due = p.Mastered, p.PlannedTotal, p.Due
		}
	}
	for _, st := range stages {
		if !st.StageCode.Valid {
			continue
		}
		view.Stages = append(view.Stages, StageProgress{
			StageCode: st.StageCode.String, PlannedTotal: st.PlannedTotal,
			Mastered: st.Mastered, Ratio: ratio(st.Mastered, st.PlannedTotal),
		})
	}
	for _, w := range weakKPs {
		view.WeakKPs = append(view.WeakKPs, WeakKP{
			KPID: w.KpID.String(), Code: w.Code, Name: w.Name,
			StageCode: w.StageCode.String, Attempts: w.Attempts, CorrectCount: w.CorrectCount,
			WrongRate:    round4(wrongRate(w.Attempts, w.CorrectCount)),
			LastAnswered: w.LastAnsweredAt.Time.Format(time.RFC3339),
		})
	}
	for _, w := range weakTypes {
		view.WeakQuestionTypes = append(view.WeakQuestionTypes, WeakQuestionType{
			QuestionType: w.QuestionType, Attempts: w.Attempts, WrongCount: w.WrongCount,
			WrongRate: round4(wrongRate(w.Attempts, w.Attempts-w.WrongCount)),
		})
	}
	if last != nil {
		str := last.Format(time.RFC3339)
		view.LastStudyAt = &str
	}
	return view, nil
}

// ---------------------------------------------------------------- 建议

// Suggestions 规则引擎（§4.7）。每条都可解释、都可关闭。
func (s *Service) Suggestions(ctx context.Context, childID uuid.UUID) (SuggestionsView, error) {
	now := time.Now()
	out := SuggestionsView{ChildID: childID.String(), Suggestions: []Suggestion{}, GeneratedAt: now}

	if sug, err := s.lowAccuracySuggestion(ctx, childID, now); err != nil {
		return out, err
	} else if sug != nil {
		out.Suggestions = append(out.Suggestions, *sug)
	}

	if sug, err := s.idleSubjectSuggestions(ctx, childID, now); err != nil {
		return out, err
	} else {
		out.Suggestions = append(out.Suggestions, sug...)
	}

	due, err := s.repo.DueCount(ctx, childID, "", now)
	if err != nil {
		return out, err
	}
	if due > ReviewBacklogThreshold {
		out.Suggestions = append(out.Suggestions, Suggestion{
			Code: "review_backlog", Severity: "notice",
			Title:   "复习攒得有点多了",
			Detail:  fmt.Sprintf("现在有 %d 个知识点到了复习时间。可以把今天设成复习日，先把这些过一遍再学新的。", due),
			Data:    map[string]any{"due": due, "threshold": ReviewBacklogThreshold},
			Actions: []SuggestionAction{{Type: "review_day", Label: "设为复习日"}},
		})
	}

	streak, err := s.PassedStreak(ctx, childID)
	if err != nil {
		return out, err
	}
	if streak >= PassStreakThreshold {
		out.Suggestions = append(out.Suggestions, Suggestion{
			Code: "raise_new_quota", Severity: "info",
			Title:   "可以试试多学一点",
			Detail:  fmt.Sprintf("已经连续 %d 天完成当日学习。节奏稳的话，可以把每日新学量往上调一档。", streak),
			Data:    map[string]any{"streak_days": streak, "threshold": PassStreakThreshold},
			Actions: []SuggestionAction{{Type: "tune_quota", Label: "调整每日量"}},
		})
	}

	return out, nil
}

// lowAccuracySuggestion 规则 1：某个知识点连续 3 天正确率 <60% → 降一档 + 专项练习。
func (s *Service) lowAccuracySuggestion(ctx context.Context, childID uuid.UUID, now time.Time) (*Suggestion, error) {
	since := now.AddDate(0, 0, -7)
	rows, err := s.repo.KPDailyAccuracy(ctx, childID, since, "")
	if err != nil {
		return nil, err
	}

	// kp_id -> 每日正确率（按日期升序）
	type dayStat struct {
		date  string
		acc   float64
		tries int64
	}
	perKP := map[uuid.UUID][]dayStat{}
	for _, r := range rows {
		perKP[r.KpID] = append(perKP[r.KpID], dayStat{
			date: fmtDate(r.StatDate), acc: ratio(r.CorrectCount, r.Attempts), tries: r.Attempts,
		})
	}

	// 找出「连续 LowAccuracyDays 天低正确率」的最长一段，取最长的那条来提醒。
	var picked uuid.UUID
	var pickedDays []string
	for kp, stats := range perKP {
		sort.Slice(stats, func(i, j int) bool { return stats[i].date < stats[j].date })
		var run []string
		for _, st := range stats {
			if st.acc < PassAccuracy {
				run = append(run, st.date)
				continue
			}
			run = nil
		}
		if len(run) < LowAccuracyDays {
			continue
		}
		if len(run) > len(pickedDays) || (len(run) == len(pickedDays) && kp.String() < picked.String()) {
			picked = kp
			pickedDays = append([]string(nil), run...)
		}
	}
	if picked == uuid.Nil {
		return nil, nil
	}

	name := picked.String()
	if kps, err := s.repo.KPsByIDs(ctx, []uuid.UUID{picked}); err == nil && len(kps) > 0 {
		name = kps[0].Name
	}
	days := len(pickedDays)
	return &Suggestion{
		Code: "low_accuracy_3days", Severity: "notice",
		Title:  fmt.Sprintf("「%s」连着几天没答对", name),
		Detail: fmt.Sprintf("这个知识点最近 %d 天正确率都在 %.0f%% 以下，建议把难度降一档，再配一组专项练习巩固。", days, PassAccuracy*100),
		Data:   map[string]any{"kp_id": picked.String(), "name": name, "days": days},
		Actions: []SuggestionAction{
			{Type: "assign_practice", Label: "生成专项练习"},
			{Type: "lower_difficulty", Label: "降一档"},
		},
	}, nil
}

// idleSubjectSuggestions 规则 2：某科连续 5 天没学 → 均衡提醒。
//
// 只有在「孩子确实学过这门课」的前提下才提醒：一科从没开始过不属于不均衡。
func (s *Service) idleSubjectSuggestions(ctx context.Context, childID uuid.UUID, now time.Time) ([]Suggestion, error) {
	var out []Suggestion
	threshold := now.AddDate(0, 0, -IdleDaysThreshold)
	for _, subject := range subjectOrder {
		first, err := s.repo.DailyMastered(ctx, childID, subject)
		if err != nil {
			return nil, err
		}
		if len(first) == 0 {
			continue // 还没开始过这门课
		}
		last, err := s.repo.LastStudyDate(ctx, childID, subject)
		if err != nil {
			return nil, err
		}
		if last == nil || last.Before(threshold) {
			days := IdleDaysThreshold
			if last != nil {
				days = int(now.Sub(*last).Hours() / 24)
			}
			out = append(out, Suggestion{
				Code: "subject_idle", Severity: "info", Subject: subject,
				Title:   fmt.Sprintf("%s有几天没碰了", subjectNames[subject]),
				Detail:  fmt.Sprintf("上次学%s是在 %d 天前。每天三科都碰一点，比一科连着猛学更容易记住。", subjectNames[subject], days),
				Data:    map[string]any{"subject": subject, "idle_days": days, "threshold": IdleDaysThreshold},
				Actions: []SuggestionAction{{Type: "balance_subjects", Label: "安排一点"}},
			})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- 节奏与效率

// Pace 标准基准线 vs 实际（§4.9）。
func (s *Service) Pace(ctx context.Context, childID uuid.UUID, days int, subject string) (PaceView, error) {
	if subject != "" && !validSubject(subject) {
		return PaceView{}, errBadSubject
	}
	days = clampDays(days, 90, daysPaceMax)
	now := time.Now()
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := to.AddDate(0, 0, -(days - 1))

	rows, err := s.repo.PaceSeries(ctx, childID, from, to, subject)
	if err != nil {
		return PaceView{}, err
	}
	series := make([]PacePoint, 0, len(rows))
	for _, r := range rows {
		series = append(series, PacePoint{
			Date: fmtDate(r.StatDate), PlannedNew: r.PlannedNew, ActualNew: r.ActualNew,
			RepeatCount: r.RepeatCount, CumPlanned: r.CumPlanned, CumActual: r.CumActual,
			DeviationDays: r.DeviationDays,
		})
	}

	weekly, err := s.repo.WeeklyEfficiency(ctx, childID, now.AddDate(0, 0, -7*8), subject)
	if err != nil {
		return PaceView{}, err
	}
	weeks := make([]WeeklyEfficiency, 0, len(weekly))
	for _, w := range weekly {
		weeks = append(weeks, WeeklyEfficiency{
			WeekStart: fmtDate(w.WeekStart), MasteredCount: w.MasteredCount,
			AttemptsSum: w.AttemptsSum, AttemptsPerMastery: round4(w.AttemptsPerMastery),
		})
	}

	eff, err := s.repo.EfficiencyTotal(ctx, childID, subject)
	if err != nil {
		return PaceView{}, err
	}

	view := PaceView{
		ChildID: childID.String(), SubjectCode: subject, Days: days,
		From: from.Format("2006-01-02"), To: to.Format("2006-01-02"),
		Series: series, Weekly: weeks,
		Efficiency: Efficiency{
			MasteredCount: eff.MasteredCount, AttemptsSum: eff.AttemptsSum,
			AttemptsPerMastery: round4(eff.AttemptsPerMastery),
		},
		Note: "标准节奏只是参照线，不设闸门：提前学、连着学、整天只复习都不影响这条线，" +
			"它的作用是把「实际速度」和「标准速度」摆在一起看。重复次数多不等于学得差，" +
			"要跟正确率一起读。",
	}
	if len(series) > 0 {
		view.Current = series[len(series)-1]
	}
	return view, nil
}

// ---------------------------------------------------------------- 成长树

// Growth 成长树：分学科 × 分阶段的掌握进度。
func (s *Service) Growth(ctx context.Context, childID uuid.UUID) (GrowthView, error) {
	view := GrowthView{ChildID: childID.String(), Subjects: make([]GrowthSubject, 0, len(subjectOrder))}
	for _, subject := range subjectOrder {
		stages, err := s.repo.StageProgress(ctx, childID, subject)
		if err != nil {
			return view, err
		}
		gs := GrowthSubject{
			SubjectCode: subject, SubjectName: subjectNames[subject],
			Stages: make([]StageProgress, 0, len(stages)),
		}
		for _, st := range stages {
			if !st.StageCode.Valid {
				continue
			}
			gs.Stages = append(gs.Stages, StageProgress{
				StageCode: st.StageCode.String, PlannedTotal: st.PlannedTotal,
				Mastered: st.Mastered, Ratio: ratio(st.Mastered, st.PlannedTotal),
			})
			gs.Mastered += st.Mastered
			gs.PlannedTotal += st.PlannedTotal
		}
		view.Subjects = append(view.Subjects, gs)
	}
	return view, nil
}

// ---------------------------------------------------------------- 导出

// ExportCSV 导出逐日明细（CSV 前端直出，PDF 留 M5 走打印模块）。
func (s *Service) ExportCSV(ctx context.Context, childID uuid.UUID, days int, subject string) ([]byte, error) {
	trend, err := s.Trend(ctx, childID, days, subject)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	// 带 BOM：Excel 打开中文表头才不会乱码
	buf.WriteString("\xEF\xBB\xBF")
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{
		"日期", "学习时长(秒)", "题量", "正确数", "正确率", "新掌握", "星星",
		"计划新学", "实际新学", "重复练习", "累计计划", "累计实际", "偏差(天)", "是否达标",
	})
	for _, p := range trend.Points {
		passed := "否"
		if p.Passed {
			passed = "是"
		}
		_ = w.Write([]string{
			p.Date,
			strconv.Itoa(int(p.DurationSec)),
			strconv.Itoa(int(p.QuestionCount)),
			strconv.Itoa(int(p.CorrectCount)),
			fmt.Sprintf("%.2f", p.Accuracy),
			strconv.Itoa(int(p.NewMastered)),
			strconv.Itoa(int(p.StarCount)),
			strconv.Itoa(int(p.PlannedNew)),
			strconv.Itoa(int(p.ActualNew)),
			strconv.Itoa(int(p.RepeatCount)),
			strconv.Itoa(int(p.CumPlanned)),
			strconv.Itoa(int(p.CumActual)),
			fmt.Sprintf("%.2f", p.DeviationDays),
			passed,
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------- 助手

func validSubject(subject string) bool {
	switch subject {
	case "chinese", "math", "english":
		return true
	default:
		return false
	}
}

func clampDays(days, def, max int) int {
	if days <= 0 {
		return def
	}
	if days > max {
		return max
	}
	return days
}

func ratio(a, b int64) float64 {
	if b <= 0 {
		return 0
	}
	return round4(float64(a) / float64(b))
}

func wrongRate(attempts, correct int64) float64 {
	if attempts <= 0 {
		return 0
	}
	return float64(attempts-correct) / float64(attempts)
}

func round4(v float64) float64 {
	return float64(int64(v*10000+0.5)) / 10000
}

// ParseSubjectList 把 "a,b" 形式的 childIds 解析出来（handler 用）。
func ParseUUIDList(raw string) ([]uuid.UUID, error) {
	parts := strings.Split(raw, ",")
	out := make([]uuid.UUID, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := uuid.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("非法的 ID %q", p)
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("至少需要一个 ID")
	}
	return out, nil
}
