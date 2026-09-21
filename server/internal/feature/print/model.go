package print

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RenderVersion 是渲染契约版本。
//
// 改动 payload 结构或模板语义时必须 +1：历史 job 的 payload 会原样重渲染，
// 版本号让「这份 PDF 是按哪一版渲染契约出的」可追溯（§4.6 打印记录）。
const RenderVersion = 1

// ---------------------------------------------------------------- 渲染数据快照

// Payload 是落进 print_jobs.payload 的渲染数据快照。
//
// 设计成自洽的：worker 渲染 PDF 时只依赖它 + template_code，不回查 content/mastery，
// 否则模板改版或内容下线会让历史打印件渲染不出原样。
type Payload struct {
	TemplateCode string   `json:"template_code"`
	TemplateName string   `json:"template_name"`
	RenderVer    int      `json:"render_version"`
	Title        string   `json:"title"`
	Subtitle     string   `json:"subtitle"`
	Meta         Meta     `json:"meta"`
	Options      Options  `json:"options"`
	Items        []Item   `json:"items"`
	AnswerItems  []Item   `json:"answer_items"`
	Sheets       []Sheet  `json:"sheets"`
	Footer       string   `json:"footer"`
	Warnings     []string `json:"warnings"`
}

// Meta 是页眉页脚要用的元信息。
type Meta struct {
	ChildName   string `json:"child_name"`
	ChildStage  string `json:"child_stage"`
	DateLabel   string `json:"date_label"`
	Watermark   string `json:"watermark"`
	Paper       string `json:"paper"`
	Copies      int    `json:"copies"`
	GeneratedAt string `json:"generated_at"`
	Source      string `json:"source"`
}

// Options 是家长填的排版参数（已归一化）。
type Options struct {
	FontSize   int  `json:"font_size"`
	WithPinyin bool `json:"with_pinyin"`
	WithAnswer bool `json:"with_answer"`
	PerPage    int  `json:"per_page"`
	Columns    int  `json:"columns"`
	BlankLines bool `json:"blank_lines"`
	ShowStroke bool `json:"show_stroke"`
}

// Item 是一道题 / 一张卡 / 一行内容。
//
// 用一组可选字段而不是每模板一个结构：10 套模板共用同一个渲染入口与同一份 payload
// schema，前端拿到的 /data 也不需要按模板分支解析。各模板只取自己关心的字段。
type Item struct {
	Seq    int      `json:"seq"`
	Main   string   `json:"main"`
	Sub    string   `json:"sub"`
	Answer string   `json:"answer"`
	Hint   string   `json:"hint"`
	Extra  []string `json:"extra"`
	KpID   string   `json:"kp_id"`
	Style  string   `json:"style"`
}

// Sheet 是报告类模板的表格分区（周学习报告用）。
type Sheet struct {
	Title string     `json:"title"`
	Note  string     `json:"note"`
	Head  []string   `json:"head"`
	Rows  [][]string `json:"rows"`
}

// ---------------------------------------------------------------- 模板注册表

// 模板分类。
const (
	CategoryHanzi   = "hanzi"
	CategoryPinyin  = "pinyin"
	CategoryMath    = "math"
	CategoryEnglish = "english"
	CategoryStory   = "story"
	CategoryReport  = "report"
)

// ParamSpec 描述一个可填参数，直接下发给前端渲染表单（§4.6 步骤 2）。
type ParamSpec struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // int | bool | enum | text
	Default any      `json:"default"`
	Min     *int     `json:"min,omitempty"`
	Max     *int     `json:"max,omitempty"`
	Options []string `json:"options,omitempty"`
	Help    string   `json:"help,omitempty"`
}

// TemplateSpec 是一套打印模板的定义。
type TemplateSpec struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	PaperSize   string `json:"paper_size"`
	DoubleSided bool   `json:"double_sided"`
	// Answerable 表示这套模板的题目能反向写成掌握度（纸质补录有意义）。
	// 闪卡 / 拼音格这类纯教具不产生作答记录，补录接口会拒绝。
	Answerable bool `json:"answerable"`
	// NeedsChild 表示必须指定孩子（用于取范围、写补录）。周报告与闪卡要，通用描红卡不强制。
	NeedsChild bool        `json:"needs_child"`
	Params     []ParamSpec `json:"params"`
}

