package children

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"kidstudy/internal/platform/apperr"
)

// Repository 负责 children 的全部 SQL。
//
// 所有查询都带上 parent_id：越权访问在 SQL 层就被拦掉，
// 不依赖上层记得补这一句。
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository 构造 children 仓储。
func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

const childColumns = `id, parent_id, nickname, avatar_id, birth_ym, stage_code, active, settings, created_at, updated_at`

// ListByParent 列出家长名下档案。activeOnly 为真时只返回未归档的。
func (r *Repository) ListByParent(ctx context.Context, parentID uuid.UUID, activeOnly bool) ([]Child, error) {
	q := `SELECT ` + childColumns + ` FROM children WHERE parent_id = $1`
	if activeOnly {
		q += ` AND active`
	}
	q += ` ORDER BY created_at`

	rows, err := r.db.Query(ctx, q, parentID)
	if err != nil {
		return nil, fmt.Errorf("查询孩子档案失败: %w", err)
	}
	defer rows.Close()

	var list []Child
	for rows.Next() {
		c, err := scanChild(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// Get 取单个档案（连同家长归属校验）。
func (r *Repository) Get(ctx context.Context, parentID, childID uuid.UUID) (Child, error) {
	row := r.db.QueryRow(ctx, `SELECT `+childColumns+` FROM children WHERE id = $1 AND parent_id = $2`, childID, parentID)
	c, err := scanChild(row)
	if err != nil {
		return Child{}, err // pgx.ErrNoRows 原样交给 service 转 404
	}
	return c, nil
}

// Create 新建档案。
func (r *Repository) Create(ctx context.Context, c Child) (Child, error) {
	const q = `
INSERT INTO children (parent_id, nickname, avatar_id, birth_ym, stage_code, settings)
VALUES ($1, $2, $3, $4, $5, COALESCE($6, '{}'::jsonb))
RETURNING ` + childColumns

	settings, err := marshalSettings(c.Settings)
	if err != nil {
		return Child{}, err
	}

	row := r.db.QueryRow(ctx, q,
		c.ParentID, c.Nickname, c.AvatarID, nullIfEmpty(c.BirthYM), nullIfEmpty(c.StageCode), settings,
	)
	created, err := scanChild(row)
	if err != nil {
		return Child{}, fmt.Errorf("创建孩子档案失败: %w", err)
	}
	return created, nil
}

// Update 局部更新档案，未提供的字段保持原值。
func (r *Repository) Update(ctx context.Context, parentID, childID uuid.UUID, req UpdateRequest) (Child, error) {
	// COALESCE 让「未传」与「保持原值」在 SQL 层统一处理，避免拼动态 SQL
	const q = `
UPDATE children SET
    nickname   = COALESCE($3, nickname),
    avatar_id  = COALESCE($4, avatar_id),
    birth_ym   = COALESCE($5, birth_ym),
    stage_code = COALESCE($6, stage_code),
    active     = COALESCE($7, active),
    settings   = COALESCE($8, settings),
    updated_at = now()
WHERE id = $1 AND parent_id = $2
RETURNING ` + childColumns

	settings, err := marshalSettings(req.Settings)
	if err != nil {
		return Child{}, err
	}

	row := r.db.QueryRow(ctx, q,
		childID, parentID,
		req.Nickname, req.AvatarID, req.BirthYM, req.StageCode, req.Active, settings,
	)
	updated, err := scanChild(row)
	if err != nil {
		return Child{}, fmt.Errorf("更新孩子档案失败: %w", err)
	}
	return updated, nil
}

// Archive 归档档案（软删除）。学习记录随之保留，只是档案对孩子端不可见。
func (r *Repository) Archive(ctx context.Context, parentID, childID uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
UPDATE children SET active = false, updated_at = now() WHERE id = $1 AND parent_id = $2`, childID, parentID)
	if err != nil {
		return fmt.Errorf("归档孩子档案失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// StageExists 校验阶段是否存在。M2 导入 stages 前，任何 stage_code 都不存在。
func (r *Repository) StageExists(ctx context.Context, code string) (bool, error) {
	var ok bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stages WHERE code = $1)`, code).Scan(&ok); err != nil {
		return false, fmt.Errorf("校验学习阶段失败: %w", err)
	}
	return ok, nil
}

// ErrNotFound 由 service 转成 404，避免上层感知 pgx。
var errNotFound = apperr.NotFound("孩子档案不存在或已归档")

// rowScanner 同时满足 *pgx.Row 的行与 Rows，便于 Scan 逻辑复用。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanChild(row rowScanner) (Child, error) {
	var (
		c        Child
		settings []byte
	)
	err := row.Scan(
		&c.ID, &c.ParentID, &c.Nickname, &c.AvatarID, &c.BirthYM, &c.StageCode,
		&c.Active, &settings, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return Child{}, err
	}
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &c.Settings); err != nil {
			return Child{}, fmt.Errorf("解析档案设置失败: %w", err)
		}
	}
	return c, nil
}

func marshalSettings(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, apperr.BadRequest("档案设置不是合法的 JSON 对象")
	}
	return b, nil
}

func nullIfEmpty(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}
