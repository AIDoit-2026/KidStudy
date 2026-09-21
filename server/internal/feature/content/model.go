// Package content 是内容底座：汉字 / 组词 / 英语词 / 故事 / 阶段字典的读取与检索，
// 以及一次性内容导入（cmd/importer 调用）。
//
// 分层遵循设计文档 §2.1：handler 只解析与格式化，service 承载规则，
// repository 负责 SQL，model 定义领域结构；领域结构与传输 DTO 分离。
package content

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------- 领域结构

// Stage 学习阶段（24 个汉字阶段 + 英语 10 级 + 数学 5 档）。
type Stage struct {
	Code        string
	SubjectCode string
	Name        string
	SortOrder   int
	TargetCount int
}

// HanziWord 控字组词。
type HanziWord struct {
	ID        uuid.UUID
	HanziID   uuid.UUID
	Word      string
	Pinyin    []string
	SortOrder int
	StageOK   bool
}

// Hanzi 汉字。
type Hanzi struct {
	ID           uuid.UUID
	Char         string
	KPID         *uuid.UUID
	Pinyin       []string
	Radical      string
	StrokeCount  *int16
	StrokePaths  json.RawMessage
	HasAnim      bool
	Explanation  string
	StageCode    string
	OrderInStage int
	Status       string
	Words        []HanziWord
}

// EnWord 英语单词。
type EnWord struct {
	ID           uuid.UUID
	Word         string
	Topic        string
	Phonetic     string
	Pos          string
	MeaningZH    string
	DefinitionEN string
	ExampleEN    string
	ExampleZH    string
	Frq          int32
	LevelCode    string
	ImageURL     string
	AudioURL     string
	Status       string
}

// Story 故事（中文或英文）。
type Story struct {
	ID         uuid.UUID
	Lang       string
	Title      string
	Summary    string
	BodyMD     string
	Category   string
	AgeGroup   string
	LevelCode  string
	CharCount  int32
	WordCount  int32
	CoverURL   string
	Images     []string
	NewChars   []string
	Questions  []string
	Discussion []string
	UnsafeHits []string
	Suitable   bool
	Source     string
	SourceRef  string
	SourceURL  string
	License    string
	Status     string
	CreatedAt  time.Time
}

// StoryPair 中英双语配对。
type StoryPair struct {
	ID       uuid.UUID
	Slug     string
	AgeGroup string
	ZH       Story
	EN       Story
}

// ---------------------------------------------------------------- 过滤条件

// HanziFilter 汉字列表过滤条件。空字符串表示不过滤。
type HanziFilter struct {
	StageCode    string
	Status       string
	IncludeWords bool
}

// EnWordFilter 英语词列表过滤条件。
type EnWordFilter struct {
	LevelCode string
	Topic     string
	Status    string
}

// StoryFilter 故事列表过滤条件。
type StoryFilter struct {
	Lang         string
	LevelCode    string
	Category     string
	Status       string
	SuitableOnly bool
}

// ---------------------------------------------------------------- 对外视图

// StageView 阶段视图。
type StageView struct {
	Code        string `json:"code"`
	SubjectCode string `json:"subject_code"`
	Name        string `json:"name"`
	SortOrder   int    `json:"sort_order"`
	TargetCount int    `json:"target_count"`
}

// ToView 转阶段视图。
func (s Stage) ToView() StageView {
	return StageView{
		Code:        s.Code,
		SubjectCode: s.SubjectCode,
		Name:        s.Name,
		SortOrder:   s.SortOrder,
		TargetCount: s.TargetCount,
	}
}

// HanziWordView 组词视图。
type HanziWordView struct {
	Word      string   `json:"word"`
	Pinyin    []string `json:"pinyin"`
	SortOrder int      `json:"sort_order"`
	StageOK   bool     `json:"stage_ok"`
}

// HanziView 汉字列表视图（不含字形数据，避免列表响应过大）。
type HanziView struct {
	ID           string          `json:"id"`
	Char         string          `json:"char"`
	Pinyin       []string        `json:"pinyin"`
	Radical      string          `json:"radical,omitempty"`
	StrokeCount  *int16          `json:"stroke_count,omitempty"`
	HasAnim      bool            `json:"has_anim"`
	Explanation  string          `json:"explanation,omitempty"`
	StageCode    string          `json:"stage_code,omitempty"`
	OrderInStage int             `json:"order_in_stage"`
	Words        []HanziWordView `json:"words,omitempty"`
}

