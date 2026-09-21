package parent

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kidstudy/internal/dbgen"
)

// Repository 读写 parent_settings。
type Repository struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewRepository 用连接池构造。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: dbgen.New(pool)}
}

// Get 取设置；没有记录返回 nil（由 service 兜默认值）。
func (r *Repository) Get(ctx context.Context, parentID uuid.UUID) (*Settings, error) {
	row, err := r.q.ParentSettingsFull(ctx, parentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &Settings{
		DailyLimitMin:        int(row.DailyLimitMin),
		SessionLimitMin:      int(row.SessionLimitMin),
		RestIntervalMin:      int(row.RestIntervalMin),
		RequireParentConfirm: row.RequireParentConfirm,
		PaceMode:             row.PaceMode,
		CompareChildren:      row.CompareChildren,
	}, nil
}

// Upsert 写入设置并返回落库后的值。
func (r *Repository) Upsert(ctx context.Context, parentID uuid.UUID, s Settings) (Settings, error) {
	row, err := r.q.UpsertParentSettings(ctx, dbgen.UpsertParentSettingsParams{
		ParentID:             parentID,
		DailyLimitMin:        int32(s.DailyLimitMin),
		SessionLimitMin:      int32(s.SessionLimitMin),
		RestIntervalMin:      int32(s.RestIntervalMin),
		RequireParentConfirm: s.RequireParentConfirm,
		PaceMode:             s.PaceMode,
		CompareChildren:      s.CompareChildren,
	})
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		DailyLimitMin:        int(row.DailyLimitMin),
		SessionLimitMin:      int(row.SessionLimitMin),
		RestIntervalMin:      int(row.RestIntervalMin),
		RequireParentConfirm: row.RequireParentConfirm,
		PaceMode:             row.PaceMode,
		CompareChildren:      row.CompareChildren,
		UpdatedAt:            time.Now(),
	}, nil
}
