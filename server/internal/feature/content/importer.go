package content

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Importer 是一次性内容导入器：把 var/ 下的采集与构建产物灌进 PostgreSQL。
//
// 铁律：
//   - 幂等。每个写入都带冲突键，重跑不会产生重复行，也不会覆盖家长已做的判定；
//   - 可反复重跑。内容迭代（补采、改分级）后直接再跑一次即可。
type Importer struct {
	repo     *Repository
	log      *slog.Logger
	dataRoot string
}

// NewImporter 构造导入器。dataRoot 指向仓库根的 var 目录（默认 ../var）。
func NewImporter(repo *Repository, log *slog.Logger, dataRoot string) *Importer {
	return &Importer{repo: repo, log: log, dataRoot: dataRoot}
}

// Report 导入结果计数，用于人工核对「字数对得上」。
type Report struct {
	Stages        int
	Hanzi         int
	HanziWords    int
	EnWords       int
	Stories       int
	StoryPairs    int
	ReviewItems   int
	PlanChildren  int
	PlanRows      int
	RefBackfilled bool
}

// importStep 是导入流程中的一步。
type importStep struct {
	key  string
	name string
	fn   func(context.Context, *Report) error
}

func (im *Importer) steps() []importStep {
	return []importStep{
		{"stages", "阶段字典", im.importStages},
		{"hanzi", "汉字与组词", im.importHanzi},
		{"words", "英语单词", im.importEnWords},
		{"stories", "故事与双语配对", im.importStories},
		{"refs", "知识点反向引用回填", im.backfillRefIDs},
		{"plans", "标准节奏基准线", im.importPlans},
	}
}

// Run 按依赖顺序跑完整导入流程。
func (im *Importer) Run(ctx context.Context) (Report, error) {
	return im.run(ctx, im.steps())
}

// RunOnly 只跑指定步骤（key 见 steps），供补数据与排障时使用。
func (im *Importer) RunOnly(ctx context.Context, key string) (Report, error) {
	for _, s := range im.steps() {
		if s.key == key {
			return im.run(ctx, []importStep{s})
		}
	}
	return Report{}, fmt.Errorf("未知的导入步骤 %q（可选 stages|hanzi|words|stories|plans）", key)
}

func (im *Importer) run(ctx context.Context, steps []importStep) (Report, error) {
	var rep Report
	for _, step := range steps {
		start := time.Now()
		if err := step.fn(ctx, &rep); err != nil {
			return rep, fmt.Errorf("导入「%s」失败: %w", step.name, err)
		}
		im.log.Info("导入步骤完成", "step", step.name, "duration_ms", time.Since(start).Milliseconds())
	}
	return rep, nil
}

// ------------------------------------------------------------------ 阶段字典

// stageDefs 是阶段元数据的事实来源：编码、学科、名称、顺序、目标量。
//
// 字表本身来自 var/raw/stages.json，这里只补人类可读的名称与配额。
var stageDefs = buildStageDefs()

