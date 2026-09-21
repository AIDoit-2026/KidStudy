package print

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"kidstudy/internal/feature/content"
	"kidstudy/internal/pkg/mathgen"
	"kidstudy/internal/platform/apperr"
)

// 排版容量常量：用来推算「每页放得下多少」，进而估算页数与打印成本。
//
// 这些数字来自 print.css 里的块高（田字格卡片约 40mm、题目行距约 12mm）与
// A4/A5 扣除 15mm/12mm 页边距后的可用高度。它们只影响**预估值**，
// 真实页数由 PDF 渲染完成后统计（见 pdfPageCount），所以调模板样式时
// 这里略微失准不会出错，但最好一起改。
const (
	rowsPerPageA4 = 20
	rowsPerPageA5 = 14
	cardsPerPage  = 8 // 双列卡片，一页 4 行
)

func rowsPerPage(paper string) int {
	if paper == "A5" {
		return rowsPerPageA5
	}
	return rowsPerPageA4
}

// buildPayload 按模板分派数据生成。
func (s *Service) buildPayload(ctx context.Context, spec TemplateSpec, p Params,
	child Child, hasChild bool) (Payload, error) {
	childID := uuid.Nil
	if hasChild {
		childID = child.ID
	}

	switch spec.Code {
	case "hanzi_trace":
		return s.buildHanziTrace(ctx, spec, p, child, childID)
	case "hanzi_flash":
		return s.buildHanziFlash(ctx, spec, p, child, childID)
	case "pinyin_grid":
		return s.buildPinyinGrid(ctx, spec, p, child, childID)
	case "math_drill":
		return s.buildMathDrill(ctx, spec, p, child)
	case "math_compare":
		return s.buildMathCompare(ctx, spec, p, child)
	case "en_word_card":
		return s.buildEnWordCard(ctx, spec, p, child, childID)
	case "letter_trace":
		return s.buildLetterTrace(ctx, spec, p, child)
	case "match_lines":
		return s.buildMatchLines(ctx, spec, p, child, childID)
	case "story_booklet":
		return s.buildStoryBooklet(ctx, spec, p, child, childID)
	case "weekly_report":
		return s.buildWeeklyReport(ctx, spec, p, child, childID)
	default:
		return Payload{}, apperr.Internal(fmt.Errorf("模板 %s 没有对应的数据生成器", spec.Code))
	}
}

// ---------------------------------------------------------------- 汉字类

// 描红卡与闪卡共用取字逻辑：范围 → kp → 素材 → 按需裁剪。
func (s *Service) loadHanzi(ctx context.Context, p Params, childID uuid.UUID, want int) ([]content.HanziMaterial, string, error) {
	kps, source, err := s.resolveKPs(ctx, p, childID, want, want*4)
	if err != nil {
		return nil, "", err
	}
	mats, err := s.content.LoadMaterials(ctx, kps)
	if err != nil {
		return nil, "", apperr.Internal(fmt.Errorf("装载素材失败: %w", err))
	}
	out := make([]content.HanziMaterial, 0, want)
	for _, kp := range kps {
		m, ok := mats.Hanzi[kp]
		if !ok {
			// 这条 kp 不是汉字（比如同阶段混进了别的 kind），跳过
			continue
		}
		out = append(out, m)
		if len(out) >= want {
			break
		}
	}
	return out, source, nil
}

func (s *Service) buildHanziTrace(ctx context.Context, spec TemplateSpec, p Params,
	child Child, childID uuid.UUID) (Payload, error) {
	count := p.GetInt("count", 12)
	words, source, err := s.loadHanzi(ctx, p, childID, count)
	if err != nil {
		return Payload{}, err
	}
	meta := s.meta(spec, p, child, source)
	payload := s.newPayload(spec, p, meta, "汉字描红卡", "先描后写，每个字写三遍")
	for i, m := range words {
		it := Item{
			Seq:  i + 1,
			Main: m.Char,
			Sub:  pinyinText(m.Pinyin),
			KpID: m.KPID.String(),
		}
		if len(m.Words) > 0 {
			n := 2
			if len(m.Words) < n {
				n = len(m.Words)
			}
			it.Extra = m.Words[:n]
		}
		payload.Items = append(payload.Items, it)
	}
	if len(payload.Items) == 0 {
		payload.Warnings = append(payload.Warnings, "没有取到可描红的汉字，换个范围或先让孩子学几个字")
	}
	return payload, nil
}

