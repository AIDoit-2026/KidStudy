package practice

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"kidstudy/internal/feature/content"
	"kidstudy/internal/pkg/randx"
)

// BuildQuestions 把「今日任务项」变成带题面与答案的题目。
//
// 干扰项全部从同一批素材里取（同阶段/同级别的其它 kp），既省掉一次额外查询，
// 又天然保证干扰项的难度与正确答案相当 —— 拿高年级字去干扰学前字没有意义。
//
// 素材缺失的项会被跳过（返回时直接不出现），不让它拖垮整次组卷。
func BuildQuestions(items []PlanItem, m content.Materials, seed uint64) []BuiltQuestion {
	out := make([]BuiltQuestion, 0, len(items))
	for i, item := range items {
		r := randx.New(randx.Derive(seed, i))
		qType := pickType(item.Kind, item.Reason, r)

		var built *BuiltQuestion
		switch item.Kind {
		case "hanzi":
			built = buildHanzi(item, m, qType, r)
		case "word":
			built = buildWord(item, m, qType, r)
		case "math_skill":
			built = buildMath(item, m, seed, uint64(i))
		case "story":
			built = buildStory(item, m, qType)
		}
		if built == nil {
			continue
		}
		built.KPID = item.KPID
		built.SubjectCode = item.SubjectCode
		if built.Difficulty <= 0 {
			built.Difficulty = 1
		}
		out = append(out, *built)
	}
	return out
}

// pickType 按「知识点类型 + 来源」选择题型。
//
// 新学的汉字以描红为主（先认字形），复习以识别类为主（考提取）；
// 英语新词先听音，复习再考形义对应。数学与故事各自只有一种形态。
func pickType(kind, reason string, r *randx.Rand) string {
	newLearn := reason == ReasonNew
	switch kind {
	case "hanzi":
		if newLearn {
			return []string{TypeTrace, TypeTrace, TypeChoiceText, TypeFillBlank, TypeOrder}[r.Intn(5)]
		}
		return []string{TypeChoiceText, TypeChoiceText, TypeFillBlank, TypeMatch, TypeOrder}[r.Intn(5)]
	case "word":
		if newLearn {
			return []string{TypeChoiceAudio, TypeChoiceText, TypeOrder}[r.Intn(3)]
		}
		return []string{TypeChoiceText, TypeChoiceImage, TypeFillBlank, TypeMatch}[r.Intn(4)]
	case "math_skill":
		return TypeMathParam
	case "story":
		return TypeSay
	}
	return TypeChoiceText
}

// ------------------------------------------------------------------ 汉字

