package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ------------------------------------------------------------------ 故事导入

// storyAnalysis 对应 var/stories/_analysis.jsonl 与 var/beddy/_analysis.jsonl 的共有字段。
type storyAnalysis struct {
	File      string   `json:"file"`
	Lang      string   `json:"lang"`
	Cat       string   `json:"cat"`
	Category  string   `json:"category"`
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Slug      string   `json:"slug"`
	Age       string   `json:"age"`
	SourceURL string   `json:"source_url"`
	Chars     int      `json:"chars"`
	CJK       int      `json:"cjk"`
	Words     int      `json:"words"`
	Level     *string  `json:"level"`
	Advice    string   `json:"advice"`
	Unsafe    []string `json:"unsafe"`
	Caution   []string `json:"caution"`
}

// pairRecord 对应 var/beddy/_pairs.jsonl。
type pairRecord struct {
	Slug   string `json:"slug"`
	Age    string `json:"age"`
	ZHFile string `json:"zh_file"`
	ENFile string `json:"en_file"`
}

var enLevelRe = regexp.MustCompile(`E(\d+)`)

// storyColumns 与下方 rows 的顺序严格一致。
var storyColumns = []string{
	"lang", "title", "summary", "body_md", "category", "age_group", "level_code",
	"char_count", "word_count", "cover_url", "images", "new_chars", "questions",
	"discussion", "unsafe_hits", "suitable", "content_hash", "source", "source_ref",
	"source_url", "license", "status",
}

func (im *Importer) importStories(ctx context.Context, rep *Report) error {
	// 中文故事（一故事网，17,250 篇）
	if err := im.importYigushiStories(ctx, rep); err != nil {
		return err
	}
	// 中英双语（SleepyStory，1,663 篇）
	if err := im.importBeddyStories(ctx, rep); err != nil {
		return err
	}
	// 双语配对
	if err := im.importStoryPairs(ctx, rep); err != nil {
		return err
	}
	// 审核队列
	return im.importReviewQueue(ctx, rep)
}

func (im *Importer) importYigushiStories(ctx context.Context, rep *Report) error {
	dir := filepath.Join(im.dataRoot, "stories")
	analysis, err := readJSONLines[storyAnalysis](filepath.Join(dir, "_analysis.jsonl"))
	if err != nil {
		return err
	}

	rows := make([][]any, 0, len(analysis))
	for _, a := range analysis {
		body, meta, err := readStoryFile(filepath.Join(dir, filepath.FromSlash(a.File)))
		if err != nil {
			return err
		}

		level := ""
		if a.Level != nil {
			level = *a.Level
		}
		rows = append(rows, []any{
			"zh",
			firstNonEmpty(a.Title, meta["title"]),
			nullIfEmpty(summarize(body, 60)),
			body,
			nullIfEmpty(firstNonEmpty(a.Cat, meta["category"])),
			nil,
			nullIfEmpty(level),
			a.Chars,
			0,
			nil,
			"[]",
			"[]",
			"[]",
			"[]",
			encodeStringList(a.Unsafe),
			len(a.Unsafe) == 0,
			hashText(body),
			"yigushi",
			a.File,
			nullIfEmpty(firstNonEmpty(a.SourceURL, meta["source_url"])),
			nullIfEmpty(meta["license"]),
			"pending",
		})
	}

	if _, err := bulkUpsert(ctx, im.repo.Pool(), "stories", storyColumns, rows,
		[]string{"content_hash"},
		[]string{"title", "summary", "body_md", "category", "level_code", "char_count",
			"unsafe_hits", "suitable", "source_url", "license"},
	); err != nil {
		return err
	}

	// 中文故事可能与其他来源撞 content_hash（同文转载），这里按实际入库数统计
	rep.Stories += len(rows)
	im.log.Info("中文故事导入完成", "source", "yigushi", "files", len(rows))
	return nil
}