func (s *Service) buildHanziFlash(ctx context.Context, spec TemplateSpec, p Params,
	child Child, childID uuid.UUID) (Payload, error) {
	count := p.GetInt("count", 8)
	words, source, err := s.loadHanzi(ctx, p, childID, count)
	if err != nil {
		return Payload{}, err
	}
	meta := s.meta(spec, p, child, source)
	payload := s.newPayload(spec, p, meta, "汉字闪卡", "正面认读，背面核对")
	for i, m := range words {
		payload.Items = append(payload.Items, Item{
			Seq:  i + 1,
			Main: m.Char,
			KpID: m.KPID.String(),
		})
		back := Item{
			Seq:  i + 1,
			Main: m.Char,
			Sub:  pinyinText(m.Pinyin),
			KpID: m.KPID.String(),
		}
		if len(m.Words) > 0 {
			n := 3
			if len(m.Words) < n {
				n = len(m.Words)
			}
			back.Extra = m.Words[:n]
		}
		payload.AnswerItems = append(payload.AnswerItems, back)
	}
	if len(payload.Items) == 0 {
		payload.Warnings = append(payload.Warnings, "没有取到卡片内容")
	}
	payload.Options.PerPage = cardsPerPage
	return payload, nil
}

// 拼音四线三格：从指定阶段的汉字里提取不同的音节，去重后按序输出。
//
// 不是「声母表」而是「这一阶段会遇到的音节」—— 家长要练的是孩子正在学的字，
// 脱离进度给一张通用声母表对不上孩子的课本。
func (s *Service) buildPinyinGrid(ctx context.Context, spec TemplateSpec, p Params,
	child Child, childID uuid.UUID) (Payload, error) {
	count := p.GetInt("count", 12)
	stage := p.Get("stage")
	if stage == "" {
		stage = child.StageCode
	}
	if stage == "" {
		stage = "S1" // 兜底：学前第一阶，字都是最基础的
	}
	// 拼音格是按阶段取内容，不走「近期新学 / 错题本」那套需要孩子的范围
	p["range"] = "stage"
	p["subject"] = "chinese"
	p["stage"] = stage

	words, source, err := s.loadHanzi(ctx, p, childID, count*6)
	if err != nil {
		return Payload{}, err
	}

	seen := map[string]struct{}{}
	syllables := make([]string, 0, count)
	for _, m := range words {
		for _, py := range m.Pinyin {
			py = strings.TrimSpace(py)
			if py == "" {
				continue
			}
			if _, dup := seen[py]; dup {
				continue
			}
			seen[py] = struct{}{}
			syllables = append(syllables, py)
			if len(syllables) >= count {
				break
			}
		}
		if len(syllables) >= count {
			break
		}
	}

	meta := s.meta(spec, p, child, source+" · 音节")
	payload := s.newPayload(spec, p, meta, "拼音四线三格", "上格描一遍，下格自己写")
	for i, sy := range syllables {
		payload.Items = append(payload.Items, Item{Seq: i + 1, Main: sy})
	}
	if len(payload.Items) == 0 {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("阶段 %s 没有找到带拼音的汉字", stage))
	}
	payload.Options.PerPage = p.GetInt("per_page", 12)
	return payload, nil
}

// ---------------------------------------------------------------- 数学类