func buildStageDefs() []Stage {
	defs := []Stage{
		{Code: "S0", SubjectCode: "chinese", Name: "数学常用字", TargetCount: 155},
		{Code: "S1", SubjectCode: "chinese", Name: "学前·身体家人", TargetCount: 200},
		{Code: "S2", SubjectCode: "chinese", Name: "学前·动物自然", TargetCount: 200},
		{Code: "S3", SubjectCode: "chinese", Name: "学前·家居衣物", TargetCount: 200},
		{Code: "S4", SubjectCode: "chinese", Name: "学前·动作情绪", TargetCount: 200},
		{Code: "S5", SubjectCode: "chinese", Name: "学前·生活场景", TargetCount: 200},
	}
	for i := 1; i <= 12; i++ {
		defs = append(defs, Stage{
			Code:        fmt.Sprintf("G%d", i),
			SubjectCode: "chinese",
			Name:        fmt.Sprintf("小学·%s", gradeLabel(i)),
			TargetCount: 250,
		})
	}
	for i := 1; i <= 6; i++ {
		defs = append(defs, Stage{
			Code:        fmt.Sprintf("X%d", i),
			SubjectCode: "chinese",
			Name:        fmt.Sprintf("拓展 %d（选学）", i),
			TargetCount: 658,
		})
	}

	enNames := []string{
		"字母与首音", "自然拼读 CVC", "视觉词·预备级", "主题名词 L1", "视觉词·一级",
		"动作词与二级视觉词", "主题名词 L2", "描述词与三级视觉词", "主题名词 L3 与时间", "句型与短文",
	}
	enTargets := []int{52, 60, 40, 90, 110, 100, 120, 110, 120, 130}
	for i := 1; i <= 10; i++ {
		defs = append(defs, Stage{
			Code:        fmt.Sprintf("E%d", i),
			SubjectCode: "english",
			Name:        enNames[i-1],
			TargetCount: enTargets[i-1],
		})
	}

	mathNames := []string{
		"5 以内加减与 2 项循环", "10 以内加减与 3 项循环", "20 以内进位退位",
		"100 以内整十与算式比较", "两位数加减与复合规律",
	}
	for i := 1; i <= 5; i++ {
		defs = append(defs, Stage{
			Code:        fmt.Sprintf("M%d", i),
			SubjectCode: "math",
			Name:        mathNames[i-1],
		})
	}

	// sort_order 按学科内部顺序编号
	counters := map[string]int{}
	for i := range defs {
		defs[i].SortOrder = counters[defs[i].SubjectCode]
		counters[defs[i].SubjectCode]++
	}
	return defs
}

func gradeLabel(n int) string {
	grade := (n + 1) / 2 // 1,1,2,2,3,3…
	term := "上"
	if n%2 == 0 {
		term = "下"
	}
	names := []string{"一", "二", "三", "四", "五", "六"}
	if grade >= 1 && grade <= len(names) {
		return names[grade-1] + "年级" + term
	}
	return fmt.Sprintf("%d 年级%s", grade, term)
}

// backfillRefIDs 回填 knowledge_points.ref_id。
//
// 导入顺序是先写内容行、再 upsert 知识点，所以「知识点 → 内容行」这一侧的引用
// 只能在两边都落地之后补。组卷要靠它 join 出读音/组词/释义，缺了会一条题都出不来。
//
// 幂等：只在 ref_id 与期望值不同时才更新，重跑导入不产生额外写入。
func (im *Importer) backfillRefIDs(ctx context.Context, rep *Report) error {
	statements := []struct {
		name string
		sql  string
	}{
		{"汉字", `UPDATE knowledge_points kp SET ref_id = h.id
                  FROM hanzi h
                  WHERE kp.kind = 'hanzi' AND kp.code = 'hanzi:' || h."char"
                    AND kp.ref_id IS DISTINCT FROM h.id`},
		{"英语词", `UPDATE knowledge_points kp SET ref_id = w.id
                    FROM en_words w
                    WHERE kp.kind = 'word' AND kp.code = 'word:' || w.word
                      AND kp.ref_id IS DISTINCT FROM w.id`},
	}
	for _, st := range statements {
		tag, err := im.repo.Pool().Exec(ctx, st.sql)
		if err != nil {
			return fmt.Errorf("回填%s知识点引用失败: %w", st.name, err)
		}
		im.log.Info("回填知识点引用", "kind", st.name, "rows", tag.RowsAffected())
	}
	rep.RefBackfilled = true
	return nil
}

func (im *Importer) importStages(ctx context.Context, rep *Report) error {
	db := im.repo.Pool()
	for _, st := range stageDefs {
		if _, err := db.Exec(ctx, `INSERT INTO stages (code, subject_code, name, sort_order, target_count)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (code) DO UPDATE SET
    subject_code = EXCLUDED.subject_code,
    name         = EXCLUDED.name,
    sort_order   = EXCLUDED.sort_order,
    target_count = EXCLUDED.target_count`,
			st.Code, st.SubjectCode, st.Name, st.SortOrder, st.TargetCount); err != nil {
			return fmt.Errorf("写入阶段 %s 失败: %w", st.Code, err)
		}
	}
	rep.Stages = len(stageDefs)
	return nil
}

// ------------------------------------------------------------------ 汉字与组词