func buildHanzi(item PlanItem, m content.Materials, qType string, r *randx.Rand) *BuiltQuestion {
	mat, ok := m.Hanzi[item.KPID]
	if !ok || mat.Char == "" {
		return nil
	}
	// 干扰项：同一阶段的其它字，取不到就退到任意同科字
	pool := hanziPool(m, mat.StageCode, item.KPID)
	pinyin := ""
	if len(mat.Pinyin) > 0 {
		pinyin = strings.Join(mat.Pinyin, " / ")
	}

	switch qType {
	case TypeTrace:
		return &BuiltQuestion{
			StageCode:    mat.StageCode,
			QuestionType: TypeTrace,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeTrace,
				SubjectCode: item.SubjectCode,
				Prompt:      "描一描这个字",
				PromptSub:   pinyin,
				AudioText:   mat.Char,
				Extra: map[string]any{
					"char":     mat.Char,
					"hanzi_id": mat.HanziID.String(),
					"has_anim": mat.HasAnim,
				},
			},
			Key: AnswerKey{Kind: KeyKindNone, Text: mat.Char},
		}

	case TypeFillBlank:
		word := pickWordWithChar(mat.Words, mat.Char, r)
		if word == "" {
			return buildHanzi(item, m, TypeChoiceText, r)
		}
		blanked := strings.Replace(word, mat.Char, "＿", 1)
		options, correctID := buildOptions(mat.Char, pool, 4, r)
		return &BuiltQuestion{
			StageCode:    mat.StageCode,
			QuestionType: TypeFillBlank,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeFillBlank,
				SubjectCode: item.SubjectCode,
				Prompt:      "把词语补充完整：" + blanked,
				PromptSub:   pinyin,
				Options:     options,
			},
			Key: AnswerKey{Kind: KeyKindSingle, Correct: []string{correctID}, Text: mat.Char,
				Explain: mat.Char + "（" + pinyin + "），组词「" + word + "」"},
		}

	case TypeOrder:
		word := pickOrderableWord(mat.Words, r)
		if word == "" {
			return buildHanzi(item, m, TypeChoiceText, r)
		}
		chars := splitRunes(word)
		scrambled := make([]string, len(chars))
		copy(scrambled, chars)
		randx.Shuffle(r, scrambled)
		return &BuiltQuestion{
			StageCode:    mat.StageCode,
			QuestionType: TypeOrder,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeOrder,
				SubjectCode: item.SubjectCode,
				Prompt:      "把下面的字排成一个词语",
				PromptSub:   pinyin,
				OrderItems:  scrambled,
			},
			Key: AnswerKey{Kind: KeyKindSequence, Correct: chars, Text: word,
				Explain: "正确词语是「" + word + "」"},
		}

	case TypeMatch:
		lefts := []string{mat.Char}
		rights := []string{pinyin}
		picked := randx.Sample(r, pool, 3)
		for _, p := range picked {
			if p.Char == "" || p.Char == mat.Char {
				continue
			}
			lefts = append(lefts, p.Char)
			rights = append(rights, strings.Join(p.Pinyin, " / "))
		}
		if len(lefts) < 2 {
			return buildHanzi(item, m, TypeChoiceText, r)
		}
		shuffledRights := make([]string, len(rights))
		copy(shuffledRights, rights)
		randx.Shuffle(r, shuffledRights)
		pairs := make([]string, 0, len(lefts))
		for i := range lefts {
			pairs = append(pairs, lefts[i]+"|"+rights[i])
		}
		return &BuiltQuestion{
			StageCode:    mat.StageCode,
			QuestionType: TypeMatch,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeMatch,
				SubjectCode: item.SubjectCode,
				Prompt:      "把汉字和它的读音连起来",
				MatchLeft:   lefts,
				MatchRight:  shuffledRights,
			},
			Key: AnswerKey{Kind: KeyKindMatch, Correct: pairs,
				Explain: "按读音把每个字和它的拼音连起来"},
		}

	default: // TypeChoiceText
		options, correctID := buildOptions(mat.Char, pool, 4, r)
		return &BuiltQuestion{
			StageCode:    mat.StageCode,
			QuestionType: TypeChoiceText,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeChoiceText,
				SubjectCode: item.SubjectCode,
				Prompt:      "哪个字读「" + pinyin + "」？",
				Options:     options,
				AudioText:   mat.Char,
			},
			Key: AnswerKey{Kind: KeyKindSingle, Correct: []string{correctID}, Text: mat.Char,
				Explain: mat.Char + "（" + pinyin + "）" + hanziExplain(mat)},
		}
	}
}

func hanziPool(m content.Materials, stage string, exclude uuid.UUID) []content.HanziMaterial {
	pool := make([]content.HanziMaterial, 0, 8)
	for id, h := range m.Hanzi {
		if id == exclude || h.Char == "" {
			continue
		}
		if stage != "" && h.StageCode != stage {
			continue
		}
		pool = append(pool, h)
	}
	// 同阶段不够时放开阶段限制，宁可干扰项难度略偏也不要出只有 1 个选项的题
	if len(pool) < 3 && stage != "" {
		return hanziPool(m, "", exclude)
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].Char < pool[j].Char })
	return pool
}

func hanziExplain(m content.HanziMaterial) string {
	if m.Explanation == "" {
		return ""
	}
	return "，" + m.Explanation
}

func pickWordWithChar(words []string, char string, r *randx.Rand) string {
	hits := make([]string, 0, len(words))
	for _, w := range words {
		if strings.Contains(w, char) && utf8.RuneCountInString(w) >= 2 {
			hits = append(hits, w)
		}
	}
	if len(hits) == 0 {
		return ""
	}
	return hits[r.Intn(len(hits))]
}

func pickOrderableWord(words []string, r *randx.Rand) string {
	cand := make([]string, 0, len(words))
	for _, w := range words {
		n := utf8.RuneCountInString(w)
		if n >= 2 && n <= 4 {
			cand = append(cand, w)
		}
	}
	if len(cand) == 0 {
		return ""
	}
	return cand[r.Intn(len(cand))]
}

// ------------------------------------------------------------------ 英语单词