func (s *Service) buildMathDrill(ctx context.Context, spec TemplateSpec, p Params, child Child) (Payload, error) {
	code := strings.TrimSpace(p.Get("math_template"))
	if code == "" {
		code = "M2_ADD10"
	}
	cfg, err := s.mathConfigFor(ctx, code)
	if err != nil {
		return Payload{}, err
	}
	seed := effectiveSeed(p)
	count := p.GetInt("count", 40)
	questions := mathgen.Generate(cfg, seed, count)

	kpID, found, err := s.repo.MathKPByTemplate(ctx, code)
	if err != nil {
		return Payload{}, apperr.Internal(err)
	}
	kpStr := ""
	if found {
		kpStr = kpID.String()
	}

	meta := s.meta(spec, p, child, fmt.Sprintf("题型 %s · %d 题 · 种子 %d", code, count, seed))
	payload := s.newPayload(spec, p, meta, "口算题卡", "")
	payload.Options.PerPage = payload.Options.Columns * rowsPerPage(meta.Paper)
	for i, q := range questions {
		payload.Items = append(payload.Items, Item{
			Seq: i + 1, Main: q.Prompt, Answer: q.Answer, Hint: q.Explain, KpID: kpStr,
		})
	}
	if payload.Options.WithAnswer {
		for i, q := range questions {
			payload.AnswerItems = append(payload.AnswerItems, Item{Seq: i + 1, Main: q.Prompt, Answer: q.Answer, KpID: kpStr})
		}
	}
	if len(payload.Items) == 0 {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("题型 %s 没有生成出题目", code))
	}
	if !found {
		payload.Warnings = append(payload.Warnings, "这个题型还没有挂知识点，补录无法计入进度")
	}
	return payload, nil
}

// 比大小·找规律：一半比较大小、一半找规律，各出一半题量。
//
// 比大小走 M3_CMP20（与屏幕练习同一张模板表，同 seed 同题）；
// 找规律是打印件特有的题型，直接给 mathgen 一个 seq 配置。
func (s *Service) buildMathCompare(ctx context.Context, spec TemplateSpec, p Params, child Child) (Payload, error) {
	seed := effectiveSeed(p)
	count := p.GetInt("count", 20)

	cmpCfg, err := s.mathConfigFor(ctx, "M3_CMP20")
	if err != nil {
		return Payload{}, err
	}
	cmpCount := (count + 1) / 2
	seqCount := count - cmpCount

	items := make([]Item, 0, count)
	for _, q := range mathgen.Generate(cmpCfg, seed, cmpCount) {
		items = append(items, Item{Main: q.Prompt, Answer: q.Answer, Hint: q.Explain, Style: "cmp"})
	}
	seqCfg := mathgen.Config{Op: "seq", Min: 0, Max: 100, Terms: 2}
	for _, q := range mathgen.Generate(seqCfg, seed+1, seqCount) {
		items = append(items, Item{Main: q.Prompt, Answer: q.Answer, Hint: q.Explain, Style: "seq"})
	}

	// 挂到比大小的知识点上，补录才会计入进度
	kpStr := ""
	if kpID, found, err := s.repo.MathKPByTemplate(ctx, "M3_CMP20"); err != nil {
		return Payload{}, apperr.Internal(err)
	} else if found {
		kpStr = kpID.String()
	}

	meta := s.meta(spec, p, child, fmt.Sprintf("%d 题 · 种子 %d", count, seed))
	payload := s.newPayload(spec, p, meta, "比大小 · 找规律", "")
	payload.Options.PerPage = payload.Options.Columns * rowsPerPage(meta.Paper)
	for i := range items {
		items[i].Seq = i + 1
		items[i].KpID = kpStr
	}
	payload.Items = items
	if payload.Options.WithAnswer {
		for _, it := range items {
			payload.AnswerItems = append(payload.AnswerItems, Item{Seq: it.Seq, Main: it.Main, Answer: it.Answer, KpID: kpStr})
		}
	}
	if len(payload.Items) == 0 {
		payload.Warnings = append(payload.Warnings, "没有生成出题目")
	}
	return payload, nil
}

// ---------------------------------------------------------------- 英语类

func (s *Service) buildEnWordCard(ctx context.Context, spec TemplateSpec, p Params,
	child Child, childID uuid.UUID) (Payload, error) {
	count := p.GetInt("count", 8)
	kps, source, err := s.resolveKPs(ctx, p, childID, count, count*4)
	if err != nil {
		return Payload{}, err
	}
	mats, err := s.content.LoadMaterials(ctx, kps)
	if err != nil {
		return Payload{}, apperr.Internal(fmt.Errorf("装载素材失败: %w", err))
	}

	meta := s.meta(spec, p, child, source)
	payload := s.newPayload(spec, p, meta, "英语单词卡", "正面读词，背面看意思")
	payload.Options.PerPage = cardsPerPage
	for _, kp := range kps {
		m, ok := mats.Words[kp]
		if !ok {
			continue
		}
		if len(payload.Items) >= count {
			break
		}
		payload.Items = append(payload.Items, Item{
			Seq: len(payload.Items) + 1, Main: m.Word, Sub: m.Phonetic, KpID: m.KPID.String(),
		})
		back := Item{
			Seq: len(payload.Items), Main: m.Word, Sub: m.MeaningZh, KpID: m.KPID.String(),
		}
		if m.ExampleEn != "" {
			back.Extra = append(back.Extra, m.ExampleEn)
		}
		if m.ExampleZh != "" {
			back.Extra = append(back.Extra, m.ExampleZh)
		}
		payload.AnswerItems = append(payload.AnswerItems, back)
	}
	if len(payload.Items) == 0 {
		payload.Warnings = append(payload.Warnings, "没有取到单词，换个范围或先让孩子学几个词")
	}
	return payload, nil
}

