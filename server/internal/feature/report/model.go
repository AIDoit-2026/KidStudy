// Package report 是家长报表、节奏偏差统计、成就评价与日汇总 worker 的落点。
//
// 分层：handler 只解析请求与写响应；service 负责口径与措辞；repository 只管 SQL；
// rollup.go / badges.go 是两条独立职责（日汇总计算、成就评测），都由 service 调用。
//
// 为什么报表直接 JOIN knowledge_points：读报表天然是多表聚合，走 content.Service 会拆成
// N 次查询并自带 N+1；这条路按「读模型」处理，不再假装它是领域聚合。
package report

import "time"

// 学科顺序固定，避免前端拿到按字典序排的结果。
var subjectOrder = []string{"chinese", "math", "english"}

// subjectNames 与 subjects 表种子一致；报表文案要用中文名。
var subjectNames = map[string]string{
	"chinese": "语文",
	"math":    "数学",
	"english": "英语",
}

// DefaultSubject 是「不指定学科」时的口径：按 chinese 之外的整体。
const (
	// daysTrendMax / daysPaceMax 限制查询窗口，防止有人用 days=100000 拉全表。
	daysTrendMax = 365
	daysPaceMax  = 365

	// PassAccuracy 当日达标所需的最低客观正确率（§4.11 默认 60%）。
	PassAccuracy = 0.60

	// ReviewBacklogThreshold 复习积压超过它就建议设复习日（§4.7 规则 3）。
	ReviewBacklogThreshold = 100

	// IdleDaysThreshold 某科连续这么多天没学就提醒均衡（§4.7 规则 2）。
	IdleDaysThreshold = 5

	// LowAccuracyDays 连续这么天低正确率就建议降档（§4.7 规则 1）。
	LowAccuracyDays = 3

	// PassStreakThreshold 连续达标这么多天就建议上调新学量（§4.7 规则 4）。
	PassStreakThreshold = 7
)

// ---------------------------------------------------------------- 总览

// SubjectProgress 分学科进度。
type SubjectProgress struct {
	SubjectCode  string `json:"subject_code"`
	SubjectName  string `json:"subject_name"`
	Mastered     int64  `json:"mastered"`
	PlannedTotal int64  `json:"planned_total"`
	Due          int64  `json:"due"`
}

// OverviewTotals 累计量。
type OverviewTotals struct {
	DurationSec   int64   `json:"duration_sec"`
	QuestionCount int64   `json:"question_count"`
	CorrectCount  int64   `json:"correct_count"`
	Accuracy      float64 `json:"accuracy"`
	StarCount     int64   `json:"star_count"`
	ActiveDays    int64   `json:"active_days"`
	SessionCount  int64   `json:"session_count"`
	AvgAccuracy   float64 `json:"avg_accuracy"`
}

// OverviewView 总览。
type OverviewView struct {
	ChildID            string            `json:"child_id"`
	MasteredTotal      int64             `json:"mastered_total"`
	Subjects           []SubjectProgress `json:"subjects"`
	Totals             OverviewTotals    `json:"totals"`
	DueTotal           int64             `json:"due_total"`
	StreakDays         int               `json:"streak_days"`
	BadgesEarned       int64             `json:"badges_earned"`
	BadgesTotal        int64             `json:"badges_total"`
	DeviationDays      float64           `json:"deviation_days"`
	AttemptsPerMastery float64           `json:"attempts_per_mastery"`
	GeneratedAt        time.Time         `json:"generated_at"`
}

// ---------------------------------------------------------------- 趋势

// TrendPoint 一天的量。
type TrendPoint struct {
	Date            string  `json:"date"`
	DurationSec     int32   `json:"duration_sec"`
	QuestionCount   int32   `json:"question_count"`
	CorrectCount    int32   `json:"correct_count"`
	Accuracy        float64 `json:"accuracy"`
	NewMastered     int32   `json:"new_mastered"`
	StarCount       int32   `json:"star_count"`
	PlannedNew      int32   `json:"planned_new"`
	ActualNew       int32   `json:"actual_new"`
	RepeatCount     int32   `json:"repeat_count"`
	CumPlanned      int32   `json:"cum_planned"`
	CumActual       int32   `json:"cum_actual"`
	DeviationDays   float64 `json:"deviation_days"`
	Passed          bool    `json:"passed"`
	ParentConfirmed bool    `json:"parent_confirmed"`
}

