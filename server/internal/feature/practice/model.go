// Package practice 负责今日任务编排、组卷、判分与会话生命周期。
//
// 依赖方向：practice → mastery（掌握度状态机）、practice → content（组卷素材）、
// practice → children（归属校验）。反向依赖一律不允许。
package practice

import (
	"time"

	"github.com/google/uuid"
)

// 九种题型（《开发设计文档》§4.2）。
const (
	TypeChoiceText  = "choice_text"  // 看拼音/释义选字选词
	TypeChoiceImage = "choice_image" // 看图选词（无图时退化为大卡片）
	TypeChoiceAudio = "choice_audio" // 听音选词（前端 TTS 朗读 audio_text）
	TypeFillBlank   = "fill_blank"   // 补全（汉字选字填空 / 英语字母填空）
	TypeMatch       = "match"        // 连线配对（字↔拼音、英↔中）
	TypeOrder       = "order"        // 排字成词 / 排字母成词
	TypeTrace       = "trace"        // 描红：只记完成，不计正确率
	TypeSay         = "say"          // 跟读/朗读：主观项，家长确认
	TypeMathParam   = "math_param"   // 数学参数化生成
)

// 答案比对方式。
const (
	KeyKindSingle   = "single"   // 单选：答案是一组选项 id 中的一个
	KeyKindSequence = "sequence" // 排序：答案要求顺序一致
	KeyKindFree     = "free"     // 自由输入：与 accept 列表任一相等即可
	KeyKindMatch    = "match"    // 连线：一组「左|右」配对
	KeyKindNone     = "none"     // 不判分（trace / say）
)

// 题目来源，决定题型偏好。
const (
	ReasonReview   = "review"
	ReasonWrong    = "wrong"
	ReasonNew      = "new"
	ReasonAssigned = "assigned"
)

// 每日新学量默认值（§4.2 步骤 4），家长可在设置里覆盖。
const (
	DefaultNewChinese = 6
	DefaultNewEnglish = 5
	DefaultNewMath    = 2
	MaxNewPerSubject  = 20
	DefaultReviewMax  = 20
	MaxWrongFirst     = 5
)

// Question 是下发给孩子端的题面。不含任何答案字段。
type Question struct {
	Type        string         `json:"type"`
	SubjectCode string         `json:"subject_code"`
	Prompt      string         `json:"prompt"`
	PromptSub   string         `json:"prompt_sub,omitempty"`
	AudioText   string         `json:"audio_text,omitempty"` // choice_audio / say 的朗读内容
	Options     []Option       `json:"options,omitempty"`
	MatchLeft   []string       `json:"match_left,omitempty"`
	MatchRight  []string       `json:"match_right,omitempty"`
	OrderItems  []string       `json:"order_items,omitempty"`
	Layout      string         `json:"layout,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
}

// Option 是一个选项。
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Image string `json:"image,omitempty"`
}

// 连线题的两列分别下发且各自乱序，避免题面直接暴露配对关系。

// AnswerKey 是服务端判分用的答案，任何读接口都不下发。
type AnswerKey struct {
	Kind    string   `json:"kind"`
	Correct []string `json:"correct,omitempty"` // single: 选项 id；sequence: 有序序列；match: "左|右"
	Accept  []string `json:"accept,omitempty"`  // free: 可接受的等价答案
	Text    string   `json:"text,omitempty"`    // 正确答案的可读文本，仅在作答后回传
	Explain string   `json:"explain,omitempty"` // 答错时展示的解释，不用红叉
}

// BuiltQuestion 是组卷产物：题面与答案分开存放。
type BuiltQuestion struct {
	KPID         uuid.UUID
	SubjectCode  string
	StageCode    string
	QuestionType string
	Difficulty   int
	Snapshot     Question
	Key          AnswerKey
}

// PlanItem 是今日任务里的一项（还没组卷）。
type PlanItem struct {
	KPID         uuid.UUID `json:"kp_id"`
	SubjectCode  string    `json:"subject_code"`
	Kind         string    `json:"kind"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Reason       string    `json:"reason"`
	Difficulty   int       `json:"difficulty"`
	PlannedDay   int       `json:"planned_day,omitempty"`
	QuestionType string    `json:"question_type,omitempty"` // 编排时先定题型，供前端预览
}

// TodayPlan 是 GET /practice/today 的返回。
type TodayPlan struct {
	ChildID          uuid.UUID  `json:"child_id"`
	GeneratedAt      string     `json:"generated_at"`
	RemainingMinutes int        `json:"remaining_minutes"`
	DailyLimitMin    int        `json:"daily_limit_min"`
	UsedMinutes      int        `json:"used_minutes"`
	Backlog          bool       `json:"backlog"`
	SuggestReview    bool       `json:"suggest_review"`
	DueCount         int        `json:"due_count"`
	Subjects         []string   `json:"subjects"`
	Items            []PlanItem `json:"items"`
	Message          string     `json:"message,omitempty"`
}

