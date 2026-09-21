package report

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"kidstudy/internal/dbgen"
)

// ErrNotFound 由 service 转成 404。
var ErrNotFound = errors.New("记录不存在")

// Repository 只做 SQL 读写，口径与措辞都在 service。
type Repository struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewRepository 用连接池构造。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: dbgen.New(pool)}
}

// Pool 暴露连接池：rollup 需要按天多轮小查询，逐个包一层没意义。
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

// Queries 暴露生成的查询集，供 rollup / badges 直接使用。
func (r *Repository) Queries() *dbgen.Queries { return r.q }

// ---------------------------------------------------------------- 总览

// MasteryTotals 累计掌握量（分学科）。
func (r *Repository) MasteryTotals(ctx context.Context, childID uuid.UUID) (dbgen.ReportMasteryTotalsRow, error) {
	return r.q.ReportMasteryTotals(ctx, childID)
}

// Totals 累计时长/题量/星星/活跃天。
func (r *Repository) Totals(ctx context.Context, childID uuid.UUID) (dbgen.ReportTotalsRow, error) {
	return r.q.ReportTotals(ctx, childID)
}

// SessionTotals 会话数与平均正确率。
func (r *Repository) SessionTotals(ctx context.Context, childID uuid.UUID) (dbgen.ReportSessionTotalsRow, error) {
	return r.q.ReportSessionTotals(ctx, childID)
}

// DueCount 待复习量。
func (r *Repository) DueCount(ctx context.Context, childID uuid.UUID, subject string, now time.Time) (int64, error) {
	return r.q.ReportDueCount(ctx, dbgen.ReportDueCountParams{
		ChildID: childID, SubjectCode: subject, NowTs: ts(now),
	})
}

// SubjectProgress 分学科进度。
func (r *Repository) SubjectProgress(ctx context.Context, childID uuid.UUID, now time.Time) ([]dbgen.ReportSubjectProgressRow, error) {
	return r.q.ReportSubjectProgress(ctx, dbgen.ReportSubjectProgressParams{ChildID: childID, NowTs: ts(now)})
}

// StageProgress 分阶段进度。
func (r *Repository) StageProgress(ctx context.Context, childID uuid.UUID, subject string) ([]dbgen.ReportStageProgressRow, error) {
	return r.q.ReportStageProgress(ctx, dbgen.ReportStageProgressParams{ChildID: childID, SubjectCode: subject})
}

// ---------------------------------------------------------------- 趋势

// DailySeries 逐日序列（含 ” 合计行与分学科行）。
func (r *Repository) DailySeries(ctx context.Context, childID uuid.UUID, from, to time.Time, subject string) ([]dbgen.ReportDailySeriesRow, error) {
	return r.q.ReportDailySeries(ctx, dbgen.ReportDailySeriesParams{
		ChildID: childID, FromDate: date(from), ToDate: date(to), SubjectCode: subject,
	})
}

// ---------------------------------------------------------------- 薄弱项

// WeakKPs 薄弱知识点（掌握度未达 3 且答错过），按错误数排序。
func (r *Repository) WeakKPs(ctx context.Context, childID uuid.UUID, since time.Time, subject string, lim int) ([]dbgen.ReportWeakKPsRow, error) {
	return r.q.ReportWeakKPs(ctx, dbgen.ReportWeakKPsParams{
		ChildID: childID, SinceTs: ts(since), SubjectCode: subject, Lim: int32(lim),
	})
}

// KPDailyAccuracy 逐日逐知识点正确率（建议规则 1 用）。
func (r *Repository) KPDailyAccuracy(ctx context.Context, childID uuid.UUID, since time.Time, subject string) ([]dbgen.ReportKPDailyAccuracyRow, error) {
	return r.q.ReportKPDailyAccuracy(ctx, dbgen.ReportKPDailyAccuracyParams{
		ChildID: childID, SinceTs: ts(since), SubjectCode: subject,
	})
}