func (s *Service) buildLetterTrace(ctx context.Context, spec TemplateSpec, p Params, child Child) (Payload, error) {
	count := p.GetInt("count", 13)
	switch p.GetEnum("case", []string{"upper", "lower", "both"}, "both") {
	case "upper":
		count = minInt(count*2, len(upperLetters))
		return s.letterPayload(spec, p, child, "字母描红（大写）", upperLetters[:count]), nil
	case "lower":
		count = minInt(count*2, len(upperLetters))
		return s.letterPayload(spec, p, child, "字母描红（小写）", lowerLetters()[:count]), nil
	default:
		letters := make([]string, 0, len(upperLetters)*2)
		// 大小写交替出现，孩子一眼能看出对应关系
		lower := lowerLetters()
		for i := range upperLetters {
			letters = append(letters, upperLetters[i], lower[i])
			if len(letters) >= count {
				break
			}
		}
		return s.letterPayload(spec, p, child, "字母描红", letters), nil
	}
}

func (s *Service) letterPayload(spec TemplateSpec, p Params, child Child, title string, letters []string) Payload {
	meta := s.meta(spec, p, child, fmt.Sprintf("%d 个字母", len(letters)))
	payload := s.newPayload(spec, p, meta, title, "上格描一遍，下格自己写")
	for i, l := range letters {
		payload.Items = append(payload.Items, Item{Seq: i + 1, Main: l})
	}
	payload.Options.PerPage = p.GetInt("per_page", 13)
	return payload
}

// ---------------------------------------------------------------- 连线题

func (s *Service) buildMatchLines(ctx context.Context, spec TemplateSpec, p Params,
	child Child, childID uuid.UUID) (Payload, error) {
	count := p.GetInt("count", 8)
	seed := effectiveSeed(p)

	words, source, err := s.loadHanzi(ctx, p, childID, count)
	if err != nil {
		return Payload{}, err
	}
	if len(words) < 4 {
		return Payload{}, apperr.BadRequest("可取的内容不足 4 组，换个范围再试")
	}

	rightKind := p.GetEnum("right_kind", []string{"pinyin", "word"}, "pinyin")
	type pair struct{ left, right, kp string }
	pairs := make([]pair, 0, len(words))
	for _, m := range words {
		right := ""
		switch rightKind {
		case "word":
			if len(m.Words) > 0 {
				right = m.Words[0]
			}
		default:
			right = pinyinText(m.Pinyin)
		}
		if right == "" {
			continue
		}
		pairs = append(pairs, pair{left: m.Char, right: right, kp: m.KPID.String()})
	}
	if len(pairs) < 4 {
		return Payload{}, apperr.BadRequest("可配对的内容不足 4 组，换个范围再试")
	}

	// 右列打乱，但同一 seed 必须复现（家长可能想再印一份同样的）
	rights := make([]string, 0, len(pairs))
	for _, pr := range pairs {
		rights = append(rights, pr.right)
	}
	shuffled := shuffleBySeed(rights, seed)

	label := "拼音"
	if rightKind == "word" {
		label = "组词"
	}
	meta := s.meta(spec, p, child, fmt.Sprintf("%s · %d 组 · 种子 %d", source, len(pairs), seed))
	payload := s.newPayload(spec, p, meta, "连线题", "把汉字和对应的"+label+"连起来")
	payload.Options.PerPage = rowsPerPage(meta.Paper)
	for i, pr := range pairs {
		it := Item{Seq: i + 1, Main: pr.left, Sub: shuffled[i], KpID: pr.kp}
		it.Answer = pr.right
		payload.Items = append(payload.Items, it)
		if payload.Options.WithAnswer {
			payload.AnswerItems = append(payload.AnswerItems,
				Item{Seq: i + 1, Main: pr.left, Answer: pr.right, KpID: pr.kp})
		}
	}
	return payload, nil
}