// SessionView 是会话对外视图。
type SessionView struct {
	ID        string     `json:"id"`
	ChildID   string     `json:"child_id"`
	Status    string     `json:"status"`
	StartedAt string     `json:"started_at"`
	EndedAt   *string    `json:"ended_at"`
	ItemCount int        `json:"item_count"`
	Items     []ItemView `json:"items"`
}

// ItemView 是单个题目的对外视图。is_correct 只在已作答时给出。
type ItemView struct {
	ID            string   `json:"id"`
	Seq           int      `json:"seq"`
	KPID          string   `json:"kp_id"`
	SubjectCode   string   `json:"subject_code"`
	QuestionType  string   `json:"question_type"`
	State         string   `json:"state"`
	Question      Question `json:"question"`
	IsCorrect     *bool    `json:"is_correct,omitempty"`
	CorrectAnswer *string  `json:"correct_answer,omitempty"` // 仅在作答后回传，供「答错看正确答案」
	Explain       string   `json:"explain,omitempty"`
	UsedHint      bool     `json:"used_hint"`
	ElapsedMS     int      `json:"elapsed_ms,omitempty"`
}

// AnswerResult 是判分结果。
type AnswerResult struct {
	ItemID      string        `json:"item_id"`
	IsCorrect   *bool         `json:"is_correct"` // nil = 不判分（trace/say）
	CorrectText string        `json:"correct_text,omitempty"`
	Explain     string        `json:"explain,omitempty"`
	State       string        `json:"state"`
	Mastery     *MasteryBrief `json:"mastery,omitempty"`
	StarDelta   int           `json:"star_delta"`
}

// MasteryBrief 是判分响应里附带的掌握度变化，供前端做即时反馈。
type MasteryBrief struct {
	Level             int  `json:"level"`
	NextReviewMinutes int  `json:"next_review_in_minutes"`
	Mastered          bool `json:"mastered"`
	EnteredWrongBook  bool `json:"entered_wrong_book"`
	ClearedWrongBook  bool `json:"cleared_wrong_book"`
	DifficultyChanged int  `json:"difficulty_changed"`
}

// SessionSummary 是结算结果。
type SessionSummary struct {
	SessionID     string  `json:"session_id"`
	Status        string  `json:"status"`
	DurationSec   int     `json:"duration_sec"`
	QuestionCount int     `json:"question_count"`
	AnsweredCount int     `json:"answered_count"`
	CorrectCount  int     `json:"correct_count"`
	SkippedCount  int     `json:"skipped_count"`
	Accuracy      float64 `json:"accuracy"`
	StarCount     int     `json:"star_count"`
	CompletedBy   string  `json:"completed_by"`
	Passed        bool    `json:"passed"`
	Message       string  `json:"message"`
}

// ConfirmRequest 是家长确认/补录请求。
type ConfirmRequest struct {
	ChildID     string `json:"child_id"`
	ParentScore *int   `json:"parent_score"` // 1–5，主观项打分
	ParentNote  string `json:"parent_note"`
	MarkDone    bool   `json:"mark_done"` // 兜底：手动标记完成
}

// 星级阈值（§4.3）：≥90% 且没用提示 = 3 星，≥75% = 2 星，≥60% = 1 星。
const (
	StarThreeAccuracy = 0.90
	StarTwoAccuracy   = 0.75
	StarOneAccuracy   = 0.60
	// PassAccuracy 是「今日完成」的达标线（§4.11 客观题判定）
	PassAccuracy = 0.60
)

// ComputeStars 按正确率与是否用过提示算星级。
func ComputeStars(accuracy float64, usedHint bool) int {
	switch {
	case accuracy >= StarThreeAccuracy && !usedHint:
		return 3
	case accuracy >= StarTwoAccuracy:
		return 2
	case accuracy >= StarOneAccuracy:
		return 1
	default:
		return 0
	}
}

// Now 是可替换的时间源，方便测试固定时间。
var Now = time.Now

// Session 是学习会话领域模型。
type Session struct {
	ID            uuid.UUID
	ChildID       uuid.UUID
	DeviceType    string
	StartedAt     time.Time
	EndedAt       *time.Time
	DurationSec   int
	QuestionCount int
	AnsweredCount int
	CorrectCount  int
	SkippedCount  int
	StarCount     int
	CompletedBy   string
	ParentScore   *int
	ParentNote    string
	Status        string
}

// Item 是会话中的一道题（含答案，只在服务端流转）。
type Item struct {
	ID           uuid.UUID
	SessionID    uuid.UUID
	Seq          int
	KPID         uuid.UUID
	SubjectCode  string
	StageCode    string
	QuestionType string
	Difficulty   int
	Snapshot     Question
	Key          AnswerKey
	State        string
	IsCorrect    *bool
	UsedHint     bool
	ElapsedMS    int
	AnsweredAt   *time.Time
}

// ParentSettings 是编排时要用到的家长控制项。
type ParentSettings struct {
	DailyLimitMin        int
	SubjectSwitches      map[string]bool
	RequireParentConfirm bool
}