// WeakQuestionTypes 薄弱题型。
func (r *Repository) WeakQuestionTypes(ctx context.Context, childID uuid.UUID, since time.Time, lim int) ([]dbgen.ReportWeakQuestionTypesRow, error) {
	return r.q.ReportWeakQuestionTypes(ctx, dbgen.ReportWeakQuestionTypesParams{
		ChildID: childID, SinceTs: ts(since), Lim: int32(lim),
	})
}

// LastStudyDate 某学科最近一次作答时间；从未学过返回 nil。
func (r *Repository) LastStudyDate(ctx context.Context, childID uuid.UUID, subject string) (*time.Time, error) {
	tsv, err := r.q.ReportLastStudyDate(ctx, dbgen.ReportLastStudyDateParams{ChildID: childID, SubjectCode: subject})
	if err != nil {
		return nil, err
	}
	if !tsv.Valid || tsv.Time.Unix() <= 0 {
		return nil, nil
	}
	t := tsv.Time
	return &t, nil
}

// ---------------------------------------------------------------- 节奏与效率

// PaceSeries 逐日计划 vs 实际（分学科）。
func (r *Repository) PaceSeries(ctx context.Context, childID uuid.UUID, from, to time.Time, subject string) ([]dbgen.ReportPaceSeriesRow, error) {
	return r.q.ReportPaceSeries(ctx, dbgen.ReportPaceSeriesParams{
		ChildID: childID, FromDate: date(from), ToDate: date(to), SubjectCode: subject,
	})
}

// WeeklyEfficiency 按自然周的效率。
func (r *Repository) WeeklyEfficiency(ctx context.Context, childID uuid.UUID, since time.Time, subject string) ([]dbgen.ReportWeeklyEfficiencyRow, error) {
	return r.q.ReportWeeklyEfficiency(ctx, dbgen.ReportWeeklyEfficiencyParams{
		ChildID: childID, SinceTs: ts(since), SubjectCode: subject,
	})
}

// EfficiencyTotal 掌握一个知识点的平均作答数。
func (r *Repository) EfficiencyTotal(ctx context.Context, childID uuid.UUID, subject string) (dbgen.ReportEfficiencyTotalRow, error) {
	return r.q.ReportEfficiencyTotal(ctx, dbgen.ReportEfficiencyTotalParams{ChildID: childID, SubjectCode: subject})
}

