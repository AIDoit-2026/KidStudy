package review

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"kidstudy/internal/dbgen"
)

// Repository 负责审核队列的读写。
//
// 「队列状态 + 目标内容状态」在同一个事务里改，避免出现队列显示已通过、
// 但孩子端还是看不到（或反过来）的半吊子状态。
type Repository struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewRepository 构造审核仓储。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: dbgen.New(pool)}
}

// List 分页列出队列条目。
func (r *Repository) List(ctx context.Context, status, contentType string, offset, limit int) ([]Item, int64, error) {
	total, err := r.q.CountReviewQueue(ctx, dbgen.CountReviewQueueParams{
		Status:      status,
		ContentType: contentType,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("统计审核队列失败: %w", err)
	}
	if total == 0 {
		return []Item{}, 0, nil
	}

	rows, err := r.q.ListReviewQueue(ctx, dbgen.ListReviewQueueParams{
		Status:      status,
		ContentType: contentType,
		Off:         int32(offset),
		Lim:         int32(limit),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("查询审核队列失败: %w", err)
	}

	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		item := Item{
			ID:          row.ID,
			ContentType: row.ContentType,
			RefTable:    row.RefTable,
			RefID:       row.RefID,
			Title:       row.Title.String,
			Summary:     row.Summary.String,
			Source:      row.Source.String,
			SourceURL:   row.SourceUrl.String,
			Hits:        decodeHits(row.Hits),
			Status:      row.Status,
			LevelCode:   row.LevelCode.String,
			CharCount:   row.CharCount.Int32,
			Suitable:    row.Suitable.Bool,
			CreatedAt:   row.CreatedAt.Time,
		}
		if row.ReviewedAt.Valid {
			t := row.ReviewedAt.Time
			item.ReviewedAt = &t
		}
		out = append(out, item)
	}
	return out, total, nil
}

// Decide 处理一条队列条目。approve 为真表示通过。
//
// 返回 false 表示条目不存在或已被处理过（幂等，重复点击不会报错）。
func (r *Repository) Decide(ctx context.Context, id, reviewerID uuid.UUID, approve bool) (Decision, bool, error) {
	newStatus := "rejected"
	contentStatus := "rejected"
	if approve {
		newStatus = "approved"
		contentStatus = "published"
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Decision{}, false, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row, err := dbgen.New(tx).DecideReview(ctx, dbgen.DecideReviewParams{
		ID:         id,
		Status:     "pending", // 只有仍处于 pending 的条目能被处理
		Status_2:   newStatus,
		ReviewerID: pgtype.UUID{Bytes: reviewerID, Valid: true},
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return Decision{}, false, nil
		}
		return Decision{}, false, fmt.Errorf("更新审核状态失败: %w", err)
	}

	sql, err := targetSQL(row.RefTable)
	if err != nil {
		return Decision{}, false, err
	}
	if _, err := tx.Exec(ctx, sql, row.RefID, contentStatus); err != nil {
		return Decision{}, false, fmt.Errorf("更新内容状态失败: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Decision{}, false, fmt.Errorf("提交审核结果失败: %w", err)
	}
	return Decision{ID: row.ID, ContentType: row.ContentType, Status: row.Status}, true, nil
}

// targetSQL 按内容所属表给出状态改写语句。
//
// 目前只服务 stories；将来接入 hanzi_words / en_words 的审核时在这里扩一行即可。
// 未知表直接报错，不静默跳过 —— 否则会出现「队列已通过但内容没动」的假象。
func targetSQL(refTable string) (string, error) {
	switch refTable {
	case "stories":
		return `UPDATE stories SET status = $2, updated_at = now() WHERE id = $1 AND status = 'pending'`, nil
	default:
		return "", fmt.Errorf("审核队列引用了未知内容表 %q", refTable)
	}
}

func decodeHits(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
