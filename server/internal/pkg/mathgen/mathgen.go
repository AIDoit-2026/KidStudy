// Package mathgen 是数学题参数化生成器（纯函数，无状态、无 IO）。
//
// 为什么单独成一个 pkg 而不是留在 practice：
//
//	屏幕练习（practice）与打印中心（print）必须用同一个生成器，才能保证
//	「同一 seed 的题面在屏幕、打印预览、PDF 三处逐题一致」（§4.2 / §4.6）。
//	而 print 按设计只能依赖 content / mastery / storage，不该为了生成题目去 import practice。
//	把纯生成逻辑下沉到 pkg，两边都依赖它，依赖图保持单向。
//
// 确定性约定（改动此文件必须守住）：
//
//	Generate(cfg, seed, count) 的第 i 题种子是 randx.Derive(seed, i)。
//	逐题派生而非共享一条随机流，意味着中途增删题目不会让其余题目漂移 ——
//	这是「打印一张 20 题的卷子，与屏幕上那 20 题对得上」的前提。
//	任何新的取随机数顺序变化都会让历史打印件与当前屏幕不一致，属于破坏性改动。
package mathgen

import (
	"encoding/json"
	"fmt"
	"strconv"

	"kidstudy/internal/pkg/randx"
)

// Config 是 math_templates.generator_config 的结构。
type Config struct {
	Op         string `json:"op"`
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Terms      int    `json:"terms"`
	NoNegative bool   `json:"no_negative"`
	Exact      bool   `json:"exact"`
	SecondMax  int    `json:"second_max"`
}

// Question 是一道生成出来的数学题。
type Question struct {
	Template string `json:"template"`
	Prompt   string `json:"prompt"`
	Answer   string `json:"answer"`
	Explain  string `json:"explain"`
	Layout   string `json:"layout"`
}

// ParseConfig 解析模板配置，字段缺失时填默认值而不是报错 ——
// 家长在后台手改 JSON 时漏一个字段不该让整次组卷失败。
func ParseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("数学模板配置解析失败: %w", err)
		}
	}
	if cfg.Max == 0 {
		cfg.Max = 10
	}
	if cfg.Min > cfg.Max {
		cfg.Min, cfg.Max = cfg.Max, cfg.Min
	}
	if cfg.Terms <= 0 {
		cfg.Terms = 2
	}
	return cfg, nil
}

// Generate 按模板配置生成 count 道题。
//
// 第 i 题用 Derive(seed, i) 派生独立种子：同一 (seed, count) 必得同一批题，
// 且中途增删题目不会让其余题目漂移 —— 屏幕练习与打印中心才能逐题对上。
func Generate(cfg Config, seed uint64, count int) []Question {
	if count <= 0 {
		return nil
	}
	out := make([]Question, 0, count)
	for i := 0; i < count; i++ {
		r := randx.New(randx.Derive(seed, i))
		q := generateOne(cfg, r)
		out = append(out, q)
	}
	return out
}

func generateOne(cfg Config, r *randx.Rand) Question {
	switch cfg.Op {
	case "sub":
		return genSub(cfg, r)
	case "mul":
		return genMul(cfg, r)
	case "div":
		return genDiv(cfg, r)
	case "cmp":
		return genCmp(cfg, r)
	case "mixed":
		return genMixed(cfg, r)
	case "word":
		return genWord(cfg, r)
	default:
		return genAdd(cfg, r)
	}
}

func layoutOf(cfg Config) string { return cfg.Op }

func genAdd(cfg Config, r *randx.Rand) Question {
	a, b := r.Range(cfg.Min, cfg.Max), r.Range(cfg.Min, cfg.Max)
	return Question{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d + %d = ?", a, b),
		Answer:   strconv.Itoa(a + b),
		Explain:  fmt.Sprintf("%d 加上 %d 等于 %d", a, b, a+b),
		Layout:   layoutOf(cfg),
	}
}

func genSub(cfg Config, r *randx.Rand) Question {
	a, b := r.Range(cfg.Min, cfg.Max), r.Range(cfg.Min, cfg.Max)
	if cfg.NoNegative && a < b {
		a, b = b, a
	}
	return Question{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d - %d = ?", a, b),
		Answer:   strconv.Itoa(a - b),
		Explain:  fmt.Sprintf("%d 减去 %d 等于 %d", a, b, a-b),
		Layout:   layoutOf(cfg),
	}
}