// TrendView 趋势。
type TrendView struct {
	Days   int          `json:"days"`
	From   string       `json:"from"`
	To     string       `json:"to"`
	Points []TrendPoint `json:"points"`
	Note   string       `json:"note,omitempty"`
}

// ---------------------------------------------------------------- 学科明细

// StageProgress 某学科某阶段的进度（成长树节点）。
type StageProgress struct {
	StageCode    string  `json:"stage_code"`
	PlannedTotal int64   `json:"planned_total"`
	Mastered     int64   `json:"mastered"`
	Ratio        float64 `json:"ratio"`
}

// WeakKP 薄弱知识点。
type WeakKP struct {
	KPID         string  `json:"kp_id"`
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	StageCode    string  `json:"stage_code,omitempty"`
	Attempts     int64   `json:"attempts"`
	CorrectCount int64   `json:"correct_count"`
	WrongRate    float64 `json:"wrong_rate"`
	LastAnswered string  `json:"last_answered_at"`
}

// WeakQuestionType 薄弱题型。
type WeakQuestionType struct {
	QuestionType string  `json:"question_type"`
	Attempts     int64   `json:"attempts"`
	WrongCount   int64   `json:"wrong_count"`
	WrongRate    float64 `json:"wrong_rate"`
}

// Efficiency 学习效率（§4.9）。
type Efficiency struct {
	MasteredCount      int64   `json:"mastered_count"`
	AttemptsSum        int64   `json:"attempts_sum"`
	AttemptsPerMastery float64 `json:"attempts_per_mastery"`
}

// SubjectView 单学科明细。
type SubjectView struct {
	ChildID           string             `json:"child_id"`
	SubjectCode       string             `json:"subject_code"`
	SubjectName       string             `json:"subject_name"`
	Mastered          int64              `json:"mastered"`
	PlannedTotal      int64              `json:"planned_total"`
	Due               int64              `json:"due"`
	Stages            []StageProgress    `json:"stages"`
	WeakKPs           []WeakKP           `json:"weak_kps"`
	WeakQuestionTypes []WeakQuestionType `json:"weak_question_types"`
	Efficiency        Efficiency         `json:"efficiency"`
	LastStudyAt       *string            `json:"last_study_at,omitempty"`
}

// ---------------------------------------------------------------- 建议

// Suggestion 一条可解释的建议（§4.7）。
type Suggestion struct {
	Code     string             `json:"code"`
	Severity string             `json:"severity"` // info | notice
	Subject  string             `json:"subject,omitempty"`
	Title    string             `json:"title"`
	Detail   string             `json:"detail"`
	Data     map[string]any     `json:"data,omitempty"`
	Actions  []SuggestionAction `json:"actions"`
}

// SuggestionAction 建议附带的动作，前端按 type 渲染按钮。
type SuggestionAction struct {
	Type  string `json:"type"`
	Label string `json:"label"`
}

// SuggestionsView 建议列表。
type SuggestionsView struct {
	ChildID     string       `json:"child_id"`
	Suggestions []Suggestion `json:"suggestions"`
	GeneratedAt time.Time    `json:"generated_at"`
}

// SuggestionActionRequest 执行建议动作的请求体（§4.7）。
// kp_id 用于 assign_practice / lower_difficulty，subject 用于 balance_subjects。
type SuggestionActionRequest struct {
	Type    string `json:"type"`
	KPID    string `json:"kp_id,omitempty"`
	Subject string `json:"subject,omitempty"`
}

// SuggestionActionResult 动作执行结果。
// Applied 表示动作已受理并执行；幂等动作（如已在最低档再点降档）可能没有实际变化，
// 具体以 Detail 文案为准。
type SuggestionActionResult struct {
	Type    string         `json:"type"`
	Applied bool           `json:"applied"`
	Detail  string         `json:"detail"`
	Data    map[string]any `json:"data,omitempty"`
}

// ExportPDFResult 报表 PDF（周学习报告）导出结果。
// 复用打印任务：Status 初始为 queued，前端轮询 /print/jobs/{id} 直到 pdf_ready，
// 再从 PDFURL 下载。DataURL 是渲染数据快照，需要时可以自查内容。
type ExportPDFResult struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	PDFURL  string `json:"pdf_url"`
	DataURL string `json:"data_url"`
}

// ---------------------------------------------------------------- 节奏与效率