func intp(v int) *int { return &v }

// 所有模板共用的范围参数。
func rangeParam() ParamSpec {
	return ParamSpec{
		Name: "range", Label: "取哪些内容", Type: "enum", Default: "recent",
		Options: []string{"recent", "wrong", "mastered", "stage"},
		Help:    "recent=近期新学 / wrong=错题本 / mastered=已掌握 / stage=按阶段挑",
	}
}

func subjectParam() ParamSpec {
	return ParamSpec{
		Name: "subject", Label: "学科", Type: "enum", Default: "chinese",
		Options: []string{"chinese", "english", "math"},
	}
}

func countParam(def, min, max int, label string) ParamSpec {
	return ParamSpec{Name: "count", Label: label, Type: "int", Default: def, Min: intp(min), Max: intp(max)}
}

func paperParam(def string) ParamSpec {
	return ParamSpec{Name: "paper", Label: "纸张", Type: "enum", Default: def,
		Options: []string{"A4", "A5"}}
}

func copiesParam() ParamSpec {
	return ParamSpec{Name: "copies", Label: "打印份数", Type: "int", Default: 1, Min: intp(1), Max: intp(20)}
}

func watermarkParam() ParamSpec {
	return ParamSpec{Name: "watermark", Label: "日期水印", Type: "bool", Default: true,
		Help: "页脚印上生成日期，方便归档"}
}

func answerParam(def bool) ParamSpec {
	return ParamSpec{Name: "with_answer", Label: "附答案页", Type: "bool", Default: def,
		Help: "答案单独成页，孩子那份可以不打印这一节"}
}