func buildWord(item PlanItem, m content.Materials, qType string, r *randx.Rand) *BuiltQuestion {
	mat, ok := m.Words[item.KPID]
	if !ok || mat.Word == "" {
		return nil
	}
	pool := wordPool(m, mat.LevelCode, item.KPID)
	meaning := firstNonEmpty(mat.MeaningZh, mat.Word)

	switch qType {
	case TypeChoiceAudio:
		options, correctID := buildWordOptions(mat.Word, pool, 4, r)
		return &BuiltQuestion{
			StageCode:    mat.LevelCode,
			QuestionType: TypeChoiceAudio,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeChoiceAudio,
				SubjectCode: item.SubjectCode,
				Prompt:      "听一听，选出你听到的单词",
				AudioText:   mat.Word,
				Options:     options,
			},
			Key: AnswerKey{Kind: KeyKindSingle, Correct: []string{correctID}, Text: mat.Word,
				Explain: mat.Word + " " + mat.Phonetic + " " + meaning},
		}

	case TypeChoiceImage:
		options, correctID := buildImageOptions(mat, pool, 4, r)
		return &BuiltQuestion{
			StageCode:    mat.LevelCode,
			QuestionType: TypeChoiceImage,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeChoiceImage,
				SubjectCode: item.SubjectCode,
				Prompt:      "哪一个是「" + meaning + "」？",
				Options:     options,
			},
			Key: AnswerKey{Kind: KeyKindSingle, Correct: []string{correctID}, Text: mat.Word,
				Explain: mat.Word + " " + meaning},
		}

	case TypeFillBlank:
		blanked := maskWord(mat.Word, r)
		if blanked == mat.Word {
			return buildWord(item, m, TypeChoiceText, r)
		}
		return &BuiltQuestion{
			StageCode:    mat.LevelCode,
			QuestionType: TypeFillBlank,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeFillBlank,
				SubjectCode: item.SubjectCode,
				Prompt:      "补全单词：" + blanked,
				PromptSub:   meaning,
			},
			Key: AnswerKey{Kind: KeyKindFree, Accept: []string{mat.Word}, Text: mat.Word,
				Explain: mat.Word + " " + mat.Phonetic + " " + meaning},
		}

	case TypeMatch:
		lefts := []string{mat.Word}
		rights := []string{meaning}
		for _, p := range randx.Sample(r, pool, 3) {
			lefts = append(lefts, p.Word)
			rights = append(rights, firstNonEmpty(p.MeaningZh, p.Word))
		}
		if len(lefts) < 2 {
			return buildWord(item, m, TypeChoiceText, r)
		}
		shuffled := make([]string, len(rights))
		copy(shuffled, rights)
		randx.Shuffle(r, shuffled)
		pairs := make([]string, 0, len(lefts))
		for i := range lefts {
			pairs = append(pairs, lefts[i]+"|"+rights[i])
		}
		return &BuiltQuestion{
			StageCode:    mat.LevelCode,
			QuestionType: TypeMatch,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeMatch,
				SubjectCode: item.SubjectCode,
				Prompt:      "把英文单词和中文意思连起来",
				MatchLeft:   lefts,
				MatchRight:  shuffled,
			},
			Key: AnswerKey{Kind: KeyKindMatch, Correct: pairs,
				Explain: "按词义把每个单词和它的中文连起来"},
		}

	case TypeOrder:
		if utf8.RuneCountInString(mat.Word) < 3 {
			return buildWord(item, m, TypeChoiceText, r)
		}
		letters := splitRunes(mat.Word)
		scrambled := make([]string, len(letters))
		copy(scrambled, letters)
		randx.Shuffle(r, scrambled)
		return &BuiltQuestion{
			StageCode:    mat.LevelCode,
			QuestionType: TypeOrder,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeOrder,
				SubjectCode: item.SubjectCode,
				Prompt:      "把字母排成一个单词",
				PromptSub:   meaning,
				OrderItems:  scrambled,
			},
			Key: AnswerKey{Kind: KeyKindSequence, Correct: letters, Text: mat.Word,
				Explain: "正确拼写是 " + mat.Word},
		}

	default: // TypeChoiceText
		options, correctID := buildWordOptions(mat.Word, pool, 4, r)
		return &BuiltQuestion{
			StageCode:    mat.LevelCode,
			QuestionType: TypeChoiceText,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeChoiceText,
				SubjectCode: item.SubjectCode,
				Prompt:      "「" + meaning + "」是哪个单词？",
				Options:     options,
			},
			Key: AnswerKey{Kind: KeyKindSingle, Correct: []string{correctID}, Text: mat.Word,
				Explain: mat.Word + " " + mat.Phonetic + " " + meaning},
		}
	}
}

