package content

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// 组卷素材：practice 生成题目时要看内容的细节（读音、组词、释义、数学模板），
// 但这些细节归 content 管。这里一次性批量给出，practice 不必逐 kp 回查（避免 N+1）。

// HanziMaterial 是汉字类知识点的组卷素材。
type HanziMaterial struct {
	KPID        uuid.UUID `json:"kp_id"`
	HanziID     uuid.UUID `json:"hanzi_id"`
	Char        string    `json:"char"`
	Pinyin      []string  `json:"pinyin"`
	Words       []string  `json:"words"`
	Explanation string    `json:"explanation"`
	StageCode   string    `json:"stage_code"`
	HasAnim     bool      `json:"has_anim"`
}

// WordMaterial 是英语单词类知识点的组卷素材。
type WordMaterial struct {
	KPID      uuid.UUID `json:"kp_id"`
	WordID    uuid.UUID `json:"word_id"`
	Word      string    `json:"word"`
	Phonetic  string    `json:"phonetic"`
	MeaningZh string    `json:"meaning_zh"`
	ExampleEn string    `json:"example_en"`
	ExampleZh string    `json:"example_zh"`
	LevelCode string    `json:"level_code"`
	ImageURL  string    `json:"image_url"`
}

// MathMaterial 是数学题型档的组卷素材（模板 + 生成参数）。
type MathMaterial struct {
	KPID         uuid.UUID       `json:"kp_id"`
	TemplateCode string          `json:"template_code"`
	Band         string          `json:"band"`
	Generator    json.RawMessage `json:"generator"`
	Display      json.RawMessage `json:"display"`
}

// StoryMaterial 是故事类知识点的组卷素材（亲子朗读用）。
type StoryMaterial struct {
	KPID      uuid.UUID `json:"kp_id"`
	StoryID   uuid.UUID `json:"story_id"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Lang      string    `json:"lang"`
	LevelCode string    `json:"level_code"`
}

// Materials 是按 kp_id 索引的素材集合。
type Materials struct {
	Hanzi   map[uuid.UUID]HanziMaterial `json:"hanzi"`
	Words   map[uuid.UUID]WordMaterial  `json:"words"`
	Math    map[uuid.UUID]MathMaterial  `json:"math"`
	Stories map[uuid.UUID]StoryMaterial `json:"stories"`
}

// NewMaterials 造一个空素材集。
func NewMaterials() Materials {
	return Materials{
		Hanzi:   map[uuid.UUID]HanziMaterial{},
		Words:   map[uuid.UUID]WordMaterial{},
		Math:    map[uuid.UUID]MathMaterial{},
		Stories: map[uuid.UUID]StoryMaterial{},
	}
}

// LoadMaterials 批量装载组卷素材。
//
// 一次拿到全部 kp 的素材，practice 侧不再逐条回查；查不到的 kp 直接缺席，
// 由 practice 决定跳过还是降级，不在这里返回错误。
func (s *Service) LoadMaterials(ctx context.Context, kpIDs []uuid.UUID) (Materials, error) {
	out := NewMaterials()
	if len(kpIDs) == 0 {
		return out, nil
	}

	hanziRows, err := s.repo.loadHanziByKPs(ctx, kpIDs)
	if err != nil {
		return Materials{}, err
	}
	hanziIDs := make([]uuid.UUID, 0, len(hanziRows))
	for _, row := range hanziRows {
		out.Hanzi[row.KpID] = HanziMaterial{
			KPID:        row.KpID,
			HanziID:     row.HanziID,
			Char:        row.Char,
			Pinyin:      stringSlice(row.Pinyin),
			Explanation: nullString(row.Explanation),
			StageCode:   nullString(row.StageCode),
			HasAnim:     row.HasAnim,
		}
		hanziIDs = append(hanziIDs, row.HanziID)
	}
	// 组词单独批量取一次，再按 hanzi_id 归位
	wordsByHanzi, err := s.repo.listHanziWords(ctx, hanziIDs)
	if err != nil {
		return Materials{}, err
	}
	for kpID, m := range out.Hanzi {
		m.Words = wordsByHanzi[m.HanziID]
		out.Hanzi[kpID] = m
	}

	wordRows, err := s.repo.loadEnWordsByKPs(ctx, kpIDs)
	if err != nil {
		return Materials{}, err
	}
	for _, row := range wordRows {
		out.Words[row.KpID] = WordMaterial{
			KPID:      row.KpID,
			WordID:    row.WordID,
			Word:      row.Word,
			Phonetic:  nullString(row.Phonetic),
			MeaningZh: nullString(row.MeaningZh),
			ExampleEn: nullString(row.ExampleEn),
			ExampleZh: nullString(row.ExampleZh),
			LevelCode: nullString(row.LevelCode),
			ImageURL:  nullString(row.ImageUrl),
		}
	}

	mathRows, err := s.repo.loadMathTemplatesByKPs(ctx, kpIDs)
	if err != nil {
		return Materials{}, err
	}
	for _, row := range mathRows {
		out.Math[row.KpID] = MathMaterial{
			KPID:         row.KpID,
			TemplateCode: row.TemplateCode,
			Band:         nullString(row.DifficultyBand),
			Generator:    row.GeneratorConfig,
			Display:      row.DisplayConfig,
		}
	}

	storyRows, err := s.repo.loadStoriesByKPs(ctx, kpIDs)
	if err != nil {
		return Materials{}, err
	}
	for _, row := range storyRows {
		out.Stories[row.KpID] = StoryMaterial{
			KPID:      row.KpID,
			StoryID:   row.StoryID,
			Title:     row.Title,
			Summary:   nullString(row.Summary),
			Lang:      row.Lang,
			LevelCode: nullString(row.LevelCode),
		}
	}

	return out, nil
}

// ListMathTemplates 列出全部已发布的数学模板，供家长端挑题与打印预览。
func (s *Service) ListMathTemplates(ctx context.Context) ([]MathTemplateView, error) {
	rows, err := s.repo.listMathTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MathTemplateView, 0, len(rows))
	for _, row := range rows {
		out = append(out, MathTemplateView{
			Code:            row.Code,
			QuestionType:    row.QuestionType,
			DifficultyBand:  nullString(row.DifficultyBand),
			GeneratorConfig: row.GeneratorConfig,
			DisplayConfig:   row.DisplayConfig,
		})
	}
	return out, nil
}

// MathTemplateView 是对外返回的数学模板。
type MathTemplateView struct {
	Code            string          `json:"code"`
	QuestionType    string          `json:"question_type"`
	DifficultyBand  string          `json:"difficulty_band"`
	GeneratorConfig json.RawMessage `json:"generator_config"`
	DisplayConfig   json.RawMessage `json:"display_config"`
}

// GetMathTemplate 按 code 取单个模板（打印预览与屏幕练习同源）。
func (s *Service) GetMathTemplate(ctx context.Context, code string) (MathMaterial, error) {
	row, err := s.repo.getMathTemplate(ctx, code)
	if err != nil {
		return MathMaterial{}, err
	}
	return MathMaterial{
		TemplateCode: row.Code,
		Band:         nullString(row.DifficultyBand),
		Generator:    row.GeneratorConfig,
		Display:      row.DisplayConfig,
	}, nil
}