// catalog 是 10 套模板的注册表（§4.6 模板清单）。
//
// 顺序即家长端的展示顺序：先识字，再拼音/数学，再英语，最后故事与报告。
var catalog = []TemplateSpec{
	{
		Code: "hanzi_trace", Name: "汉字描红卡", Category: CategoryHanzi,
		Description: "田字格描红：淡字打底 + 空行练写，可带拼音与组词",
		PaperSize:   "A4", Answerable: true,
		Params: []ParamSpec{
			rangeParam(), subjectParam(),
			{Name: "stage", Label: "阶段", Type: "text", Default: "", Help: "range=stage 时生效，如 S2 / G3"},
			{Name: "days", Label: "近几天", Type: "int", Default: 7, Min: intp(1), Max: intp(90), Help: "range=recent 时生效"},
			countParam(12, 1, 60, "字数"),
			{Name: "per_page", Label: "每页格数", Type: "int", Default: 12, Min: intp(4), Max: intp(24)},
			{Name: "font_size", Label: "字号(pt)", Type: "int", Default: 48, Min: intp(24), Max: intp(96)},
			{Name: "with_pinyin", Label: "带拼音", Type: "bool", Default: true},
			answerParam(false), paperParam("A4"), copiesParam(), watermarkParam(),
		},
	},
	{
		Code: "hanzi_flash", Name: "汉字闪卡", Category: CategoryHanzi,
		Description: "双面闪卡：正面大字，背面拼音 + 组词，沿中线对折",
		PaperSize:   "A4", DoubleSided: true,
		Params: []ParamSpec{
			rangeParam(), subjectParam(),
			{Name: "stage", Label: "阶段", Type: "text", Default: ""},
			{Name: "days", Label: "近几天", Type: "int", Default: 7, Min: intp(1), Max: intp(90)},
			countParam(8, 1, 40, "卡片数"),
			{Name: "font_size", Label: "字号(pt)", Type: "int", Default: 72, Min: intp(36), Max: intp(160)},
			{Name: "with_pinyin", Label: "背面带拼音", Type: "bool", Default: true},
			paperParam("A4"), copiesParam(), watermarkParam(),
		},
	},
	{
		Code: "pinyin_grid", Name: "拼音四线三格", Category: CategoryPinyin,
		Description: "四线三格抄写纸，可带范字，练声母 / 韵母 / 整体认读",
		PaperSize:   "A4",
		Params: []ParamSpec{
			subjectParam(),
			{Name: "stage", Label: "阶段", Type: "text", Default: "E1"},
			countParam(12, 1, 40, "音节数"),
			{Name: "per_page", Label: "每页行数", Type: "int", Default: 12, Min: intp(4), Max: intp(20)},
			{Name: "font_size", Label: "字号(pt)", Type: "int", Default: 28, Min: intp(16), Max: intp(48)},
			paperParam("A4"), copiesParam(), watermarkParam(),
		},
	},
	{
		Code: "math_drill", Name: "口算题卡", Category: CategoryMath,
		Description: "参数化口算：40 / 60 / 100 题可选，与屏幕练习同一 seed 同源",
		PaperSize:   "A4", Answerable: true, NeedsChild: true,
		Params: []ParamSpec{
			{Name: "math_template", Label: "题型模板", Type: "text", Default: "M2_ADD10",
				Help: "如 M1_ADD5 / M2_SUB10 / M3_ADD20；与 /practice/math/templates 同源"},
			countParam(40, 10, 100, "题量"),
			{Name: "columns", Label: "分栏", Type: "int", Default: 4, Min: intp(1), Max: intp(6)},
			{Name: "seed", Label: "随机种子", Type: "int", Default: 0, Min: intp(0), Max: intp(2147483647),
				Help: "填固定值可复现同一套题；0 表示每次随机"},
			{Name: "blank_lines", Label: "留竖式空位", Type: "bool", Default: false},
			{Name: "font_size", Label: "字号(pt)", Type: "int", Default: 16, Min: intp(10), Max: intp(28)},
			answerParam(true), paperParam("A4"), copiesParam(), watermarkParam(),
		},
	},
	{
		Code: "math_compare", Name: "比大小·找规律", Category: CategoryMath,
		Description: "比较大小填空 + 数列找规律，训练数感",
		PaperSize:   "A4", Answerable: true, NeedsChild: true,
		Params: []ParamSpec{
			countParam(20, 5, 60, "题量"),
			{Name: "columns", Label: "分栏", Type: "int", Default: 3, Min: intp(1), Max: intp(4)},
			{Name: "seed", Label: "随机种子", Type: "int", Default: 0, Min: intp(0), Max: intp(2147483647)},
			{Name: "font_size", Label: "字号(pt)", Type: "int", Default: 18, Min: intp(10), Max: intp(32)},
			answerParam(true), paperParam("A4"), copiesParam(), watermarkParam(),
		},
	},
	{
		Code: "en_word_card", Name: "英语单词卡", Category: CategoryEnglish,
		Description: "双面单词卡：正面单词 + 音标，背面中文释义 + 例句",
		PaperSize:   "A4", DoubleSided: true,
		Params: []ParamSpec{
			rangeParam(),
			{Name: "stage", Label: "级别", Type: "text", Default: ""},
			{Name: "days", Label: "近几天", Type: "int", Default: 7, Min: intp(1), Max: intp(90)},
			countParam(8, 1, 40, "卡片数"),
			{Name: "font_size", Label: "字号(pt)", Type: "int", Default: 40, Min: intp(20), Max: intp(96)},
			paperParam("A4"), copiesParam(), watermarkParam(),
		},
	},
	{
		Code: "letter_trace", Name: "字母描红", Category: CategoryEnglish,
		Description: "A–Z 大小写描红，带四线三格基准线",
		PaperSize:   "A4",
		Params: []ParamSpec{
			{Name: "case", Label: "大小写", Type: "enum", Default: "both", Options: []string{"upper", "lower", "both"}},
			countParam(13, 1, 26, "字母数"),
			{Name: "per_page", Label: "每页行数", Type: "int", Default: 13, Min: intp(4), Max: intp(26)},
			{Name: "font_size", Label: "字号(pt)", Type: "int", Default: 40, Min: intp(20), Max: intp(80)},
			paperParam("A4"), copiesParam(), watermarkParam(),
		},
	},
	{
		Code: "match_lines", Name: "连线题", Category: CategoryHanzi,
		Description: "左列汉字 / 右列拼音或组词，画线连接",
		PaperSize:   "A4", Answerable: true, NeedsChild: true,
		Params: []ParamSpec{
			rangeParam(), subjectParam(),
			{Name: "stage", Label: "阶段", Type: "text", Default: ""},
			{Name: "days", Label: "近几天", Type: "int", Default: 7, Min: intp(1), Max: intp(90)},
			countParam(8, 4, 16, "组数"),
			{Name: "right_kind", Label: "右列内容", Type: "enum", Default: "pinyin", Options: []string{"pinyin", "word"}},
			{Name: "seed", Label: "随机种子", Type: "int", Default: 0, Min: intp(0), Max: intp(2147483647)},
			{Name: "font_size", Label: "字号(pt)", Type: "int", Default: 24, Min: intp(14), Max: intp(48)},
			answerParam(true), paperParam("A4"), copiesParam(), watermarkParam(),
		},
	},
	{
		Code: "story_booklet", Name: "故事小册子", Category: CategoryStory,
		Description: "A5 小册子：故事正文 + 亲子讨论题，适合朗读后讨论",
		PaperSize:   "A5", NeedsChild: true,
		Params: []ParamSpec{
			rangeParam(),
			{Name: "stage", Label: "级别", Type: "text", Default: ""},
			{Name: "story_id", Label: "指定故事 ID", Type: "text", Default: "", Help: "留空则按范围随机取一篇"},
			{Name: "font_size", Label: "字号(pt)", Type: "int", Default: 16, Min: intp(12), Max: intp(28)},
			{Name: "with_questions", Label: "附讨论题", Type: "bool", Default: true},
			paperParam("A5"), copiesParam(), watermarkParam(),
		},
	},
	{
		Code: "weekly_report", Name: "周学习报告", Category: CategoryReport,
		Description: "家长版周报：趋势、学科分布、薄弱项与下周建议",
		PaperSize:   "A4", NeedsChild: true,
		Params: []ParamSpec{
			{Name: "days", Label: "统计天数", Type: "int", Default: 7, Min: intp(7), Max: intp(90)},
			{Name: "font_size", Label: "字号(pt)", Type: "int", Default: 12, Min: intp(10), Max: intp(18)},
			paperParam("A4"), copiesParam(), watermarkParam(),
		},
	},
}