// PlannedDateAtProgress 取计划顺序上第 progress 个知识点的计划日期；无计划返回 nil。
// progress <= 0 时取第一个（用于「还没开始，落后了多少」）。
func (r *Repository) PlannedDateAtProgress(ctx context.Context, childID uuid.UUID, subject string, progress int64) (*time.Time, error) {
	n := progress
	if n < 1 {
		n = 1
	}
	d, err := r.q.ReportPlannedDateAtProgress(ctx, dbgen.ReportPlannedDateAtProgressParams{
		ChildID: childID, SubjectCode: subject, Progress: int32(n),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !d.Valid {
		return nil, nil
	}
	t := d.Time
	return &t, nil
}

// plannedDeviation 计划顺序上第 progress 个知识点的计划日与今天的差（天）；无计划返回 nil。
func (r *Repository) plannedDeviation(ctx context.Context, childID uuid.UUID, subject string, progress int64, now time.Time) (*float64, error) {
	pd, err := r.PlannedDateAtProgress(ctx, childID, subject, progress)
	if err != nil || pd == nil {
		return nil, err
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	v := day.Sub(*pd).Hours() / 24
	return &v, nil
}

// ---------------------------------------------------------------- 多孩对比

// ChildrenInfo 取家长名下指定孩子的信息（顺带完成归属过滤）。
func (r *Repository) ChildrenInfo(ctx context.Context, parentID uuid.UUID, childIDs []uuid.UUID) ([]dbgen.ReportChildrenInfoRow, error) {
	return r.q.ReportChildrenInfo(ctx, dbgen.ReportChildrenInfoParams{ParentID: parentID, ChildIds: childIDs})
}

// DailyMastered 逐日新掌握数。
func (r *Repository) DailyMastered(ctx context.Context, childID uuid.UUID, subject string) ([]dbgen.ReportDailyMasteredRow, error) {
	return r.q.ReportDailyMastered(ctx, dbgen.ReportDailyMasteredParams{ChildID: childID, SubjectCode: subject})
}

// DailyAnswers 逐日作答与正确数。
func (r *Repository) DailyAnswers(ctx context.Context, childID uuid.UUID, subject string) ([]dbgen.ReportDailyAnswersRow, error) {
	return r.q.ReportDailyAnswers(ctx, dbgen.ReportDailyAnswersParams{ChildID: childID, SubjectCode: subject})
}

// DailyDuration 逐日学习时长。
func (r *Repository) DailyDuration(ctx context.Context, childID uuid.UUID) ([]dbgen.ReportDailyDurationRow, error) {
	return r.q.ReportDailyDuration(ctx, childID)
}

// KPsByIDs 取知识点基本信息（建议文案要显示名字）。
func (r *Repository) KPsByIDs(ctx context.Context, ids []uuid.UUID) ([]dbgen.ListKPsByIDsRow, error) {
	return r.q.ListKPsByIDs(ctx, ids)
}

// FirstStudyDate 第一个学习日；从未学过返回零值。
func (r *Repository) FirstStudyDate(ctx context.Context, childID uuid.UUID) (pgtype.Date, error) {
	return r.q.ReportFirstStudyDate(ctx, childID)
}

// ---------------------------------------------------------------- 成就

// ListBadges 徽章目录。
func (r *Repository) ListBadges(ctx context.Context) ([]dbgen.ListBadgesRow, error) {
	return r.q.ListBadges(ctx)
}

// ListChildBadges 孩子已获得的徽章。
func (r *Repository) ListChildBadges(ctx context.Context, childID uuid.UUID) ([]dbgen.ListChildBadgesRow, error) {
	return r.q.ListChildBadges(ctx, childID)
}

// AwardBadge 授予徽章；已授予过返回 false（幂等）。
func (r *Repository) AwardBadge(ctx context.Context, childID, badgeID uuid.UUID, progress []byte) (bool, error) {
	_, err := r.q.InsertChildBadge(ctx, dbgen.InsertChildBadgeParams{
		ChildID: childID, BadgeID: badgeID, Progress: progress,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil // ON CONFLICT DO NOTHING 没返回行 = 早就有了
		}
		return false, err
	}
	return true, nil
}

// BadgeSessionStats 会话类素材。
func (r *Repository) BadgeSessionStats(ctx context.Context, childID uuid.UUID) (dbgen.BadgeSessionStatsRow, error) {
	return r.q.BadgeSessionStats(ctx, childID)
}

// BadgeMasteryStats 掌握类素材。
func (r *Repository) BadgeMasteryStats(ctx context.Context, childID uuid.UUID) (dbgen.BadgeMasteryStatsRow, error) {
	return r.q.BadgeMasteryStats(ctx, childID)
}

// PassedDaysDesc 连续达标天（从今天往回，遇到未达标停）。
func (r *Repository) PassedDaysDesc(ctx context.Context, childID uuid.UUID, lim int) ([]pgtype.Date, error) {
	return r.q.ListPassedDaysDesc(ctx, dbgen.ListPassedDaysDescParams{ChildID: childID, Lim: int32(lim)})
}

// ---------------------------------------------------------------- 日汇总

// ListChildren 全量孩子（含 parent_id，rollup 要用它读家长设置）。
func (r *Repository) ListChildren(ctx context.Context) ([]dbgen.ListChildIDsRow, error) {
	return r.q.ListChildIDs(ctx)
}

// Rollup 相关的日汇总查询打包成一个结构，省得 rollup.go 里到处传 repo。
type rollupQueries struct{ q *dbgen.Queries }

// RollupQueries 返回日汇总用的查询集。
func (r *Repository) RollupQueries() rollupQueries { return rollupQueries{q: r.q} }

// PlannedForDate 当日计划新学量。
func (r rollupQueries) PlannedForDate(ctx context.Context, childID uuid.UUID, subject string, day pgtype.Date) (int64, error) {
	return r.q.RollupPlannedForDate(ctx, dbgen.RollupPlannedForDateParams{
		ChildID: childID, SubjectCode: subject, StatDate: day,
	})
}

// CumPlanned 累计计划量（到当日为止）。
func (r rollupQueries) CumPlanned(ctx context.Context, childID uuid.UUID, subject string, day pgtype.Date) (int64, error) {
	return r.q.RollupCumPlanned(ctx, dbgen.RollupCumPlannedParams{
		ChildID: childID, SubjectCode: subject, StatDate: day,
	})
}

// DailyMastered 当日新掌握数。
func (r rollupQueries) DailyMastered(ctx context.Context, childID uuid.UUID, subject string, day pgtype.Date) (int64, error) {
	return r.q.RollupDailyMastered(ctx, dbgen.RollupDailyMasteredParams{
		ChildID: childID, SubjectCode: subject, StatDate: day,
	})
}

// CumMastered 累计掌握量（到当日为止）。
func (r rollupQueries) CumMastered(ctx context.Context, childID uuid.UUID, subject string, day pgtype.Date) (int64, error) {
	return r.q.RollupCumMastered(ctx, dbgen.RollupCumMasteredParams{
		ChildID: childID, SubjectCode: subject, StatDate: day,
	})
}

// RepeatCount 当日对已学内容的重复练习量。
func (r rollupQueries) RepeatCount(ctx context.Context, childID uuid.UUID, subject string, day pgtype.Date) (int64, error) {
	return r.q.RollupRepeatCount(ctx, dbgen.RollupRepeatCountParams{
		ChildID: childID, SubjectCode: subject, StatDate: day,
	})
}

// DailyAnswers 当日作答与正确数。
func (r rollupQueries) DailyAnswers(ctx context.Context, childID uuid.UUID, subject string, day pgtype.Date) (dbgen.RollupDailyAnswersRow, error) {
	return r.q.RollupDailyAnswers(ctx, dbgen.RollupDailyAnswersParams{
		ChildID: childID, SubjectCode: subject, StatDate: day,
	})
}

// ParentConfirmed 当日是否有家长确认。
func (r rollupQueries) ParentConfirmed(ctx context.Context, childID uuid.UUID, day pgtype.Date) (bool, error) {
	return r.q.RollupParentConfirmed(ctx, dbgen.RollupParentConfirmedParams{
		ChildID: childID, StatDate: day,
	})
}

// Write 写回汇总行。
func (r rollupQueries) Write(ctx context.Context, p dbgen.UpsertDailyRollupParams) error {
	return r.q.UpsertDailyRollup(ctx, p)
}

// ---------------------------------------------------------------- 设置

// GetParentSettings 取家长设置；没有记录返回 nil。
func (r *Repository) GetParentSettings(ctx context.Context, parentID uuid.UUID) (*dbgen.GetParentSettingsRow, error) {
	row, err := r.q.GetParentSettings(ctx, parentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// UpsertParentSettings 写家长设置。
func (r *Repository) UpsertParentSettings(ctx context.Context, p dbgen.UpsertParentSettingsParams) (dbgen.UpsertParentSettingsRow, error) {
	return r.q.UpsertParentSettings(ctx, p)
}

// ---------------------------------------------------------------- 转换助手

// ts 把 time.Time 转成 pgtype.Timestamptz。
func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()} }

// date 只保留日期部分（按调用方时区）。
func date(t time.Time) pgtype.Date {
	y, m, d := t.Date()
	return pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, t.Location()), Valid: true}
}

// fmtDate 输出 YYYY-MM-DD。
func fmtDate(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("2006-01-02")
}

// fmtDatePtr 日期指针转字符串，无效返回 nil。
func fmtDatePtr(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	s := d.Time.Format("2006-01-02")
	return &s
}