func wordPool(m content.Materials, level string, exclude uuid.UUID) []content.WordMaterial {
	pool := make([]content.WordMaterial, 0, 8)
	for id, w := range m.Words {
		if id == exclude || w.Word == "" {
			continue
		}
		if level != "" && w.LevelCode != level {
			continue
		}
		pool = append(pool, w)
	}
	if len(pool) < 3 && level != "" {
		return wordPool(m, "", exclude)
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].Word < pool[j].Word })
	return pool
}

// ------------------------------------------------------------------ 数学

func buildMath(item PlanItem, m content.Materials, seed uint64, index uint64) *BuiltQuestion {
	mat, ok := m.Math[item.KPID]
	if !ok {
		return nil
	}
	cfg, err := ParseMathConfig(mat.Generator)
	if err != nil {
		return nil
	}
	questions := GenerateMath(cfg, randx.Derive(seed, int(index)), 1)
	if len(questions) == 0 {
		return nil
	}
	q := questions[0]

	// 比大小给三个固定选项，其余题型自由输入（数字键盘或手写）
	if cfg.Op == "cmp" {
		symbols := []string{">", "<", "="}
		options := make([]Option, 0, len(symbols))
		correctID := "0"
		for i, s := range symbols {
			options = append(options, Option{ID: strconv.Itoa(i), Label: s})
			if s == q.Answer {
				correctID = strconv.Itoa(i)
			}
		}
		return &BuiltQuestion{
			StageCode:    mat.Band,
			QuestionType: TypeMathParam,
			Difficulty:   item.Difficulty,
			Snapshot: Question{
				Type:        TypeMathParam,
				SubjectCode: item.SubjectCode,
				Prompt:      q.Prompt,
				Options:     options,
				Layout:      q.Layout,
				Extra:       map[string]any{"template": mat.TemplateCode},
			},
			Key: AnswerKey{Kind: KeyKindSingle, Correct: []string{correctID}, Text: q.Answer, Explain: q.Explain},
		}
	}

	return &BuiltQuestion{
		StageCode:    mat.Band,
		QuestionType: TypeMathParam,
		Difficulty:   item.Difficulty,
		Snapshot: Question{
			Type:        TypeMathParam,
			SubjectCode: item.SubjectCode,
			Prompt:      q.Prompt,
			Layout:      q.Layout,
			Extra:       map[string]any{"template": mat.TemplateCode},
		},
		Key: AnswerKey{Kind: KeyKindFree, Accept: []string{q.Answer}, Text: q.Answer, Explain: q.Explain},
	}
}

// ------------------------------------------------------------------ 故事（亲子朗读）

func buildStory(item PlanItem, m content.Materials, qType string) *BuiltQuestion {
	mat, ok := m.Stories[item.KPID]
	if !ok {
		return nil
	}
	return &BuiltQuestion{
		StageCode:    mat.LevelCode,
		QuestionType: TypeSay,
		Difficulty:   item.Difficulty,
		Snapshot: Question{
			Type:        TypeSay,
			SubjectCode: item.SubjectCode,
			Prompt:      "和爸爸妈妈一起读：《" + mat.Title + "》",
			PromptSub:   mat.Summary,
			AudioText:   mat.Title,
			Extra: map[string]any{
				"story_id": mat.StoryID.String(),
				"lang":     mat.Lang,
			},
		},
		Key: AnswerKey{Kind: KeyKindNone, Text: mat.Title,
			Explain: "读完请家长点「完成」，可以顺便给个评分"},
	}
}

// ------------------------------------------------------------------ 组装选项

// buildOptions 把正确答案混进干扰项并打乱，返回选项与正确项 id。
func buildOptions(correct string, pool []content.HanziMaterial, want int, r *randx.Rand) ([]Option, string) {
	labels := []string{correct}
	seen := map[string]bool{correct: true}
	for _, h := range randx.Sample(r, pool, want+2) {
		if len(labels) >= want {
			break
		}
		if seen[h.Char] {
			continue
		}
		seen[h.Char] = true
		labels = append(labels, h.Char)
	}
	randx.Shuffle(r, labels)
	options := make([]Option, 0, len(labels))
	correctID := "0"
	for i, l := range labels {
		id := strconv.Itoa(i)
		options = append(options, Option{ID: id, Label: l})
		if l == correct {
			correctID = id
		}
	}
	return options, correctID
}

