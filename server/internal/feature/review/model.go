// Package review 内容审核队列：采集与导入的内容默认 pending，家长过一遍才上线。
//
// 依赖方向遵循设计文档 §2.2（review → content）：审核通过/驳回会在同一个事务里
// 改写目标内容表的状态，保证「队列状态」与「内容可见性」不会各走各的。
package review

import (
	"time"

	"github.com/google/uuid"
)

// 决策动作。
const (
	DecisionApprove = "approve"
	DecisionReject  = "reject"
)

// Item 是队列里的一条待审内容。
type Item struct {
	ID          uuid.UUID
	ContentType string
	RefTable    string
	RefID       uuid.UUID
	Title       string
	Summary     string
	Source      string
	SourceURL   string
	Hits        []string // 命中不宜词，家长据此判断
	Status      string
	LevelCode   string
	CharCount   int32
	Suitable    bool
	ReviewedAt  *time.Time
	CreatedAt   time.Time
}

// ItemView 队列条目视图。
type ItemView struct {
	ID          string   `json:"id"`
	ContentType string   `json:"content_type"`
	RefTable    string   `json:"ref_table"`
	RefID       string   `json:"ref_id"`
	Title       string   `json:"title,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Source      string   `json:"source,omitempty"`
	SourceURL   string   `json:"source_url,omitempty"`
	Hits        []string `json:"hits"` // 恒返回（空数组表示未命中），前端据此渲染「无命中」
	Status      string   `json:"status"`
	LevelCode   string   `json:"level_code,omitempty"`
	CharCount   int32    `json:"char_count,omitempty"`
	Suitable    bool     `json:"suitable"`
	CreatedAt   string   `json:"created_at"`
	ReviewedAt  string   `json:"reviewed_at,omitempty"`
}

// ToView 转队列条目视图。
func (i Item) ToView() ItemView {
	v := ItemView{
		ID:          i.ID.String(),
		ContentType: i.ContentType,
		RefTable:    i.RefTable,
		RefID:       i.RefID.String(),
		Title:       i.Title,
		Summary:     i.Summary,
		Source:      i.Source,
		SourceURL:   i.SourceURL,
		Hits:        i.Hits,
		Status:      i.Status,
		LevelCode:   i.LevelCode,
		CharCount:   i.CharCount,
		Suitable:    i.Suitable,
		CreatedAt:   i.CreatedAt.UTC().Format(time.RFC3339),
	}
	if i.ReviewedAt != nil {
		v.ReviewedAt = i.ReviewedAt.UTC().Format(time.RFC3339)
	}
	return v
}

// Decision 是审核结果。
type Decision struct {
	ID          uuid.UUID `json:"id"`
	ContentType string    `json:"content_type"`
	Status      string    `json:"status"`
}

// BatchResult 批量审核结果。
type BatchResult struct {
	Requested int `json:"requested"`
	Applied   int `json:"applied"`
	Skipped   int `json:"skipped"` // 已被处理过或不存在
}