// Catalog 返回全部模板定义（按展示顺序）。
func Catalog() []TemplateSpec {
	out := make([]TemplateSpec, len(catalog))
	copy(out, catalog)
	return out
}

// SpecByCode 按 code 取模板定义。
func SpecByCode(code string) (TemplateSpec, bool) {
	for _, t := range catalog {
		if t.Code == code {
			return t, true
		}
	}
	return TemplateSpec{}, false
}

// CatalogCodes 返回全部模板 code（按展示顺序）。
func CatalogCodes() []string {
	out := make([]string, 0, len(catalog))
	for _, t := range catalog {
		out = append(out, t.Code)
	}
	return out
}

// ---------------------------------------------------------------- 参数

// Params 是 POST /print/jobs 的 params 字段（家长填的原始参数）。
type Params map[string]any

// Get 取字符串值，做类型宽容处理：JSON 数字与字符串都接受。
func (p Params) Get(key string) string {
	v, ok := p[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", t), "0"), ".")
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

// GetInt 取整数，缺失或非法时返回 def。JSON 里数字会是 float64，这里一并接受。
func (p Params) GetInt(key string, def int) int {
	v, ok := p[key]
	if !ok || v == nil {
		return def
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%d", &n); err == nil {
			return n
		}
	}
	return def
}

// GetBool 取布尔，缺失或非法时返回 def。
func (p Params) GetBool(key string, def bool) bool {
	v, ok := p[key]
	if !ok || v == nil {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	case float64:
		return t != 0
	}
	return def
}

// GetEnum 取枚举值，不在允许集合内则返回 def（大小写不敏感）。
func (p Params) GetEnum(key string, allowed []string, def string) string {
	got := strings.ToLower(p.Get(key))
	if got == "" {
		return def
	}
	for _, a := range allowed {
		if strings.ToLower(a) == got {
			return a
		}
	}
	return def
}

// sortedKeys 只用于把 payload.warnings 之类的输出稳定下来，便于冒烟脚本比对。
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