// stageWordRecord 对应 var/raw/stage_words.jsonl 的一行。
type stageWordRecord struct {
	Char          string   `json:"char"`
	Stage         string   `json:"stage"`
	Pinyin        []string `json:"pinyin"`
	Radical       string   `json:"radical"`
	StrokeCount   string   `json:"stroke_count"`
	HasStrokeAnim bool     `json:"has_stroke_anim"`
	Words         []string `json:"words"`
}

func (im *Importer) importHanzi(ctx context.Context, rep *Report) error {
	// 1. 阶段顺序（stages.json 里每个阶段的数组顺序就是学习顺序）
	order, err := im.readStageOrder()
	if err != nil {
		return err
	}

	// 2. 字表明细
	records, err := im.readStageWords()
	if err != nil {
		return err
	}

	// 3. 释义（可选补充，来自 hanzi.jsonl）
	explanations, err := im.readHanziExplanations()
	if err != nil {
		return err
	}

	// 4. 笔顺字形（hwdata.tgz，只取本字表的字）
	wanted := make(map[string]bool, len(records))
	for _, r := range records {
		wanted[r.Char] = true
	}
	strokes, err := im.readStrokeData(wanted)
	if err != nil {
		return err
	}

	cols := []string{`"char"`, "pinyin", "radical", "stroke_count", "has_anim", "stroke_paths",
		"explanation", "stage_code", "order_in_stage", "source"}
	rows := make([][]any, 0, len(records))
	for _, r := range records {
		pos, ok := order[r.Char]
		stageCode := r.Stage
		orderInStage := 0
		if ok {
			stageCode = pos.stage
			orderInStage = pos.index
		}

		var stroke any
		if raw, ok := strokes[r.Char]; ok {
			stroke = string(raw)
		}
		rows = append(rows, []any{
			r.Char,
			encodeStringList(r.Pinyin),
			nullIfEmpty(r.Radical),
			parseStrokeCount(r.StrokeCount),
			stroke != nil,
			stroke,
			nullIfEmpty(explanations[r.Char]),
			nullIfEmpty(stageCode),
			orderInStage,
			"stage_words",
		})
	}

	if _, err := bulkUpsert(ctx, im.repo.Pool(), "hanzi", cols, rows,
		[]string{`"char"`},
		[]string{"pinyin", "radical", "stroke_count", "has_anim", "stroke_paths",
			"explanation", "stage_code", "order_in_stage"},
	); err != nil {
		return err
	}
	rep.Hanzi = len(rows)

	// 5. 知识点：每个字一条，掌握度的挂载点
	kpRows := make([][]any, 0, len(rows))
	for _, r := range records {
		pos, ok := order[r.Char]
		var stage any = nil
		if ok {
			stage = pos.stage
		}
		kpRows = append(kpRows, []any{
			"chinese", stage, "hanzi", "hanzi:" + r.Char, r.Char,
			difficultyByStroke(r.StrokeCount), `{"source":"stage_words"}`,
		})
	}
	kpCols := []string{"subject_code", "stage_code", "kind", "code", "name", "difficulty", "metadata"}
	if _, err := bulkUpsert(ctx, im.repo.Pool(), "knowledge_points", kpCols, kpRows,
		[]string{"code"},
		[]string{"subject_code", "stage_code", "kind", "name", "difficulty"},
	); err != nil {
		return err
	}

	// 6. 回填 hanzi.kp_id
	if _, err := im.repo.Pool().Exec(ctx, `
UPDATE hanzi h SET kp_id = kp.id
FROM knowledge_points kp
WHERE kp.code = 'hanzi:' || h."char" AND h.kp_id IS DISTINCT FROM kp.id`); err != nil {
		return fmt.Errorf("回填汉字知识点失败: %w", err)
	}

	// 7. 组词
	idByChar, err := im.hanziIDs(ctx)
	if err != nil {
		return err
	}
	wordCols := []string{"hanzi_id", "word", "sort_order"}
	wordRows := make([][]any, 0, len(records)*6)
	for _, r := range records {
		hid, ok := idByChar[r.Char]
		if !ok {
			continue
		}
		for i, w := range r.Words {
			wordRows = append(wordRows, []any{hid, strings.TrimSpace(w), i})
		}
	}
	if _, err := bulkUpsert(ctx, im.repo.Pool(), "hanzi_words", wordCols, wordRows,
		[]string{"hanzi_id", "word"}, []string{"sort_order"}); err != nil {
		return err
	}
	rep.HanziWords = len(wordRows)
	return nil
}