func (im *Importer) importBeddyStories(ctx context.Context, rep *Report) error {
	dir := filepath.Join(im.dataRoot, "beddy")
	analysis, err := readJSONLines[storyAnalysis](filepath.Join(dir, "_analysis.jsonl"))
	if err != nil {
		return err
	}

	rows := make([][]any, 0, len(analysis))
	for _, a := range analysis {
		body, meta, err := readStoryFile(filepath.Join(dir, filepath.FromSlash(a.File)))
		if err != nil {
			return err
		}

		lang := firstNonEmpty(a.Lang, meta["lang"])
		level := ""
		switch lang {
		case "zh":
			if a.Level != nil {
				level = *a.Level
			}
		default:
			// 英文篇没有中文字表分级，分析脚本给出的建议区间即 E 级
			if m := enLevelRe.FindStringSubmatch(a.Advice); len(m) > 1 {
				level = "E" + m[1]
			}
		}

		charCount := a.CJK
		if charCount == 0 {
			charCount = a.Chars
		}
		rows = append(rows, []any{
			lang,
			firstNonEmpty(a.Title, meta["title"]),
			nullIfEmpty(firstNonEmpty(meta["summary"], summarize(body, 60))),
			body,
			nullIfEmpty(firstNonEmpty(a.Category, a.Cat, meta["type"])),
			nullIfEmpty(firstNonEmpty(a.Age, meta["age_group"])),
			nullIfEmpty(level),
			charCount,
			a.Words,
			nullIfEmpty(meta["cover"]),
			encodeStringList(parseListMeta(meta["images"])),
			"[]",
			"[]",
			"[]",
			"[]",
			true, // 站方已按儿童读物筛过，暂不做不宜词二次判定（待补）
			hashText(body),
			firstNonEmpty(meta["source"], "sleepystory"),
			a.File,
			nullIfEmpty(firstNonEmpty(a.SourceURL, meta["source_url"])),
			nullIfEmpty(meta["license"]),
			"pending",
		})
	}

	if _, err := bulkUpsert(ctx, im.repo.Pool(), "stories", storyColumns, rows,
		[]string{"content_hash"},
		[]string{"title", "summary", "body_md", "category", "age_group", "level_code",
			"char_count", "word_count", "cover_url", "images", "source_url", "license"},
	); err != nil {
		return err
	}

	rep.Stories += len(rows)
	im.log.Info("双语故事导入完成", "source", "sleepystory", "files", len(rows))
	return nil
}

func (im *Importer) importStoryPairs(ctx context.Context, rep *Report) error {
	dir := filepath.Join(im.dataRoot, "beddy")
	pairs, err := readJSONLines[pairRecord](filepath.Join(dir, "_pairs.jsonl"))
	if err != nil {
		return err
	}

	idByRef, err := im.storyIDsByRef(ctx, "sleepystory")
	if err != nil {
		return err
	}

	cols := []string{"slug", "zh_story_id", "en_story_id", "age_group"}
	rows := make([][]any, 0, len(pairs))
	for _, p := range pairs {
		zhID, okZH := idByRef[p.ZHFile]
		enID, okEN := idByRef[p.ENFile]
		if !okZH || !okEN {
			im.log.Warn("双语配对缺少故事，已跳过", "slug", p.Slug, "zh", okZH, "en", okEN)
			continue
		}
		rows = append(rows, []any{p.Slug, zhID, enID, nullIfEmpty(p.Age)})
	}

	written, err := bulkUpsert(ctx, im.repo.Pool(), "story_pairs", cols, rows,
		[]string{"slug"}, []string{"zh_story_id", "en_story_id", "age_group"})
	if err != nil {
		return err
	}
	rep.StoryPairs = written
	return nil
}