// ToView 转汉字列表视图。
func (h Hanzi) ToView() HanziView {
	v := HanziView{
		ID:           h.ID.String(),
		Char:         h.Char,
		Pinyin:       h.Pinyin,
		Radical:      h.Radical,
		StrokeCount:  h.StrokeCount,
		HasAnim:      h.HasAnim,
		Explanation:  h.Explanation,
		StageCode:    h.StageCode,
		OrderInStage: h.OrderInStage,
	}
	for _, w := range h.Words {
		v.Words = append(v.Words, HanziWordView{Word: w.Word, Pinyin: w.Pinyin, SortOrder: w.SortOrder, StageOK: w.StageOK})
	}
	return v
}

// HanziDetailView 汉字详情视图，含笔顺字形数据。
type HanziDetailView struct {
	HanziView
	StrokePaths json.RawMessage `json:"stroke_paths,omitempty"`
}

// ToDetailView 转汉字详情视图。
func (h Hanzi) ToDetailView() HanziDetailView {
	return HanziDetailView{HanziView: h.ToView(), StrokePaths: h.StrokePaths}
}

// EnWordView 英语词视图。
type EnWordView struct {
	ID           string `json:"id"`
	Word         string `json:"word"`
	Topic        string `json:"topic,omitempty"`
	Phonetic     string `json:"phonetic,omitempty"`
	Pos          string `json:"pos,omitempty"`
	MeaningZH    string `json:"meaning_zh,omitempty"`
	DefinitionEN string `json:"definition_en,omitempty"`
	ExampleEN    string `json:"example_en,omitempty"`
	ExampleZH    string `json:"example_zh,omitempty"`
	Frq          int32  `json:"frq"`
	LevelCode    string `json:"level_code,omitempty"`
	ImageURL     string `json:"image_url,omitempty"`
	AudioURL     string `json:"audio_url,omitempty"`
}

// ToView 转英语词视图。
func (w EnWord) ToView() EnWordView {
	return EnWordView{
		ID:           w.ID.String(),
		Word:         w.Word,
		Topic:        w.Topic,
		Phonetic:     w.Phonetic,
		Pos:          w.Pos,
		MeaningZH:    w.MeaningZH,
		DefinitionEN: w.DefinitionEN,
		ExampleEN:    w.ExampleEN,
		ExampleZH:    w.ExampleZH,
		Frq:          w.Frq,
		LevelCode:    w.LevelCode,
		ImageURL:     w.ImageURL,
		AudioURL:     w.AudioURL,
	}
}

// StoryView 故事列表视图（不含正文，列表页不需要 7KB 的正文）。
type StoryView struct {
	ID        string `json:"id"`
	Lang      string `json:"lang"`
	Title     string `json:"title"`
	Summary   string `json:"summary,omitempty"`
	Category  string `json:"category,omitempty"`
	AgeGroup  string `json:"age_group,omitempty"`
	LevelCode string `json:"level_code,omitempty"`
	CharCount int32  `json:"char_count"`
	WordCount int32  `json:"word_count"`
	CoverURL  string `json:"cover_url,omitempty"`
	Suitable  bool   `json:"suitable"`
	Status    string `json:"status"`
	Source    string `json:"source,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	CreatedAt string `json:"created_at"`
}

// ToView 转故事列表视图。
func (s Story) ToView() StoryView {
	return StoryView{
		ID:        s.ID.String(),
		Lang:      s.Lang,
		Title:     s.Title,
		Summary:   s.Summary,
		Category:  s.Category,
		AgeGroup:  s.AgeGroup,
		LevelCode: s.LevelCode,
		CharCount: s.CharCount,
		WordCount: s.WordCount,
		CoverURL:  s.CoverURL,
		Suitable:  s.Suitable,
		Status:    s.Status,
		Source:    s.Source,
		SourceURL: s.SourceURL,
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// StoryDetailView 故事详情视图，含正文与题目。
type StoryDetailView struct {
	StoryView
	BodyMD     string   `json:"body_md"`
	Images     []string `json:"images,omitempty"`
	NewChars   []string `json:"new_chars,omitempty"`
	Questions  []string `json:"questions,omitempty"`
	Discussion []string `json:"discussion,omitempty"`
	License    string   `json:"license,omitempty"`
}

// ToDetailView 转故事详情视图。
func (s Story) ToDetailView() StoryDetailView {
	return StoryDetailView{
		StoryView:  s.ToView(),
		BodyMD:     s.BodyMD,
		Images:     s.Images,
		NewChars:   s.NewChars,
		Questions:  s.Questions,
		Discussion: s.Discussion,
		License:    s.License,
	}
}

// StoryPairView 双语配对视图，支持中 / 英 / 对照三种呈现。
type StoryPairView struct {
	Slug     string          `json:"slug"`
	AgeGroup string          `json:"age_group,omitempty"`
	ZH       StoryDetailView `json:"zh"`
	EN       StoryDetailView `json:"en"`
}