// PacePoint 一天的节奏数据。
type PacePoint struct {
	Date          string  `json:"date"`
	PlannedNew    int32   `json:"planned_new"`
	ActualNew     int32   `json:"actual_new"`
	RepeatCount   int32   `json:"repeat_count"`
	CumPlanned    int32   `json:"cum_planned"`
	CumActual     int32   `json:"cum_actual"`
	DeviationDays float64 `json:"deviation_days"`
}

// WeeklyEfficiency 按自然周的效率。
type WeeklyEfficiency struct {
	WeekStart          string  `json:"week_start"`
	MasteredCount      int64   `json:"mastered_count"`
	AttemptsSum        int64   `json:"attempts_sum"`
	AttemptsPerMastery float64 `json:"attempts_per_mastery"`
}

// PaceView 节奏偏差视图（§4.9）。
type PaceView struct {
	ChildID     string             `json:"child_id"`
	SubjectCode string             `json:"subject_code"`
	Days        int                `json:"days"`
	From        string             `json:"from"`
	To          string             `json:"to"`
	Series      []PacePoint        `json:"series"`
	Weekly      []WeeklyEfficiency `json:"weekly"`
	Efficiency  Efficiency         `json:"efficiency"`
	Current     PacePoint          `json:"current"`
	Note        string             `json:"note"`
}

// ---------------------------------------------------------------- 成长树

// GrowthSubject 学科成长树。
type GrowthSubject struct {
	SubjectCode  string          `json:"subject_code"`
	SubjectName  string          `json:"subject_name"`
	Mastered     int64           `json:"mastered"`
	PlannedTotal int64           `json:"planned_total"`
	Stages       []StageProgress `json:"stages"`
}

// GrowthView 成长树。
type GrowthView struct {
	ChildID  string          `json:"child_id"`
	Subjects []GrowthSubject `json:"subjects"`
}

// ---------------------------------------------------------------- 成就

// BadgeItem 徽章（含是否已获得）。
type BadgeItem struct {
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Category    string         `json:"category"`
	Description string         `json:"description"`
	Icon        string         `json:"icon"`
	Rule        map[string]any `json:"rule"`
	Earned      bool           `json:"earned"`
	EarnedAt    *string        `json:"earned_at,omitempty"`
	Progress    map[string]any `json:"progress,omitempty"`
}

// BadgesView 徽章列表。
type BadgesView struct {
	ChildID string      `json:"child_id"`
	Total   int         `json:"total"`
	Earned  int         `json:"earned"`
	Items   []BadgeItem `json:"items"`
}

// EarnedBadge 刚授予的徽章（供调用方记日志或推送）。
type EarnedBadge struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// ---------------------------------------------------------------- 多孩对比

// ComparePoint 对比曲线上的一个点。
//
// X 轴口径：align=session 时 index 是「第几个学习日」（各自从第一次学习起算，
// 比的是学习速度，不受入学早晚影响）；align=calendar 时 index 是距最早首日的天数，
// date 一并给出，看的是真实时间轴。
type ComparePoint struct {
	Index       int     `json:"index"`
	Date        string  `json:"date,omitempty"`
	CumMastered int64   `json:"cum_mastered"`
	NewMastered int64   `json:"new_mastered"`
	Attempts    int64   `json:"attempts"`
	CorrectRate float64 `json:"correct_rate"`
}

// CompareMetrics 一个孩子的汇总指标。
type CompareMetrics struct {
	MasteredTotal      int64   `json:"mastered_total"`
	PlannedTotal       int64   `json:"planned_total"`
	ActiveDays         int64   `json:"active_days"`
	AvgNewPerDay       float64 `json:"avg_new_per_day"`
	AttemptsPerMastery float64 `json:"attempts_per_mastery"`
	CorrectRate        float64 `json:"correct_rate"`
	DurationSec        int64   `json:"duration_sec"`
	DeviationDays      float64 `json:"deviation_days"`
}

// CompareChild 一个孩子的曲线与指标。
type CompareChild struct {
	ChildID   string         `json:"child_id"`
	Nickname  string         `json:"nickname"`
	AvatarID  string         `json:"avatar_id"`
	FirstDate string         `json:"first_date,omitempty"`
	Points    []ComparePoint `json:"points"`
	Metrics   CompareMetrics `json:"metrics"`
}

// CompareView 多孩对比（§4.10）。
type CompareView struct {
	Align       string         `json:"align"`
	SubjectCode string         `json:"subject_code"`
	Children    []CompareChild `json:"children"`
	Note        string         `json:"note"`
}
