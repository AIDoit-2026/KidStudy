package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"kidstudy/internal/dbgen"
)

// Repository 负责内容域的读查询。
//
// 读查询全部走 sqlc 生成的代码（queries/content.sql → dbgen），保证 SQL 在编译期
// 就被校验；批量写入见 bulk.go。
type Repository struct {
	q    *dbgen.Queries
	pool *pgxpool.Pool
}

// NewRepository 构造内容仓储。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{q: dbgen.New(pool), pool: pool}
}

// Pool 暴露连接池，供需要事务/批量写的调用方使用。
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

// ---------------------------------------------------------------- 阶段

// ListStages 按学科列出阶段；subjectCode 为空返回全部。
func (r *Repository) ListStages(ctx context.Context, subjectCode string) ([]Stage, error) {
	rows, err := r.q.ListStages(ctx, subjectCode)
	if err != nil {
		return nil, fmt.Errorf("查询学习阶段失败: %w", err)
	}
	out := make([]Stage, 0, len(rows))
	for _, row := range rows {
		out = append(out, Stage{
			Code:        row.Code,
			SubjectCode: row.SubjectCode,
			Name:        row.Name,
			SortOrder:   int(row.SortOrder),
			TargetCount: int(row.TargetCount),
		})
	}
	return out, nil
}

// ---------------------------------------------------------------- 汉字

