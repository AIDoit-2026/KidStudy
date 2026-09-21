package practice

import (
	"encoding/json"
	"fmt"
	"strconv"

	"kidstudy/internal/pkg/randx"
)

// MathConfig 是 math_templates.generator_config 的结构。
type MathConfig struct {
	Op         string `json:"op"`
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Terms      int    `json:"terms"`
	NoNegative bool   `json:"no_negative"`
	Exact      bool   `json:"exact"`
	SecondMax  int    `json:"second_max"`
}

// MathQuestion 是一道生成出来的数学题。
type MathQuestion struct {
	Template string `json:"template"`
	Prompt   string `json:"prompt"`
	Answer   string `json:"answer"`
	Explain  string `json:"explain"`
	Layout   string `json:"layout"`
}

// ParseMathConfig 解析模板配置，字段缺失时填默认值而不是报错 ——
// 家长在后台手改 JSON 时漏一个字段不该让整次组卷失败。
func ParseMathConfig(raw json.RawMessage) (MathConfig, error) {
	var cfg MathConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return MathConfig{}, fmt.Errorf("数学模板配置解析失败: %w", err)
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

// GenerateMath 按模板配置生成 count 道题。
//
// 第 i 题用 Derive(seed, i) 派生独立种子：同一 (seed, count) 必得同一批题，
// 且中途增删题目不会让其余题目漂移 —— 屏幕练习与打印中心才能逐题对上。
func GenerateMath(cfg MathConfig, seed uint64, count int) []MathQuestion {
	if count <= 0 {
		return nil
	}
	out := make([]MathQuestion, 0, count)
	for i := 0; i < count; i++ {
		r := randx.New(randx.Derive(seed, i))
		q := generateOne(cfg, r)
		out = append(out, q)
	}
	return out
}

func generateOne(cfg MathConfig, r *randx.Rand) MathQuestion {
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

func layoutOf(cfg MathConfig) string { return cfg.Op }

func genAdd(cfg MathConfig, r *randx.Rand) MathQuestion {
	a, b := r.Range(cfg.Min, cfg.Max), r.Range(cfg.Min, cfg.Max)
	return MathQuestion{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d + %d = ?", a, b),
		Answer:   strconv.Itoa(a + b),
		Explain:  fmt.Sprintf("%d 加上 %d 等于 %d", a, b, a+b),
		Layout:   layoutOf(cfg),
	}
}

func genSub(cfg MathConfig, r *randx.Rand) MathQuestion {
	a, b := r.Range(cfg.Min, cfg.Max), r.Range(cfg.Min, cfg.Max)
	if cfg.NoNegative && a < b {
		a, b = b, a
	}
	return MathQuestion{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d - %d = ?", a, b),
		Answer:   strconv.Itoa(a - b),
		Explain:  fmt.Sprintf("%d 减去 %d 等于 %d", a, b, a-b),
		Layout:   layoutOf(cfg),
	}
}

func genMul(cfg MathConfig, r *randx.Rand) MathQuestion {
	hi := cfg.Max
	if cfg.SecondMax > 0 {
		hi = cfg.SecondMax
	}
	a, b := r.Range(cfg.Min, cfg.Max), r.Range(cfg.Min, hi)
	return MathQuestion{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d × %d = ?", a, b),
		Answer:   strconv.Itoa(a * b),
		Explain:  fmt.Sprintf("%d 乘 %d 等于 %d", a, b, a*b),
		Layout:   layoutOf(cfg),
	}
}

func genDiv(cfg MathConfig, r *randx.Rand) MathQuestion {
	lo := cfg.Min
	if lo < 2 {
		lo = 2
	}
	b := r.Range(lo, cfg.Max)
	q := r.Range(2, cfg.Max)
	a := b * q
	return MathQuestion{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d ÷ %d = ?", a, b),
		Answer:   strconv.Itoa(q),
		Explain:  fmt.Sprintf("%d 平均分成 %d 份，每份是 %d", a, b, q),
		Layout:   layoutOf(cfg),
	}
}

func genCmp(cfg MathConfig, r *randx.Rand) MathQuestion {
	a, b := r.Range(cfg.Min, cfg.Max), r.Range(cfg.Min, cfg.Max)
	symbol := "="
	switch {
	case a > b:
		symbol = ">"
	case a < b:
		symbol = "<"
	}
	return MathQuestion{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d ○ %d（在○里填 >、< 或 =）", a, b),
		Answer:   symbol,
		Explain:  fmt.Sprintf("%d 和 %d 相比，应填 %s", a, b, symbol),
		Layout:   layoutOf(cfg),
	}
}

// genMixed 两步混合运算：一半出「先乘后加」教运算优先级，一半出「加减同级从左到右」。
func genMixed(cfg MathConfig, r *randx.Rand) MathQuestion {
	a := r.Range(cfg.Min, cfg.Max)
	b := r.Range(cfg.Min, cfg.Max)
	c := r.Range(cfg.Min, cfg.Max)
	if r.Intn(2) == 0 {
		return MathQuestion{
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
	return MathQuestion{
		Template: cfg.Op,
		Prompt:   fmt.Sprintf("%d - %d + %d = ?", a, b, c),
		Answer:   strconv.Itoa(left + c),
		Explain:  fmt.Sprintf("先算 %d - %d = %d，再加 %d 得 %d", a, b, left, c, left+c),
		Layout:   layoutOf(cfg),
	}
}

// genWord 简单应用题：句式固定几套，随机取一套再套数字。
func genWord(cfg MathConfig, r *randx.Rand) MathQuestion {
	a := r.Range(cfg.Min, cfg.Max)
	b := r.Range(cfg.Min, cfg.Max)
	switch r.Intn(3) {
	case 0:
		return MathQuestion{
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
		return MathQuestion{
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
		return MathQuestion{
			Template: cfg.Op,
			Prompt:   fmt.Sprintf("每盒有 %d 支彩笔，买了 %d 盒。一共有几支彩笔？", a, b),
			Answer:   strconv.Itoa(a * b),
			Explain:  fmt.Sprintf("%d × %d = %d（支）", a, b, a*b),
			Layout:   layoutOf(cfg),
		}
	}
}