// ---------------------------------------------------------------- 故事小册子

func (s *Service) buildStoryBooklet(ctx context.Context, spec TemplateSpec, p Params,
	child Child, childID uuid.UUID) (Payload, error) {
	stage := p.Get("stage")
	if stage == "" {
		stage = child.StageCode
	}

	var (
		story    Story
		notFound bool
		err      error
	)
	if raw := strings.TrimSpace(p.Get("story_id")); raw != "" {
		id, perr := uuid.Parse(raw)
		if perr != nil {
			return Payload{}, apperr.BadRequest("故事 ID 格式不正确")
		}
		story, notFound, err = s.repo.GetStory(ctx, id)
	} else {
		story, notFound, err = s.repo.PickStory(ctx, stage)
	}
	if err != nil {
		return Payload{}, apperr.Internal(err)
	}

	meta := s.meta(spec, p, child, stage)
	payload := s.newPayload(spec, p, meta, "故事小册子", "")
	if notFound {
		payload.Warnings = append(payload.Warnings,
			"没有找到已发布的故事。故事默认需要家长在审核队列里通过后才会进入打印中心。")
		return payload, nil
	}

	payload.Title = story.Title
	payload.Meta.Source = fmt.Sprintf("级别 %s · %d 字", story.LevelCode, story.CharCount)
	paras := paragraphs(story.BodyMD)
	for i, para := range paras {
		payload.Items = append(payload.Items, Item{Seq: i + 1, Main: para})
	}

	if p.GetBool("with_questions", true) {
		qs := stringList(story.Discussion)
		title := "亲子讨论"
		if len(qs) == 0 {
			qs = stringList(story.Questions)
			title = "读一读想一想"
		}
		if len(qs) > 0 {
			rows := make([][]string, 0, len(qs))
			for _, q := range qs {
				rows = append(rows, []string{q})
			}
			payload.Sheets = append(payload.Sheets, Sheet{
				Title: title,
				Note:  "读完一起聊聊，不用写下来",
				Rows:  rows,
			})
		}
	}
	payload.Options.PerPage = rowsPerPageA5
	return payload, nil
}

// ---------------------------------------------------------------- 周学习报告

