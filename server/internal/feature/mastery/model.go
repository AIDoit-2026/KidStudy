// Package mastery 承载掌握度状态机、复习队列与错题本。
//
// 对外只暴露 service 方法：practice 模块答题后调用 ApplyResult，自己不碰这几张表。
package mastery

import (
	"time"

	"github.com/google/uuid"
)

// 状态机常量（《开发设计文档》§4.4）。
const (
	// MinLevel / MaxLevel —— level 0 未学，5 为毕业档
	MinLevel = 0
	MaxLevel = 5

	// MasteredLevel —— 达到此 level 记为「已掌握」，mastered_at 落时间戳
	MasteredLevel = 3

	// DefaultEase —— SM-2 初始难度因子
	DefaultEase = 2.5
	// MinEase —— 因子下限，再错也不会低于此值，否则间隔会坍缩到 0
	MinEase = 1.3

	// EasePenaltyHint —— 用提示答对仍算对，但因子小幅下调
	EasePenaltyHint = 0.10
	// EasePenaltyWrong —— 答错的因子惩罚
	EasePenaltyWrong = 0.20

	// WrongIntervalHours —— 答错后 10 分钟会话内复现（§4.3）
	WrongIntervalHours = 0.17

	// MinIntervalHours —— 首次答对后至少 1 小时再复习，避免「刚会就考」
	MinIntervalHours = 1

	// WrongBookClearStreak —— 连续答对 3 次移出错题本
	WrongBookClearStreak = 3

	// BacklogLimit —— 到期复习超过这个数就只下发复习、暂停新学（§4.4 复习积压保护）
	BacklogLimit = 100
)

// LevelIntervalsHours 是 level → 复习间隔（小时）的阶梯。
//
// 设计里写的是「1h→4h→1d→3d→7d→15d→毕业」，同时又要求「level≥5 时 30d 维持性复习」。
// 二者在 7d/15d 两档上冲突；这里取显式代码行的语义：level 5 是毕业档直接跳到 30 天，
// 中间不再逐级爬 —— 学到 level 5 的内容本来就该是长期记忆，多插两档没有意义。
var LevelIntervalsHours = map[int]float64{
	1: 1,   // 1 小时
	2: 4,   // 4 小时
	3: 24,  // 1 天
	4: 72,  // 3 天
	5: 720, // 30 天，维持性复习
}

// Record 是掌握度领域模型（对应 mastery_records 一行）。
type Record struct {
	KPID             uuid.UUID
	Level            int
	Ease             float64
	IntervalHours    float64
	NextReviewAt     time.Time
	Difficulty       int
	CorrectCount     int
	WrongCount       int
	Streak           int
	WrongStreak      int
	LastResult       string
	FirstLearnedAt   *time.Time
	MasteredAt       *time.Time
	Attempts         int
	AttemptsToMaster *int
	RepeatDays       int
	LastReviewDate   *time.Time
}

// NewRecord 造一条未学记录，作为 SM-2 递推的起点。
func NewRecord(nextReviewAt time.Time) Record {
	return Record{
		Level:         MinLevel,
		Ease:          DefaultEase,
		IntervalHours: 0,
		NextReviewAt:  nextReviewAt,
		Difficulty:    1,
	}
}

// Result 是 ApplyResult 的输出，供判分接口回给前端做即时反馈。
type Result struct {
	KPID                uuid.UUID `json:"kp_id"`
	Level               int       `json:"level"`
	Ease                float64   `json:"ease"`
	IntervalHours       float64   `json:"interval_hours"`
	NextReviewAt        string    `json:"next_review_at"`
	NextReviewInMinutes int       `json:"next_review_in_minutes"`
	Difficulty          int       `json:"difficulty"`
	Streak              int       `json:"streak"`
	WrongStreak         int       `json:"wrong_streak"`
	Mastered            bool      `json:"mastered"`           // 本次刚达到掌握
	EnteredWrongBook    bool      `json:"entered_wrong_book"` // 本次进错题本
	ClearedWrongBook    bool      `json:"cleared_wrong_book"` // 本次移出错题本
	DifficultyChanged   int       `json:"difficulty_changed"` // -1 降档 / +1 升档 / 0 不变
}

// ReviewItem 是复习队列里的一条。
type ReviewItem struct {
	KPID         uuid.UUID `json:"kp_id"`
	SubjectCode  string    `json:"subject_code"`
	Kind         string    `json:"kind"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Level        int       `json:"level"`
	Difficulty   int       `json:"difficulty"`
	NextReviewAt string    `json:"next_review_at"`
	OverdueHours float64   `json:"overdue_hours"`
}

// ReviewQueue 是复习队列查询结果。
type ReviewQueue struct {
	Items         []ReviewItem `json:"items"`
	DueCount      int64        `json:"due_count"`
	Backlog       bool         `json:"backlog"`        // true = 积压，今日应暂停新学
	SuggestReview bool         `json:"suggest_review"` // 给家长端的提示文案开关
}

// WrongEntry 是错题本条目。
type WrongEntry struct {
	ID                 uuid.UUID `json:"id"`
	KPID               uuid.UUID `json:"kp_id"`
	SubjectCode        string    `json:"subject_code"`
	Code               string    `json:"code"`
	Name               string    `json:"name"`
	WrongCount         int       `json:"wrong_count"`
	ConsecutiveCorrect int       `json:"consecutive_correct"`
	AddedAt            string    `json:"added_at"`
	ClearedAt          *string   `json:"cleared_at"`
}

// WrongBookPage 是错题本一页。
type WrongBookPage struct {
	Items []WrongEntry `json:"items"`
	Total int64        `json:"total"`
}