type charPos struct {
	stage string
	index int
}

func (im *Importer) readStageOrder() (map[string]charPos, error) {
	path := filepath.Join(im.dataRoot, "raw", "stages.json")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开阶段字表失败: %w", err)
	}
	defer f.Close()

	var raw map[string][]string
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析阶段字表失败: %w", err)
	}

	out := make(map[string]charPos)
	for stage, chars := range raw {
		for i, ch := range chars {
			out[ch] = charPos{stage: stage, index: i}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("阶段字表为空: %s", path)
	}
	return out, nil
}

func (im *Importer) readStageWords() ([]stageWordRecord, error) {
	path := filepath.Join(im.dataRoot, "raw", "stage_words.jsonl")
	records, err := readJSONLines[stageWordRecord](path)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("字表为空: %s", path)
	}
	return records, nil
}

type hanziRawRecord struct {
	Char        string `json:"char"`
	Explanation string `json:"explanation"`
}

func (im *Importer) readHanziExplanations() (map[string]string, error) {
	path := filepath.Join(im.dataRoot, "raw", "hanzi.jsonl")
	records, err := readJSONLines[hanziRawRecord](path)
	if err != nil {
		// 释义是可选补充：文件缺失不该阻塞导入
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := make(map[string]string, len(records))
	for _, r := range records {
		if strings.TrimSpace(r.Explanation) != "" {
			out[r.Char] = r.Explanation
		}
	}
	return out, nil
}

// readStrokeData 流式读取 hwdata.tgz，只保留字表需要的字。
func (im *Importer) readStrokeData(wanted map[string]bool) (map[string]json.RawMessage, error) {
	path := filepath.Join(im.dataRoot, "raw", "hwdata.tgz")
	f, err := os.Open(path)
	if err != nil {
		im.log.Warn("缺少笔顺数据包，导入将不带字形", "path", path, "error", err)
		return map[string]json.RawMessage{}, nil
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("解压笔顺数据包失败: %w", err)
	}
	defer gz.Close()

	out := make(map[string]json.RawMessage, len(wanted))
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取笔顺数据包失败: %w", err)
		}
		name := filepath.Base(hdr.Name)
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		char := strings.TrimSuffix(name, ".json")
		if len([]rune(char)) != 1 || !wanted[char] {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(tr, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("读取 %s 字形失败: %w", name, err)
		}
		out[char] = json.RawMessage(raw)
	}
	return out, nil
}

