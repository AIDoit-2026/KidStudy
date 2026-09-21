// Package parent 承载家长控制项（时长额度、休息间隔、完成判定开关、节奏模式、对比开关）。
//
// 目前只做设置读写；打印记录（/parent/print-jobs）随 M5 的打印中心一起进这个模块。
package parent

import "time"

// 与 parent_settings 表默认值保持一致：没有记录时返回这些，家长第一次打开看到的就是默认态。
const (
	DefaultDailyLimitMin   = 60
	DefaultSessionLimitMin = 20
	DefaultRestIntervalMin = 20
)

// Settings 家长控制项（对外视图）。
type Settings struct {
	DailyLimitMin        int       `json:"daily_limit_min"`
	SessionLimitMin      int       `json:"session_limit_min"`
	RestIntervalMin      int       `json:"rest_interval_min"`
	RequireParentConfirm bool      `json:"require_parent_confirm"`
	PaceMode             string    `json:"pace_mode"`
	CompareChildren      bool      `json:"compare_children"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// UpdateRequest 部分更新：只改传来的字段，没传的保持原值。
//
// 用指针而不是零值判断，是因为 false / 0 都是合法取值
// （关闭家长确认、每日时长设为 0 表示不限），不能用「零值 = 未提供」。
type UpdateRequest struct {
	DailyLimitMin        *int    `json:"daily_limit_min"`
	SessionLimitMin      *int    `json:"session_limit_min"`
	RestIntervalMin      *int    `json:"rest_interval_min"`
	RequireParentConfirm *bool   `json:"require_parent_confirm"`
	PaceMode             *string `json:"pace_mode"`
	CompareChildren      *bool   `json:"compare_children"`
}