// ListHanzi 分页列出汉字。
func (r *Repository) ListHanzi(ctx context.Context, f HanziFilter, offset, limit int) ([]Hanzi, int64, error) {
	total, err := r.q.CountHanzi(ctx, dbgen.CountHanziParams{StageCode: f.StageCode, Status: f.Status})
	if err != nil {
		return nil, 0, fmt.Errorf("统计汉字失败: %w", err)
	}
	if total == 0 {
		return []Hanzi{}, 0, nil
	}

	rows, err := r.q.ListHanzi(ctx, dbgen.ListHanziParams{
		StageCode: f.StageCode,
		Status:    f.Status,
		Off:       int32(offset),
		Lim:       int32(limit),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("查询汉字失败: %w", err)
	}

	list := make([]Hanzi, 0, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		list = append(list, Hanzi{
			ID:           row.ID,
			Char:         row.Char,
			Pinyin:       decodeStringList(row.Pinyin),
			Radical:      row.Radical.String,
			StrokeCount:  int16Ptr(row.StrokeCount),
			HasAnim:      row.HasAnim,
			Explanation:  row.Explanation.String,
			StageCode:    row.StageCode.String,
			OrderInStage: int(row.OrderInStage),
			Status:       row.Status,
		})
		ids = append(ids, row.ID)
	}

	if f.IncludeWords && len(ids) > 0 {
		// 一次取回本页所有组词，避免逐字查询造成 N+1
		words, err := r.ListHanziWords(ctx, ids)
		if err != nil {
			return nil, 0, err
		}
		byHanzi := map[uuid.UUID][]HanziWord{}
		for _, w := range words {
			byHanzi[w.HanziID] = append(byHanzi[w.HanziID], w)
		}
		for i := range list {
			list[i].Words = byHanzi[list[i].ID]
		}
	}

	return list, total, nil
}

// GetHanzi 按 UUID 或汉字取单个汉字（含笔顺字形数据）。
func (r *Repository) GetHanzi(ctx context.Context, id uuid.UUID, char string) (Hanzi, error) {
	var (
		row dbgen.Hanzi
		err error
	)
	if char != "" {
		row, err = r.q.GetHanziByChar(ctx, char)
	} else {
		row, err = r.q.GetHanziByID(ctx, id)
	}
	if err != nil {
		return Hanzi{}, err
	}

	h := Hanzi{
		ID:           row.ID,
		Char:         row.Char,
		KPID:         uuidPtr(row.KpID),
		Pinyin:       decodeStringList(row.Pinyin),
		Radical:      row.Radical.String,
		StrokeCount:  int16Ptr(row.StrokeCount),
		StrokePaths:  row.StrokePaths,
		HasAnim:      row.HasAnim,
		Explanation:  row.Explanation.String,
		StageCode:    row.StageCode.String,
		OrderInStage: int(row.OrderInStage),
		Status:       row.Status,
	}

	words, err := r.ListHanziWords(ctx, []uuid.UUID{h.ID})
	if err != nil {
		return Hanzi{}, err
	}
	h.Words = words
	return h, nil
}

// ListHanziWords 批量取组词。
func (r *Repository) ListHanziWords(ctx context.Context, hanziIDs []uuid.UUID) ([]HanziWord, error) {
	rows, err := r.q.ListHanziWords(ctx, hanziIDs)
	if err != nil {
		return nil, fmt.Errorf("查询组词失败: %w", err)
	}
	out := make([]HanziWord, 0, len(rows))
	for _, row := range rows {
		out = append(out, HanziWord{
			ID:        row.ID,
			HanziID:   row.HanziID,
			Word:      row.Word,
			Pinyin:    decodeStringList(row.Pinyin),
			SortOrder: int(row.SortOrder),
			StageOK:   row.StageOk,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------- 英语词

// ListEnWords 分页列出英语词。
func (r *Repository) ListEnWords(ctx context.Context, f EnWordFilter, offset, limit int) ([]EnWord, int64, error) {
	p := dbgen.CountEnWordsParams{LevelCode: f.LevelCode, Topic: f.Topic, Status: f.Status}
	total, err := r.q.CountEnWords(ctx, p)
	if err != nil {
		return nil, 0, fmt.Errorf("统计英语词失败: %w", err)
	}
	if total == 0 {
		return []EnWord{}, 0, nil
	}

	rows, err := r.q.ListEnWords(ctx, dbgen.ListEnWordsParams{
		LevelCode: f.LevelCode,
		Topic:     f.Topic,
		Status:    f.Status,
		Off:       int32(offset),
		Lim:       int32(limit),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("查询英语词失败: %w", err)
	}

	out := make([]EnWord, 0, len(rows))
	for _, row := range rows {
		out = append(out, EnWord{
			ID:           row.ID,
			Word:         row.Word,
			Topic:        row.Topic.String,
			Phonetic:     row.Phonetic.String,
			Pos:          row.Pos.String,
			MeaningZH:    row.MeaningZh.String,
			DefinitionEN: row.DefinitionEn.String,
			ExampleEN:    row.ExampleEn.String,
			ExampleZH:    row.ExampleZh.String,
			Frq:          row.Frq,
			LevelCode:    row.LevelCode.String,
			ImageURL:     row.ImageUrl.String,
			AudioURL:     row.AudioUrl.String,
			Status:       row.Status,
		})
	}
	return out, total, nil
}

// ---------------------------------------------------------------- 故事

// ListStories 分页列出故事。
func (r *Repository) ListStories(ctx context.Context, f StoryFilter, offset, limit int) ([]Story, int64, error) {
	cp := dbgen.CountStoriesParams{
		Lang:         f.Lang,
		LevelCode:    f.LevelCode,
		Category:     f.Category,
		Status:       f.Status,
		SuitableOnly: f.SuitableOnly,
	}
	total, err := r.q.CountStories(ctx, cp)
	if err != nil {
		return nil, 0, fmt.Errorf("统计故事失败: %w", err)
	}
	if total == 0 {
		return []Story{}, 0, nil
	}

	rows, err := r.q.ListStories(ctx, dbgen.ListStoriesParams{
		Lang:         f.Lang,
		LevelCode:    f.LevelCode,
		Category:     f.Category,
		Status:       f.Status,
		SuitableOnly: f.SuitableOnly,
		Off:          int32(offset),
		Lim:          int32(limit),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("查询故事失败: %w", err)
	}

	out := make([]Story, 0, len(rows))
	for _, row := range rows {
		out = append(out, Story{
			ID:        row.ID,
			Lang:      row.Lang,
			Title:     row.Title,
			Summary:   row.Summary.String,
			Category:  row.Category.String,
			AgeGroup:  row.AgeGroup.String,
			LevelCode: row.LevelCode.String,
			CharCount: row.CharCount,
			WordCount: row.WordCount,
			CoverURL:  row.CoverUrl.String,
			Suitable:  row.Suitable,
			Status:    row.Status,
			Source:    row.Source,
			SourceURL: row.SourceUrl.String,
			CreatedAt: row.CreatedAt.Time,
		})
	}
	return out, total, nil
}

// GetStory 取故事详情。
func (r *Repository) GetStory(ctx context.Context, id uuid.UUID) (Story, error) {
	row, err := r.q.GetStoryByID(ctx, id)
	if err != nil {
		return Story{}, err
	}
	return Story{
		ID:         row.ID,
		Lang:       row.Lang,
		Title:      row.Title,
		Summary:    row.Summary.String,
		BodyMD:     row.BodyMd,
		Category:   row.Category.String,
		AgeGroup:   row.AgeGroup.String,
		LevelCode:  row.LevelCode.String,
		CharCount:  row.CharCount,
		WordCount:  row.WordCount,
		CoverURL:   row.CoverUrl.String,
		Images:     decodeStringList(row.Images),
		NewChars:   decodeStringList(row.NewChars),
		Questions:  decodeStringList(row.Questions),
		Discussion: decodeStringList(row.Discussion),
		UnsafeHits: decodeStringList(row.UnsafeHits),
		Suitable:   row.Suitable,
		Source:     row.Source,
		SourceRef:  row.SourceRef.String,
		SourceURL:  row.SourceUrl.String,
		License:    row.License.String,
		Status:     row.Status,
		CreatedAt:  row.CreatedAt.Time,
	}, nil
}

// GetStoryPair 按任一语言的故事 ID 取双语配对。
func (r *Repository) GetStoryPair(ctx context.Context, storyID uuid.UUID) (StoryPair, error) {
	row, err := r.q.GetStoryPair(ctx, storyID)
	if err != nil {
		return StoryPair{}, err
	}
	return StoryPair{
		ID:       row.ID,
		Slug:     row.Slug,
		AgeGroup: row.AgeGroup.String,
		ZH: Story{
			ID:        row.ZhStoryID,
			Lang:      "zh",
			Title:     row.ZhTitle,
			BodyMD:    row.ZhBodyMd,
			LevelCode: row.ZhLevelCode.String,
		},
		EN: Story{
			ID:        row.EnStoryID,
			Lang:      "en",
			Title:     row.EnTitle,
			BodyMD:    row.EnBodyMd,
			LevelCode: row.EnLevelCode.String,
		},
	}, nil
}

// ---------------------------------------------------------------- 发布动作

// PublishStory 把待审故事置为 published。返回是否发生了状态变更。
func (r *Repository) PublishStory(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.q.PublishStory(ctx, id)
	if err != nil {
		return false, fmt.Errorf("发布故事失败: %w", err)
	}
	return n > 0, nil
}

// RejectStory 把待审故事置为 rejected。
func (r *Repository) RejectStory(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.q.RejectStory(ctx, id)
	if err != nil {
		return false, fmt.Errorf("驳回故事失败: %w", err)
	}
	return n > 0, nil
}

// ErrNotFound 由 service 转成 404。
var ErrNotFound = errors.New("内容不存在")

// ---------------------------------------------------------------- 基准线铺线

// PlanChild 需要铺标准节奏基准线的孩子。
type PlanChild struct {
	ID        uuid.UUID
	StageCode string
	CreatedAt time.Time
}

// PlannedKP 参与铺线的知识点（按学习顺序排列）。
type PlannedKP struct {
	ID           uuid.UUID
	SubjectCode  string
	StageCode    string
	Kind         string
	OrderInStage int
}

// ListPlanChildren 列出全部孩子（含已归档，归档档案的学习线保留）。
func (r *Repository) ListPlanChildren(ctx context.Context) ([]PlanChild, error) {
	rows, err := r.q.ListChildrenForPlan(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询孩子列表失败: %w", err)
	}
	out := make([]PlanChild, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlanChild{
			ID:        row.ID,
			StageCode: row.StageCode.String,
			CreatedAt: row.CreatedAt.Time,
		})
	}
	return out, nil
}

// ListPlannedKPs 按学科取从 fromStage 起的全部知识点，已按学习顺序排好。
func (r *Repository) ListPlannedKPs(ctx context.Context, subjectCode, fromStage string) ([]PlannedKP, error) {
	rows, err := r.q.ListPlannedKPs(ctx, dbgen.ListPlannedKPsParams{
		SubjectCode: subjectCode,
		Code:        fromStage,
	})
	if err != nil {
		return nil, fmt.Errorf("查询知识点失败: %w", err)
	}
	out := make([]PlannedKP, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlannedKP{
			ID:           row.ID,
			SubjectCode:  row.SubjectCode,
			StageCode:    row.StageCode.String,
			Kind:         row.Kind,
			OrderInStage: int(row.OrderInStage),
		})
	}
	return out, nil
}

// CountPlanRows 已有基准线条目数。
func (r *Repository) CountPlanRows(ctx context.Context, childID uuid.UUID) (int64, error) {
	n, err := r.q.CountPlanForChild(ctx, childID)
	if err != nil {
		return 0, fmt.Errorf("统计基准线失败: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------- 转换工具

// IsNoRows 判断 pgx 的「没有数据」错误。
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func decodeStringList(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func uuidPtr(v pgtype.UUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	x := uuid.UUID(v.Bytes)
	return &x
}

func int16Ptr(v pgtype.Int2) *int16 {
	if !v.Valid {
		return nil
	}
	x := v.Int16
	return &x
}
