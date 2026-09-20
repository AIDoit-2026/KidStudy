package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"kidstudy/internal/platform/apperr"
)

// Repository 负责 auth 领域的全部 SQL。上层不感知具体驱动。
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository 构造 auth 仓储。
func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// 唯一约束冲突码：23505 unique_violation。
const pgUniqueViolation = "23505"

// CreateParent 写入家长账号，凭据哈希由调用方算好传入。
func (r *Repository) CreateParent(ctx context.Context, email, phone *string, passwordHash, displayName string) (Parent, error) {
	const q = `
INSERT INTO parents (email, phone, password_hash, display_name)
VALUES ($1, $2, $3, $4)
RETURNING id, email, phone, display_name, status, (pin_hash IS NOT NULL), created_at`

	var p Parent
	err := r.db.QueryRow(ctx, q, email, phone, passwordHash, displayName).Scan(
		&p.ID, &p.Email, &p.Phone, &p.DisplayName, &p.Status, &p.HasPIN, &p.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			// 不指明是邮箱还是手机号重复以外的更多信息，够前端提示即可
			return Parent{}, apperr.Conflict("该邮箱或手机号已被注册")
		}
		return Parent{}, fmt.Errorf("创建家长账号失败: %w", err)
	}
	return p, nil
}

// FindCredentialsByAccount 按邮箱或手机号查登录凭据。未找到返回 pgx.ErrNoRows，由 service 决定对外语义。
func (r *Repository) FindCredentialsByAccount(ctx context.Context, account string) (Credentials, error) {
	const q = `
SELECT id, display_name, status, password_hash, pin_hash
FROM parents
WHERE email = $1 OR phone = $1`

	var c Credentials
	if err := r.db.QueryRow(ctx, q, account).Scan(
		&c.ID, &c.DisplayName, &c.Status, &c.PasswordHash, &c.PINHash,
	); err != nil {
		return Credentials{}, err
	}
	return c, nil
}

// FindByID 按 ID 查家长（用于刷新会话与 /auth/me）。
func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (Parent, error) {
	const q = `
SELECT id, email, phone, display_name, status, (pin_hash IS NOT NULL), created_at
FROM parents WHERE id = $1`

	var p Parent
	err := r.db.QueryRow(ctx, q, id).Scan(
		&p.ID, &p.Email, &p.Phone, &p.DisplayName, &p.Status, &p.HasPIN, &p.CreatedAt,
	)
	if err != nil {
		return Parent{}, err
	}
	return p, nil
}

// EnsureSettings 为新建账号落一行默认家长设置，已存在则不动。
func (r *Repository) EnsureSettings(ctx context.Context, parentID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
INSERT INTO parent_settings (parent_id) VALUES ($1) ON CONFLICT (parent_id) DO NOTHING`, parentID)
	if err != nil {
		return fmt.Errorf("初始化家长设置失败: %w", err)
	}
	return nil
}

// FindPINHash 取 PIN 哈希；未设置时返回 nil。
func (r *Repository) FindPINHash(ctx context.Context, parentID uuid.UUID) (*string, error) {
	var h *string
	if err := r.db.QueryRow(ctx, `SELECT pin_hash FROM parents WHERE id = $1`, parentID).Scan(&h); err != nil {
		return nil, err
	}
	return h, nil
}

// SetPINHash 写入或更新 PIN 哈希。
func (r *Repository) SetPINHash(ctx context.Context, id uuid.UUID, pinHash string) error {
	if _, err := r.db.Exec(ctx, `UPDATE parents SET pin_hash = $2, updated_at = now() WHERE id = $1`, id, pinHash); err != nil {
		return fmt.Errorf("写入 PIN 失败: %w", err)
	}
	return nil
}

// TouchLogin 更新最近登录时间，失败不影响登录主流程。
func (r *Repository) TouchLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE parents SET last_login_at = now() WHERE id = $1`, id)
	return err
}