func (s *Service) buildWeeklyReport(ctx context.Context, spec TemplateSpec, p Params,
	child Child, childID uuid.UUID) (Payload, error) {
	days := p.GetInt("days", 7)
	meta := s.meta(spec, p, child, fmt.Sprintf("近 %d 天", days))
	payload := s.newPayload(spec, p, meta, "周学习报告", "家长版 · 只汇总不排名")
	payload.Options.PerPage = 14

	if s.report == nil || childID == uuid.Nil {
		payload.Warnings = append(payload.Warnings, "报表数据源未接入，报告内容为空")
		return payload, nil
	}

	ov, err := s.report.Overview(ctx, childID)
	if err != nil {
		return Payload{}, apperr.Internal(fmt.Errorf("读取总览失败: %w", err))
	}
	trend, err := s.report.Trend(ctx, childID, days, "")
	if err != nil {
		return Payload{}, apperr.Internal(fmt.Errorf("读取趋势失败: %w", err))
	}
	sug, err := s.report.Suggestions(ctx, childID)
	if err != nil {
		return Payload{}, apperr.Internal(fmt.Errorf("读取建议失败: %w", err))
	}

	// 顶部指标行
	payload.Items = append(payload.Items,
		Item{Sub: "累计掌握", Main: fmt.Sprintf("%d", ov.MasteredTotal)},
		Item{Sub: "连续达标", Main: fmt.Sprintf("%d 天", ov.StreakDays)},
		Item{Sub: "待复习", Main: fmt.Sprintf("%d", ov.DueTotal)},
		Item{Sub: "获得徽章", Main: fmt.Sprintf("%d / %d", ov.BadgesEarned, ov.BadgesTotal)},
	)

	// 学科分布
	subjectRows := make([][]string, 0, len(ov.Subjects))
	for _, sj := range ov.Subjects {
		subjectRows = append(subjectRows, []string{
			sj.SubjectName,
			fmt.Sprintf("%d", sj.Mastered),
			fmt.Sprintf("%d", sj.PlannedTotal),
			fmt.Sprintf("%d", sj.Due),
			fmt.Sprintf("%.0f%%", ratio(sj.Mastered, sj.PlannedTotal)*100),
		})
	}
	if len(subjectRows) > 0 {
		payload.Sheets = append(payload.Sheets, Sheet{
			Title: "学科进度",
			Head:  []string{"学科", "已掌握", "计划", "待复习", "完成度"},
			Rows:  subjectRows,
		})
	}

	// 每日趋势（倒序，最近的在最上面）
	trendRows := make([][]string, 0, len(trend.Points))
	for i := len(trend.Points) - 1; i >= 0; i-- {
		pt := trend.Points[i]
		passed := "—"
		if pt.Passed {
			passed = "达标"
		}
		trendRows = append(trendRows, []string{
			pt.Date,
			fmt.Sprintf("%d", pt.QuestionCount),
			fmt.Sprintf("%.0f%%", pt.Accuracy*100),
			fmt.Sprintf("%d", pt.NewMastered),
			fmt.Sprintf("%.1f", pt.DeviationDays),
			passed,
		})
	}
	if len(trendRows) > 0 {
		payload.Sheets = append(payload.Sheets, Sheet{
			Title: "每日明细",
			Note:  "偏差为正表示比基准线慢，为负表示提前完成",
			Head:  []string{"日期", "题量", "正确率", "新掌握", "偏差(天)", "达标"},
			Rows:  trendRows,
		})
	}

	// 建议
	if len(sug.Suggestions) > 0 {
		rows := make([][]string, 0, len(sug.Suggestions))
		for _, sg := range sug.Suggestions {
			rows = append(rows, []string{sg.Title + "：" + sg.Detail})
		}
		payload.Sheets = append(payload.Sheets, Sheet{
			Title: "下一步建议",
			Note:  "建议都可关闭，仅供参考",
			Rows:  rows,
		})
	}

	if len(payload.Sheets) == 0 {
		payload.Warnings = append(payload.Warnings, "这段时间还没有学习记录，先陪孩子做一次练习吧")
	}
	return payload, nil
}

// ---------------------------------------------------------------- 小工具

// upperLetters 是字母描红的字母表（顺序固定，大小写一一对应）。
var upperLetters = []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M",
	"N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z"}

func lowerLetters() []string {
	out := make([]string, 0, len(upperLetters))
	for _, s := range upperLetters {
		out = append(out, strings.ToLower(s))
	}
	return out
}

func ratio(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// paragraphs 把故事正文切成段落。
//
// 正文是采集来的 Markdown，但格式不统一：可能带标题行、可能用单换行分段。
// 打印小册子只需要「能读的段落」，所以去掉标题标记、把连续空行当分段。
func paragraphs(md string) []string {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	raw := strings.Split(md, "\n")
	out := make([]string, 0, len(raw))
	var buf []string
	flush := func() {
		if len(buf) > 0 {
			out = append(out, strings.Join(buf, ""))
			buf = buf[:0]
		}
	}
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		line = strings.TrimLeft(line, "#")
		line = strings.TrimSpace(line)
		line = strings.Trim(line, "*_`")
		if line == "" || line == "---" {
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

// stringList 宽松解析 jsonb 里的字符串数组：元素可能是字符串也可能是对象。
func stringList(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var asStrings []string
	if err := json.Unmarshal(raw, &asStrings); err == nil {
		return trimAll(asStrings)
	}
	var asAny []any
	if err := json.Unmarshal(raw, &asAny); err != nil {
		return nil
	}
	out := make([]string, 0, len(asAny))
	for _, v := range asAny {
		switch t := v.(type) {
		case string:
			out = append(out, t)
		case map[string]any:
			// 有些故事的题目是 {"q": "...", "a": "..."}，取题干
			for _, key := range []string{"q", "question", "text", "title"} {
				if s, ok := t[key].(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, s)
					break
				}
			}
		}
	}
	return trimAll(out)
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