func genMul(cfg Config, r *randx.Rand) Question {
	hi := cfg.Max
	if cfg.SecondMax > 0 {
		hi = cfg.SecondMax
	}
	a, b := r.Range(cfg.Min, cfg.Max), r.Range(cfg.Min, hi)
	return Question{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d × %d = ?", a, b),
		Answer:   strconv.Itoa(a * b),
		Explain:  fmt.Sprintf("%d 乘 %d 等于 %d", a, b, a*b),
		Layout:   layoutOf(cfg),
	}
}

func genDiv(cfg Config, r *randx.Rand) Question {
	lo := cfg.Min
	if lo < 2 {
		lo = 2
	}
	b := r.Range(lo, cfg.Max)
	q := r.Range(2, cfg.Max)
	a := b * q
	return Question{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d ÷ %d = ?", a, b),
		Answer:   strconv.Itoa(q),
		Explain:  fmt.Sprintf("%d 平均分成 %d 份，每份是 %d", a, b, q),
		Layout:   layoutOf(cfg),
	}
}

func genCmp(cfg Config, r *randx.Rand) Question {
	a, b := r.Range(cfg.Min, cfg.Max), r.Range(cfg.Min, cfg.Max)
	symbol := "="
	switch {
	case a > b:
		symbol = ">"
	case a < b:
		symbol = "<"
	}
	return Question{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d ○ %d（在○里填 >、< 或 =）", a, b),
		Answer:   symbol,
		Explain:  fmt.Sprintf("%d 和 %d 相比，应填 %s", a, b, symbol),
		Layout:   layoutOf(cfg),
	}
}

// genMixed 两步混合运算：一半出「先乘后加」教运算优先级，一半出「加减同级从左到右」。
func genMixed(cfg Config, r *randx.Rand) Question {
	a := r.Range(cfg.Min, cfg.Max)
	b := r.Range(cfg.Min, cfg.Max)
	c := r.Range(cfg.Min, cfg.Max)
	if r.Intn(2) == 0 {
		return Question{
			Template: cfg.Op,
			Prompt:   fmt.Sprintf("%d + %d × %d = ?", a, b, c),
			Answer:   strconv.Itoa(a + b*c),
			Explain:  fmt.Sprintf("先算乘法 %d × %d = %d，再加 %d 得 %d", b, c, b*c, a, a+b*c),
			Layout:   layoutOf(cfg),
		}
	}
	if a < b {
		a, b = b, a
	}
	left := a - b
	return Question{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d - %d + %d = ?", a, b, c),
		Answer:   strconv.Itoa(left + c),
		Explain:  fmt.Sprintf("先算 %d - %d = %d，再加 %d 得 %d", a, b, left, c, left+c),
		Layout:   layoutOf(cfg),
	}
}

// genWord 简单应用题：句式固定几套，随机取一套再套数字。
func genWord(cfg Config, r *randx.Rand) Question {
	a := r.Range(cfg.Min, cfg.Max)
	b := r.Range(cfg.Min, cfg.Max)
	switch r.Intn(3) {
	case 0:
		return Question{
			Template: cfg.Op,
			Prompt:   fmt.Sprintf("小明有 %d 个苹果，妈妈又给了他 %d 个。现在一共有几个苹果？", a, b),
			Answer:   strconv.Itoa(a + b),
			Explain:  fmt.Sprintf("%d + %d = %d（个）", a, b, a+b),
			Layout:   layoutOf(cfg),
		}
	case 1:
		if a < b {
			a, b = b, a
		}
		return Question{
			Template: cfg.Op,
			Prompt:   fmt.Sprintf("树上有 %d 只小鸟，飞走了 %d 只。树上还剩几只小鸟？", a, b),
			Answer:   strconv.Itoa(a - b),
			Explain:  fmt.Sprintf("%d - %d = %d（只）", a, b, a-b),
			Layout:   layoutOf(cfg),
		}
	default:
		if b < 2 {
			b = 2
		}
		return Question{
			Template: cfg.Op,
			Prompt:   fmt.Sprintf("每盒有 %d 支彩笔，买了 %d 盒。一共有几支彩笔？", a, b),
			Answer:   strconv.Itoa(a * b),
			Explain:  fmt.Sprintf("%d × %d = %d（支）", a, b, a*b),
			Layout:   layoutOf(cfg),
		}
	}
}