// InsertRefreshToken 登记刷新令牌哈希。tokenHash 为 SHA-256 摘要，原文不落库。
func (r *Repository) InsertRefreshToken(ctx context.Context, parentID uuid.UUID, tokenHash, ua, ip string, expiresAt time.Time) (uuid.UUID, error) {
	const q = `
INSERT INTO refresh_tokens (parent_id, token_hash, user_agent, ip, expires_at)
VALUES ($1, $2, $3, nullif($4, '')::inet, $5)
RETURNING id`

	var id uuid.UUID
	if err := r.db.QueryRow(ctx, q, parentID, tokenHash, nullable(ua), ip, expiresAt).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("登记刷新令牌失败: %w", err)
	}
	return id, nil
}

// FindUsableRefreshToken 查未撤销且未过期的刷新令牌。
func (r *Repository) FindUsableRefreshToken(ctx context.Context, tokenHash string) (id, parentID uuid.UUID, err error) {
	const q = `
SELECT id, parent_id FROM refresh_tokens
WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`

	err = r.db.QueryRow(ctx, q, tokenHash).Scan(&id, &parentID)
	return id, parentID, err
}

// RotateRefreshToken 在一个事务里完成「旧令牌作废 + 新令牌登记」，
// replaced_by 保留轮换链，便于追溯被盗令牌的扩散路径。
func (r *Repository) RotateRefreshToken(ctx context.Context, oldID, parentID uuid.UUID, newHash, ua, ip string, expiresAt time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var newID uuid.UUID
	if err := tx.QueryRow(ctx, `
INSERT INTO refresh_tokens (parent_id, token_hash, user_agent, ip, expires_at)
VALUES ($1, $2, $3, nullif($4, '')::inet, $5)
RETURNING id`, parentID, newHash, nullable(ua), ip, expiresAt).Scan(&newID); err != nil {
		return fmt.Errorf("登记新刷新令牌失败: %w", err)
	}

	if _, err := tx.Exec(ctx, `
UPDATE refresh_tokens SET revoked_at = now(), replaced_by = $2 WHERE id = $1`, oldID, newID); err != nil {
		return fmt.Errorf("作废旧刷新令牌失败: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交轮换事务失败: %w", err)
	}
	return nil
}

// RevokeRefreshToken 撤销单个令牌（登出用）。重复撤销视为幂等成功。
func (r *Repository) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := r.db.Exec(ctx, `
UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
	return err
}

// RevokeAllForParent 撤销该家长的全部令牌（改密、异常登录后下线所有设备）。
func (r *Repository) RevokeAllForParent(ctx context.Context, parentID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
UPDATE refresh_tokens SET revoked_at = now() WHERE parent_id = $1 AND revoked_at IS NULL`, parentID)
	return err
}

// Audit 记录登录/令牌相关审计事件。
func (r *Repository) Audit(ctx context.Context, parentID *uuid.UUID, event, ip, ua string, success bool) {
	_, _ = r.db.Exec(ctx, `
INSERT INTO login_audit (parent_id, event, ip, ua, success)
VALUES ($1, $2, nullif($3, '')::inet, $4, $5)`, parentID, event, ip, nullable(ua), success)
}

// nullable 把空字符串转成 NULL，避免把空值写进可空的文本列。
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ---------- 扫码登录 ----------

// QRSession 是扫码会话的库内视图。
type QRSession struct {
	ID                uuid.UUID
	Status            string
	CreatorIP         *string
	CreatorUA         *string
	ParentID          *uuid.UUID
	ScanCount         int
	FailedCount       int
	ExchangeHash      *string
	ExchangeExpiresAt *time.Time
	ExpiresAt         time.Time
}

// CreateQRSession 登记二维码会话。库内只存令牌哈希，二维码被截屏也无法反查。
func (r *Repository) CreateQRSession(ctx context.Context, tokenHash, ip, ua string, expiresAt time.Time) (uuid.UUID, error) {
	const q = `
INSERT INTO qr_login_sessions (qr_token, creator_ip, creator_ua, expires_at)
VALUES ($1, nullif($2, '')::inet, $3, $4)
RETURNING id`

	var id uuid.UUID
	if err := r.db.QueryRow(ctx, q, tokenHash, ip, nullable(ua), expiresAt).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("创建扫码会话失败: %w", err)
	}
	return id, nil
}

