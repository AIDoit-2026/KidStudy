package mathgen

import (
	"fmt"
	"testing"
)

// 这些 golden 值是从「与 git 中旧实现逐题对拍一致」的那一版冻结下来的。
//
// 为什么值得用硬编码的期望值而不是「调用两次相等」这类性质断言：
// randx 的取数顺序决定了题面，任何一次无意的重排（哪怕只是把两个 r.Range
// 换了位置）都会让已打印出的卷子与屏幕上的题对不上。性质断言抓不到这种漂移，
// 只有 golden 能。改动本文件里任何一个数字，都必须先确认是有意为之。
func TestGenerateGolden(t *testing.T) {
	cases := []struct {
		op         string
		min, max   int
		seed       uint64
		idx        int
		noNeg      bool
		secondMax  int
		wantPrompt string
		wantAnswer string
	}{
		{"add", 0, 5, 12345, 0, true, 0, "3 + 0 = ?", "3"},
		{"add", 0, 5, 12345, 4, true, 0, "3 + 2 = ?", "5"},
		{"sub", 0, 20, 999, 0, true, 0, "11 - 10 = ?", "1"},
		{"sub", 0, 20, 999, 1, true, 0, "10 - 4 = ?", "6"},
		// no_negative=false 时必须允许出现负数（减法题卡不该偷偷把顺序换掉）
		{"sub", 0, 9, 3, 1, false, 0, "6 - 8 = ?", "-2"},
		{"mul", 1, 9, 4242, 0, true, 9, "4 × 8 = ?", "32"},
		{"div", 1, 9, 777, 0, true, 0, "56 ÷ 8 = ?", "7"},
		{"cmp", 0, 20, 31337, 2, true, 0, "1 ○ 15（在○里填 >、< 或 =）", "<"},
		{"mixed", 1, 9, 8888, 0, true, 0, "4 + 6 × 9 = ?", "58"},
		{"mixed", 1, 9, 8888, 1, true, 0, "8 - 4 + 4 = ?", "8"},
		{"word", 0, 20, 55, 0, true, 0, "每盒有 14 支彩笔，买了 11 盒。一共有几支彩笔？", "154"},
		{"seq", 1, 30, 20240921, 0, true, 0, "18, 19, 20, 21, ____", "22"},
		{"seq", 1, 30, 20240921, 4, true, 0, "6, 9, 12, 15, ____", "18"},
		// 未知 op 必须有确定的落点（默认走加法），否则家长手改模板配置会得到空题
		{"unknown_op", 1, 6, 2024, 0, true, 0, "2 + 2 = ?", "4"},
	}

	for _, c := range cases {
		cfg := Config{Op: c.op, Min: c.min, Max: c.max, Terms: 2, NoNegative: c.noNeg, SecondMax: c.secondMax}
		got := Generate(cfg, c.seed, c.idx+1)
		if len(got) <= c.idx {
			t.Fatalf("%s seed=%d: 生成数量不足，期望至少 %d 道，得到 %d 道", c.op, c.seed, c.idx+1, len(got))
		}
		if got[c.idx].Prompt != c.wantPrompt {
			t.Errorf("%s seed=%d #%d 题面漂移：\n  got  %q\n  want %q", c.op, c.seed, c.idx, got[c.idx].Prompt, c.wantPrompt)
		}
		if got[c.idx].Answer != c.wantAnswer {
			t.Errorf("%s seed=%d #%d 答案漂移：\n  got  %q\n  want %q", c.op, c.seed, c.idx, got[c.idx].Answer, c.wantAnswer)
		}
	}
}

// 同 seed 必须完全可复现 —— 打印中心「同一 seed 的题与屏幕逐题对应」的前提。
func TestGenerateDeterministic(t *testing.T) {
	cfg := Config{Op: "mixed", Min: 1, Max: 9, Terms: 2}
	first := Generate(cfg, 777, 20)
	second := Generate(cfg, 777, 20)
	if len(first) != len(second) {
		t.Fatalf("两次生成数量不同: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("第 %d 题不可复现: %+v vs %+v", i, first[i], second[i])
		}
	}
}

// 逐题派生种子（Derive(seed, i)）的意义：中途增删题目不影响其余题目。
func TestGenerateStableWhenCountChanges(t *testing.T) {
	cfg := Config{Op: "add", Min: 0, Max: 100, Terms: 2}
	short := Generate(cfg, 4242, 5)
	long := Generate(cfg, 4242, 30)
	for i := range short {
		if short[i] != long[i] {
			t.Fatalf("题目数从 5 增到 30 后第 %d 题漂移了: %+v vs %+v", i, short[i], long[i])
		}
	}
}

func TestGenerateDifferentSeedsDiffer(t *testing.T) {
	cfg := Config{Op: "add", Min: 0, Max: 100, Terms: 2}
	a := Generate(cfg, 1, 10)
	b := Generate(cfg, 2, 10)
	same := 0
	for i := range a {
		if a[i] == b[i] {
			same++
		}
	}
	// 允许偶然重合，但不该整批一样
	if same == len(a) {
		t.Fatalf("换 seed 后 10 道题全部相同，随机源可能失效")
	}
}

func TestGenerateNonPositiveCount(t *testing.T) {
	cfg := Config{Op: "add", Min: 0, Max: 5}
	if got := Generate(cfg, 1, 0); got != nil {
		t.Fatalf("count=0 应返回 nil，得到 %v", got)
	}
	if got := Generate(cfg, 1, -3); got != nil {
		t.Fatalf("count<0 应返回 nil，得到 %v", got)
	}
}

// 找规律：四项递增、公差一致、答案是第五项。这是打印件上家长核对答案的依据。
func TestGenSeqIsArithmetic(t *testing.T) {
	cfg := Config{Op: "seq", Min: 1, Max: 30}
	for i := 0; i < 50; i++ {
		q := Generate(cfg, uint64(i+1), 1)[0]
		var a, b, c, d int
		// 题面形如 "18, 19, 20, 21, ____"
		if _, err := fmt.Sscanf(q.Prompt, "%d, %d, %d, %d,", &a, &b, &c, &d); err != nil {
			t.Fatalf("题面格式不符合预期: %q (%v)", q.Prompt, err)
		}
		step1, step2, step3 := b-a, c-b, d-c
		if step1 != step2 || step2 != step3 || step1 <= 0 {
			t.Fatalf("不是递增等差数列: %q（公差 %d/%d/%d）", q.Prompt, step1, step2, step3)
		}
		// 答案必须是第五项
		var want int
		if _, err := fmt.Sscanf(q.Answer, "%d", &want); err != nil {
			t.Fatalf("答案不是整数: %q", q.Answer)
		}
		if want != d+step1 {
			t.Fatalf("答案与公差对不上: 题面 %q 答案 %d，按公差应为 %d", q.Prompt, want, d+step1)
		}
	}
}