// importReviewQueue 把所有待审故事推进审核队列。
//
// 直接以 stories 表为准生成队列行，而不是导入时内存里的数据 —— 这样重跑导入
// 也不会漏掉任何一篇（含此前已存在但尚未入队的内容）。
func (im *Importer) importReviewQueue(ctx context.Context, rep *Report) error {
	rows, err := im.repo.Pool().Query(ctx, `
SELECT id, title, summary, source, source_url, unsafe_hits
FROM stories WHERE status = 'pending'`)
	if err != nil {
		return fmt.Errorf("读取待审故事失败: %w", err)
	}
	defer rows.Close()

	cols := []string{"content_type", "ref_table", "ref_id", "title", "summary", "source", "source_url", "hits"}
	var data [][]any
	for rows.Next() {
		var (
			id, title, source  string
			summary, url, hits *string
		)
		if err := rows.Scan(&id, &title, &summary, &source, &url, &hits); err != nil {
			return err
		}
		data = append(data, []any{
			"story", "stories", id, title, summary, source, url, firstNonEmpty(deref(hits), "[]"),
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	written, err := bulkUpsert(ctx, im.repo.Pool(), "content_review", cols, data,
		[]string{"ref_table", "ref_id"}, nil)
	if err != nil {
		return err
	}
	rep.ReviewItems = written
	return nil
}

func (im *Importer) storyIDsByRef(ctx context.Context, source string) (map[string]string, error) {
	rows, err := im.repo.Pool().Query(ctx, `SELECT id, source_ref FROM stories WHERE source = $1`, source)
	if err != nil {
		return nil, fmt.Errorf("读取故事 ID 失败: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var id string
		var ref *string
		if err := rows.Scan(&id, &ref); err != nil {
			return nil, err
		}
		if ref != nil {
			out[*ref] = id
		}
	}
	return out, rows.Err()
}

// ------------------------------------------------------------------ 文件解析

// readStoryFile 读取故事 Markdown，返回正文（不含 frontmatter）与元数据。
func readStoryFile(path string) (string, map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("读取故事文件失败: %w", err)
	}
	meta, body := parseFrontmatter(string(raw))
	return strings.TrimSpace(body), meta, nil
}

// parseFrontmatter 解析文件头部的 `---` 区块。
//
// 自己写而不引 YAML 库，是因为这里的格式是我们自己产出的、极简且固定：
//   - 每行 `key: value`，值可能带引号，可能以 `#` 结尾写注释；
//   - 列表写成 `key:` 后跟若干 `  - "值"` 行，或直接 `[]`。
func parseFrontmatter(content string) (map[string]string, string) {
	meta := map[string]string{}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return meta, content
	}

	end := -1
	var listKey string
	var list []string
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			end = i
			break
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") && listKey != "" {
			list = append(list, unquote(strings.TrimSpace(trimmed[2:])))
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if listKey != "" {
			meta[listKey] = strings.Join(list, "\n")
			listKey, list = "", nil
		}
		key = strings.TrimSpace(key)
		val = cleanValue(val)
		if val == "" {
			listKey = key // 可能是列表头
			continue
		}
		meta[key] = unquote(val)
	}
	if listKey != "" && len(list) > 0 {
		meta[listKey] = strings.Join(list, "\n")
	}
	if end < 0 {
		return meta, content
	}
	return meta, strings.Join(lines[end+1:], "\n")
}

// cleanValue 去掉行尾注释与首尾空白；带引号的值原样保留到闭引号。
func cleanValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if v[0] == '"' || v[0] == '\'' {
		quote := v[0]
		if idx := strings.IndexByte(v[1:], quote); idx >= 0 {
			return v[:idx+2]
		}
		return v
	}
	if idx := strings.IndexByte(v, '#'); idx >= 0 {
		v = v[:idx]
	}
	return strings.TrimSpace(v)
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// parseListMeta 把 frontmatter 里多行拼接的列表拆回切片；`[]` 或空值得到空切片。
func parseListMeta(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" || v == "[]" {
		return nil
	}
	parts := strings.Split(v, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// summarize 取正文开头若干字符作为摘要（采集内容本身大多没有摘要）。
func summarize(body string, maxRunes int) string {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\n", ""))
	body = strings.TrimSpace(strings.ReplaceAll(body, "#", ""))
	runes := []rune(body)
	if len(runes) <= maxRunes {
		return body
	}
	return string(runes[:maxRunes]) + "…"
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// jsonString 用于拼装简单 JSON 字面量（导入期的少量固定结构）。
func jsonString(v map[string]string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