// FindQRSession 按令牌哈希查会话。
func (r *Repository) FindQRSession(ctx context.Context, tokenHash string) (QRSession, error) {
	const q = `
SELECT id, status, host(creator_ip), coalesce(creator_ua, ''), parent_id,
       scan_count, failed_count, exchange_code_hash, exchange_expires_at, expires_at
FROM qr_login_sessions WHERE qr_token = $1`

	var s QRSession
	err := r.db.QueryRow(ctx, q, tokenHash).Scan(
		&s.ID, &s.Status, &s.CreatorIP, &s.CreatorUA, &s.ParentID,
		&s.ScanCount, &s.FailedCount, &s.ExchangeHash, &s.ExchangeExpiresAt, &s.ExpiresAt,
	)
	if err != nil {
		return QRSession{}, err
	}
	return s, nil
}

// MarkScanned 标记已扫码并累加扫码次数。
func (r *Repository) MarkScanned(ctx context.Context, id, parentID uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
UPDATE qr_login_sessions
SET status = 'scanned', parent_id = $2, scan_count = scan_count + 1
WHERE id = $1 AND status = 'waiting' AND expires_at > now()`, id, parentID)
	if err != nil {
		return fmt.Errorf("更新扫码状态失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errQRNotAvailable
	}
	return nil
}

// ConfirmQR 写入一次性兑换码哈希与其 30 秒有效期。
func (r *Repository) ConfirmQR(ctx context.Context, id, parentID uuid.UUID, codeHash string, expiresAt time.Time) error {
	tag, err := r.db.Exec(ctx, `
UPDATE qr_login_sessions
SET status = 'confirmed', parent_id = $2,
    exchange_code_hash = $3, confirmed_at = now(), exchange_expires_at = $4
WHERE id = $1 AND status IN ('waiting', 'scanned') AND expires_at > now()`, id, parentID, codeHash, expiresAt)
	if err != nil {
		return fmt.Errorf("确认扫码失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errQRNotAvailable
	}
	return nil
}

// MarkExchanged 标记兑换完成（兑换码即焚）。
func (r *Repository) MarkExchanged(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
UPDATE qr_login_sessions SET status = 'exchanged' WHERE id = $1`, id)
	return err
}

// NoteExchangeFailure 累加失败次数，达到阈值后把会话置为 failed。
func (r *Repository) NoteExchangeFailure(ctx context.Context, id uuid.UUID, threshold int) error {
	tag, err := r.db.Exec(ctx, `
UPDATE qr_login_sessions
SET failed_count = failed_count + 1,
    status = CASE WHEN failed_count + 1 >= $2 THEN 'failed' ELSE status END
WHERE id = $1`, id, threshold)
	if err != nil {
		return fmt.Errorf("记录兑换失败失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errQRNotAvailable
	}
	return nil
}

// MarkExpired 显式标记过期（SSE 推送 expired 时调用）。
func (r *Repository) MarkExpired(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
UPDATE qr_login_sessions SET status = 'expired'
WHERE id = $1 AND status IN ('waiting', 'scanned') AND expires_at <= now()`, id)
	return err
}

// CountRecentScans 统计该家长最近一分钟的扫码次数，用于限速。
func (r *Repository) CountRecentScans(ctx context.Context, parentID uuid.UUID, window time.Duration) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
SELECT count(*) FROM qr_login_sessions
WHERE parent_id = $1 AND created_at > now() - ($2 || ' seconds')::interval`,
		parentID, fmt.Sprintf("%.0f", window.Seconds())).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计扫码次数失败: %w", err)
	}
	return n, nil
}

// errQRNotAvailable 表示二维码不在可用状态（已过期/已兑换/状态不符）。
var errQRNotAvailable = errors.New("二维码不可用")

// ErrNoRows 供上层判断「查无记录」，避免 service 直接 import pgx。
var ErrNoRows = pgx.ErrNoRows
