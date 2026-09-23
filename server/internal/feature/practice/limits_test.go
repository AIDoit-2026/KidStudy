package practice

import "testing"

func TestNewLimitFor(t *testing.T) {
	cases := []struct {
		subject string
		pace    string
		want    int
	}{
		{"chinese", PaceStandard, DefaultNewChinese}, // 6
		{"english", PaceStandard, DefaultNewEnglish}, // 5
		{"math", PaceStandard, DefaultNewMath},       // 2
		{"chinese", "", DefaultNewChinese},           // 空串按标准
		{"chinese", PaceFast, 9},                     // 6 → 9（+50%）
		{"english", PaceFast, 8},                     // 5 → 8（向上取整）
		{"math", PaceFast, 3},                        // 2 → 3
		{"chinese", PaceReview, DefaultNewChinese},   // 只复习：量本身不变（由调用方不再取新学）
	}
	for _, c := range cases {
		if got := newLimitFor(c.subject, c.pace); got != c.want {
			t.Errorf("newLimitFor(%q,%q) = %d，期望 %d", c.subject, c.pace, got, c.want)
		}
	}
}

func TestPlanMessage_ReviewDayWins(t *testing.T) {
	// 复习日即使同时积压、也即使还有额度，文案都该说「复习日」
	p := TodayPlan{PaceMode: PaceReview, Backlog: true, RemainingMinutes: 30}
	if got := planMessage(p); got != "今天是复习日，先把已经学过的过一遍" {
		t.Errorf("复习日文案 = %q", got)
	}
	// 非复习日维持原有优先级：积压 > 额度用尽 > 无内容
	if got := planMessage(TodayPlan{Backlog: true, RemainingMinutes: 0}); got != "到期复习有点多，今天先专心复习吧" {
		t.Errorf("积压文案 = %q", got)
	}
	if got := planMessage(TodayPlan{PaceMode: PaceStandard, RemainingMinutes: 0}); got != "今天的练习时间用完啦，明天再来" {
		t.Errorf("额度用尽文案 = %q", got)
	}
	// 有题目、有额度、不积压 → 无提示（Items 非空才不会落到「没有新内容」分支）
	full := TodayPlan{PaceMode: PaceStandard, RemainingMinutes: 30, Items: []PlanItem{{}}}
	if got := planMessage(full); got != "" {
		t.Errorf("正常情况不该有提示，实际 %q", got)
	}
	// 空 Items 的兜底文案
	if got := planMessage(TodayPlan{PaceMode: PaceStandard, RemainingMinutes: 30}); got != "今天没有新的内容，可以去复习或者读故事" {
		t.Errorf("空任务文案 = %q", got)
	}
}