func buildWordOptions(correct string, pool []content.WordMaterial, want int, r *randx.Rand) ([]Option, string) {
	labels := []string{correct}
	for _, w := range randx.Sample(r, pool, want+2) {
		if len(labels) >= want {
			break
		}
		if w.Word == correct || contains(labels, w.Word) {
			continue
		}
		labels = append(labels, w.Word)
	}
	randx.Shuffle(r, labels)
	options := make([]Option, 0, len(labels))
	correctID := "0"
	for i, l := range labels {
		id := strconv.Itoa(i)
		options = append(options, Option{ID: id, Label: l})
		if l == correct {
			correctID = id
		}
	}
	return options, correctID
}

func buildImageOptions(mat content.WordMaterial, pool []content.WordMaterial, want int, r *randx.Rand) ([]Option, string) {
	type item struct {
		word  string
		image string
	}
	items := []item{{word: mat.Word, image: mat.ImageURL}}
	for _, w := range randx.Sample(r, pool, want+2) {
		if len(items) >= want {
			break
		}
		if w.Word == mat.Word {
			continue
		}
		items = append(items, item{word: w.Word, image: w.ImageURL})
	}
	randx.Shuffle(r, items)
	options := make([]Option, 0, len(items))
	correctID := "0"
	for i, it := range items {
		id := strconv.Itoa(i)
		options = append(options, Option{ID: id, Label: it.word, Image: it.image})
		if it.word == mat.Word {
			correctID = id
		}
	}
	return options, correctID
}

// ------------------------------------------------------------------ 判分

// Grade 比对答案。返回 nil 表示这道题不判分（描红/跟读）。
func Grade(key AnswerKey, answer []string) *bool {
	if key.Kind == KeyKindNone {
		return nil
	}
	var ok bool
	switch key.Kind {
	case KeyKindSingle:
		ok = len(key.Correct) > 0 && len(answer) > 0 && strings.TrimSpace(answer[0]) == key.Correct[0]
	case KeyKindSequence:
		ok = join(answer) == join(key.Correct)
	case KeyKindFree:
		ok = len(answer) > 0 && acceptEqual(key.Accept, answer[0])
	case KeyKindMatch:
		ok = matchEqual(key.Correct, answer)
	default:
		return nil
	}
	return &ok
}

func join(xs []string) string { return strings.Join(xs, "") }

func acceptEqual(accept []string, got string) bool {
	g := normalizeAnswer(got)
	for _, a := range accept {
		if normalizeAnswer(a) == g {
			return true
		}
	}
	return false
}

// normalizeAnswer 判分时忽略大小写、空白与全角空格 —— 孩子手抖多打一个空格不该算错。
func normalizeAnswer(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\u3000", "")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func matchEqual(want, got []string) bool {
	if len(want) != len(got) || len(want) == 0 {
		return false
	}
	w := append([]string(nil), want...)
	g := append([]string(nil), got...)
	sort.Strings(w)
	sort.Strings(g)
	for i := range w {
		if w[i] != g[i] {
			return false
		}
	}
	return true
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func splitRunes(s string) []string {
	out := make([]string, 0, utf8.RuneCountInString(s))
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

// maskWord 把单词挖空：3 个字母以内挖 1 个，更长的挖 2 个（避开首尾，保留可辨识度）。
func maskWord(word string, r *randx.Rand) string {
	runes := []rune(word)
	n := len(runes)
	if n <= 2 {
		return word
	}
	blanks := 1
	if n >= 5 {
		blanks = 2
	}
	idx := make(map[int]bool)
	for i := 0; i < blanks; i++ {
		// 只在中间段挖空，首尾留着更容易猜
		pos := 1 + r.Intn(maxInt(n-2, 1))
		idx[pos] = true
	}
	for p := range idx {
		if p > 0 && p < n {
			runes[p] = '_'
		}
	}
	return string(runes)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// DecodeKey 从题目快照里取出答案（用于判分）。
func DecodeKey(raw json.RawMessage) (AnswerKey, error) {
	var key AnswerKey
	if err := json.Unmarshal(raw, &key); err != nil {
		return AnswerKey{}, err
	}
	return key, nil
}