func (im *Importer) hanziIDs(ctx context.Context) (map[string]string, error) {
	rows, err := im.repo.Pool().Query(ctx, `SELECT id, "char" FROM hanzi`)
	if err != nil {
		return nil, fmt.Errorf("读取汉字 ID 失败: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string, 9000)
	for rows.Next() {
		var id, char string
		if err := rows.Scan(&id, &char); err != nil {
			return nil, err
		}
		out[char] = id
	}
	return out, rows.Err()
}

// ------------------------------------------------------------------ 英语单词

type enWordRecord struct {
	Word        string `json:"word"`
	Topic       string `json:"topic"`
	Phonetic    string `json:"phonetic"`
	Definition  string `json:"definition"`
	Translation string `json:"translation"`
	Pos         string `json:"pos"`
	Frq         string `json:"frq"`
}

func (im *Importer) importEnWords(ctx context.Context, rep *Report) error {
	path := filepath.Join(im.dataRoot, "raw", "words.jsonl")
	records, err := readJSONLines[enWordRecord](path)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return fmt.Errorf("英语词表为空: %s", path)
	}

	levels := assignEnglishLevels(records)

	cols := []string{"word", "topic", "phonetic", "pos", "meaning_zh", "definition_en",
		"example_en", "example_zh", "frq", "level_code", "source"}
	rows := make([][]any, 0, len(records))
	for _, r := range records {
		rows = append(rows, []any{
			r.Word,
			nullIfEmpty(r.Topic),
			nullIfEmpty(r.Phonetic),
			nullIfEmpty(r.Pos),
			nullIfEmpty(strings.TrimSpace(r.Translation)),
			nullIfEmpty(strings.TrimSpace(r.Definition)),
			nil, // 例句待 M3 生成
			nil,
			parseInt(r.Frq),
			nullIfEmpty(levels[r.Word]),
			"ecdict",
		})
	}
	if _, err := bulkUpsert(ctx, im.repo.Pool(), "en_words", cols, rows,
		[]string{"word"},
		[]string{"topic", "phonetic", "pos", "meaning_zh", "definition_en", "frq", "level_code"},
	); err != nil {
		return err
	}
	rep.EnWords = len(rows)

	// 知识点
	kpCols := []string{"subject_code", "stage_code", "kind", "code", "name", "metadata"}
	kpRows := make([][]any, 0, len(rows))
	for _, r := range records {
		kpRows = append(kpRows, []any{
			"english", nullIfEmpty(levels[r.Word]), "word", "word:" + r.Word, r.Word,
			`{"topic":"` + strings.ReplaceAll(r.Topic, `"`, "") + `"}`,
		})
	}
	if _, err := bulkUpsert(ctx, im.repo.Pool(), "knowledge_points", kpCols, kpRows,
		[]string{"code"}, []string{"subject_code", "stage_code", "kind", "name"},
	); err != nil {
		return err
	}

	if _, err := im.repo.Pool().Exec(ctx, `
UPDATE en_words w SET kp_id = kp.id
FROM knowledge_points kp
WHERE kp.code = 'word:' || w.word AND w.kp_id IS DISTINCT FROM kp.id`); err != nil {
		return fmt.Errorf("回填英语词知识点失败: %w", err)
	}
	return nil
}

// topicBand 把 ECDICT 的主题映射到英语级别（见《内容与分级体系设计》§4）。
var topicBand = map[string]string{
	"little_words": "E3",
	"animals":      "E4",
	"colors":       "E4",
	"numbers":      "E4",
	"food":         "E4",
	"family":       "E5",
	"body":         "E5",
	"actions":      "E6",
	"school":       "E7",
	"home":         "E7",
	"clothes":      "E7",
	"weather":      "E7",
	"transport":    "E7",
	"describing":   "E8",
	"nature":       "E9",
	"time":         "E9",
}

// assignEnglishLevels 给每个词定级。
//
// 规则（启发式，可在家长后台调整）：
//  1. 有明确主题的词按主题表归级；
//  2. high_frequency 池按词频升序均匀切到 E4–E9 六档 —— 越常用越靠前。
func assignEnglishLevels(records []enWordRecord) map[string]string {
	out := make(map[string]string, len(records))

	var highFreq []string
	for _, r := range records {
		if band, ok := topicBand[r.Topic]; ok {
			out[r.Word] = band
			continue
		}
		highFreq = append(highFreq, r.Word)
	}
	if len(highFreq) == 0 {
		return out
	}

	// records 已是词频升序（越靠前越常用），按顺序均分到 E4–E9
	bands := []string{"E4", "E5", "E6", "E7", "E8", "E9"}
	n := len(highFreq)
	for i, w := range highFreq {
		idx := i * len(bands) / n
		if idx >= len(bands) {
			idx = len(bands) - 1
		}
		out[w] = bands[idx]
	}
	return out
}

// ------------------------------------------------------------------ 工具

// readJSONLines 逐行读取 JSONL。
func readJSONLines[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 %s 失败: %w", filepath.Base(path), err)
	}
	defer f.Close()

	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var item T
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", filepath.Base(path), err)
		}
		out = append(out, item)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", filepath.Base(path), err)
	}
	return out, nil
}

func encodeStringList(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func parseInt(s string) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return v
}

func parseStrokeCount(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return parseInt(s)
}

// difficultyByStroke 用笔画数粗分难度 1–5，供后续自适应升降档作起点。
func difficultyByStroke(strokeCount string) int {
	n := parseInt(strokeCount)
	switch {
	case n == 0:
		return 1
	case n <= 4:
		return 1
	case n <= 6:
		return 2
	case n <= 9:
		return 3
	case n <= 12:
		return 4
	default:
		return 5
	}
}
