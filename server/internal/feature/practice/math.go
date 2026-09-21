package practice

import (
	"encoding/json"

	"kidstudy/internal/pkg/mathgen"
)

// 数学题生成器已下沉到 internal/pkg/mathgen（纯函数、无状态），
// 目的是让打印中心复用同一生成器而不必 import practice —— 见 mathgen 包注释。
//
// 这里保留原有的类型名与函数名作为兼容层：practice 内部（questions.go / service.go）
// 与既有冒烟脚本的调用点一行都不用改，也不会出现两套生成逻辑。

// MathConfig 是 math_templates.generator_config 的结构。
type MathConfig = mathgen.Config

// MathQuestion 是一道生成出来的数学题。
type MathQuestion = mathgen.Question

// ParseMathConfig 解析模板配置，字段缺失时填默认值而不是报错。
func ParseMathConfig(raw json.RawMessage) (MathConfig, error) {
	return mathgen.ParseConfig(raw)
}

// GenerateMath 按模板配置生成 count 道题；同一 (seed, count) 必得同一批题。
func GenerateMath(cfg MathConfig, seed uint64, count int) []MathQuestion {
	return mathgen.Generate(cfg, seed, count)
}
