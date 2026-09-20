package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"kidstudy/internal/config"
	"kidstudy/internal/platform/apperr"
	platformauth "kidstudy/internal/platform/auth"
)

// 登录失败保护阈值：同一账号连续失败 5 次后锁定 15 分钟。
const (
	maxLoginFailures = 5
	loginLockFor     = 15 * time.Minute
	failureWindow    = 30 * time.Minute
)

// Service 承载 auth 领域规则：注册、登录、令牌轮换、PIN 校验。
// 刻意不 import net/http —— Cookie 下发等 HTTP 关注点全部留在 handler。
type Service struct {
	repo   *Repository
	tokens *platformauth.TokenService
	cfg    config.Config
	log    *slog.Logger
	qr     *QRHub

	mu       sync.Mutex
	failures map[string]failureState
}

type failureState struct {
	count      int
	lastFailed time.Time
	lockedTill time.Time
}

// NewService 构造 auth 服务。扫码事件中心在内部创建，HTTP 层按需订阅即可。
func NewService(repo *Repository, tokens *platformauth.TokenService, cfg config.Config, log *slog.Logger) *Service {
	return &Service{
		repo:     repo,
		tokens:   tokens,
		cfg:      cfg,
		log:      log,
		qr:       NewQRHub(),
		failures: make(map[string]failureState),
	}
}

// Token 是签发完成后交给上层的令牌组合。
// RefreshCipher 只在服务端与 HttpOnly Cookie 之间流转，绝不写进响应体。
type Token struct {
	Session
	RefreshCipher   string
	RefreshExpireAt time.Time
}

// Register 注册家长账号：先验邀请码（未开放公开注册），再落库并签发令牌。
func (s *Service) Register(ctx context.Context, req RegisterRequest, ip, ua string) (Token, error) {
	if err := req.Validate(); err != nil {
		return Token{}, err
	}
	if err := s.checkInviteCode(req.InviteCode); err != nil {
		return Token{}, err
	}

	email, phone := normalizeContact(req.Email, req.Phone)

	hash, err := platformauth.HashPassword(req.Password)
	if err != nil {
		return Token{}, apperr.Internal(err)
	}

	p, err := s.repo.CreateParent(ctx, email, phone, hash, strings.TrimSpace(req.DisplayName))
	if err != nil {
		return Token{}, apperr.From(err)
	}
	// 默认设置行与账号同生共死，避免后续读设置时还要处理「不存在」分支
	if err := s.repo.EnsureSettings(ctx, p.ID); err != nil {
		return Token{}, apperr.Internal(err)
	}

	s.repo.Audit(ctx, &p.ID, "register", ip, ua, true)
	return s.issue(ctx, p, ip, ua)
}

// Login 校验账号口令，成功则签发令牌并轮换 Refresh。
func (s *Service) Login(ctx context.Context, req LoginRequest, ip, ua string) (Token, error) {
	if err := req.Validate(); err != nil {
		return Token{}, err
	}

	account := strings.TrimSpace(req.Account)
	if err := s.ensureNotLocked(account); err != nil {
		return Token{}, err
	}

	cred, err := s.repo.FindCredentialsByAccount(ctx, account)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 账号不存在与口令错误返回同一句文案，避免账号枚举
			s.noteFailure(account)
			s.repo.Audit(ctx, nil, "login", ip, ua, false)
			return Token{}, errInvalidCredentials
		}
		return Token{}, apperr.Internal(err)
	}

	ok, err := platformauth.VerifyPassword(cred.PasswordHash, req.Password)
	if err != nil {
		s.log.Error("口令哈希校验异常", "parent_id", cred.ID, "error", err)
		return Token{}, apperr.Internal(err)
	}
	if !ok {
		s.noteFailure(account)
		s.repo.Audit(ctx, &cred.ID, "login", ip, ua, false)
		return Token{}, errInvalidCredentials
	}
	if cred.Status != "active" {
		s.repo.Audit(ctx, &cred.ID, "login", ip, ua, false)
		return Token{}, apperr.Forbidden("账号已被停用，请联系管理员")
	}

	s.clearFailures(account)
	_ = s.repo.TouchLogin(ctx, cred.ID)
	s.repo.Audit(ctx, &cred.ID, "login", ip, ua, true)

	p, err := s.repo.FindByID(ctx, cred.ID)
	if err != nil {
		return Token{}, apperr.Internal(err)
	}
	return s.issue(ctx, p, ip, ua)
}

// Refresh 用 Refresh 令牌换新的一对令牌：旧的立即作废，新的入库。
func (s *Service) Refresh(ctx context.Context, refreshCipher, ip, ua string) (Token, error) {
	if strings.TrimSpace(refreshCipher) == "" {
		return Token{}, apperr.Unauthorized("登录已失效，请重新登录")
	}

	hash := platformauth.HashToken(refreshCipher)
	oldID, parentID, err := s.repo.FindUsableRefreshToken(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 令牌不存在/已撤销/已过期：统一当成失效，不泄漏原因
			return Token{}, apperr.Unauthorized("登录已失效，请重新登录")
		}
		return Token{}, apperr.Internal(err)
	}

	p, err := s.repo.FindByID(ctx, parentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Token{}, apperr.Unauthorized("登录已失效，请重新登录")
		}
		return Token{}, apperr.Internal(err)
	}
	if p.Status != "active" {
		return Token{}, apperr.Forbidden("账号已被停用，请联系管理员")
	}

	return s.rotate(ctx, p, &oldID, ip, ua)
}

// Logout 撤销当前 Refresh 令牌（单设备登出）。
func (s *Service) Logout(ctx context.Context, parentID uuid.UUID, refreshCipher, ip, ua string) error {
	if strings.TrimSpace(refreshCipher) != "" {
		if err := s.repo.RevokeRefreshToken(ctx, platformauth.HashToken(refreshCipher)); err != nil {
			return apperr.Internal(err)
		}
	}
	s.repo.Audit(ctx, &parentID, "logout", ip, ua, true)
	return nil
}

// SetPIN 设置或更新 PIN。
func (s *Service) SetPIN(ctx context.Context, parentID uuid.UUID, req SetPINRequest, ip, ua string) error {
	if err := req.Validate(); err != nil {
		return err
	}
	hash, err := platformauth.HashPassword(strings.TrimSpace(req.PIN))
	if err != nil {
		return apperr.Internal(err)
	}
	if err := s.repo.SetPINHash(ctx, parentID, hash); err != nil {
		return apperr.Internal(err)
	}
	s.repo.Audit(ctx, &parentID, "pin_set", ip, ua, true)
	return nil
}

// VerifyPIN 校验 PIN，通过后返回 5 分钟有效的解锁令牌。
func (s *Service) VerifyPIN(ctx context.Context, parentID uuid.UUID, req VerifyPINRequest, ip, ua string) (UnlockResult, error) {
	if err := req.Validate(); err != nil {
		return UnlockResult{}, err
	}

	cred, err := s.repo.FindPINHash(ctx, parentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UnlockResult{}, apperr.NotFound("尚未设置 PIN")
		}
		return UnlockResult{}, apperr.Internal(err)
	}
	if cred == nil {
		return UnlockResult{}, apperr.NotFound("尚未设置 PIN")
	}

	ok, err := platformauth.VerifyPassword(*cred, strings.TrimSpace(req.PIN))
	if err != nil {
		return UnlockResult{}, apperr.Internal(err)
	}
	if !ok {
		s.repo.Audit(ctx, &parentID, "pin_verify", ip, ua, false)
		return UnlockResult{}, apperr.Unauthorized("PIN 不正确")
	}

	token, expiresAt, err := s.tokens.IssueUnlockToken(parentID.String())
	if err != nil {
		return UnlockResult{}, apperr.Internal(err)
	}
	s.repo.Audit(ctx, &parentID, "pin_verify", ip, ua, true)

	return UnlockResult{
		UnlockToken: token,
		ExpiresIn:   int64(time.Until(expiresAt).Seconds()),
	}, nil
}

// Me 返回当前家长信息。
func (s *Service) Me(ctx context.Context, parentID uuid.UUID) (ParentView, error) {
	p, err := s.repo.FindByID(ctx, parentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ParentView{}, apperr.NotFound("账号不存在")
		}
		return ParentView{}, apperr.Internal(err)
	}
	return p.ToView(), nil
}

// issue 签发全新的一对令牌（注册与登录走这里）。
func (s *Service) issue(ctx context.Context, p Parent, ip, ua string) (Token, error) {
	return s.rotate(ctx, p, nil, ip, ua)
}

// rotate 生成并登记新 Refresh；oldID 非空时把旧令牌标记为被替换。
func (s *Service) rotate(ctx context.Context, p Parent, oldID *uuid.UUID, ip, ua string) (Token, error) {
	access, accessExp, err := s.tokens.IssueAccessToken(p.ID.String())
	if err != nil {
		return Token{}, apperr.Internal(err)
	}
	cipher, cipherHash, err := platformauth.NewOpaqueToken()
	if err != nil {
		return Token{}, apperr.Internal(err)
	}

	now := time.Now()
	expiresAt := now.Add(s.cfg.RefreshTokenTTL)

	if oldID != nil {
		if err := s.repo.RotateRefreshToken(ctx, *oldID, p.ID, cipherHash, ua, ip, expiresAt); err != nil {
			return Token{}, apperr.Internal(err)
		}
	} else {
		if _, err := s.repo.InsertRefreshToken(ctx, p.ID, cipherHash, ua, ip, expiresAt); err != nil {
			return Token{}, apperr.Internal(err)
		}
	}

	return Token{
		Session: Session{
			AccessToken: access,
			ExpiresIn:   int64(accessExp.Sub(now).Seconds()),
			Parent:      p.ToView(),
		},
		RefreshCipher:   cipher,
		RefreshExpireAt: expiresAt,
	}, nil
}

// checkInviteCode 关闭公开注册：邀请码缺失或不符一律拒绝。
func (s *Service) checkInviteCode(code string) error {
	want := s.cfg.BootstrapInviteCode
	if want == "" {
		s.log.Warn("未配置 BOOTSTRAP_INVITE_CODE，注册被拒绝（如需建号请先配置邀请码）")
		return apperr.Forbidden("注册已关闭，请联系管理员获取邀请码")
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(strings.TrimSpace(code))) != 1 {
		return apperr.Forbidden("邀请码不正确")
	}
	return nil
}

// 统一文案：不区分账号是否存在与口令是否正确。
var errInvalidCredentials = apperr.Unauthorized("账号或密码不正确")

// noteFailure 记录一次登录失败并按阈值锁定。
func (s *Service) noteFailure(account string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := strings.ToLower(account)
	st := s.failures[key]
	now := time.Now()
	if now.Sub(st.lastFailed) > failureWindow {
		st.count = 0
	}
	st.count++
	st.lastFailed = now
	if st.count >= maxLoginFailures {
		st.lockedTill = now.Add(loginLockFor)
		s.log.Warn("账号因连续登录失败被临时锁定", "account", key, "minutes", int(loginLockFor.Minutes()))
	}
	s.failures[key] = st
}

func (s *Service) clearFailures(account string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, strings.ToLower(account))
}

func (s *Service) ensureNotLocked(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := strings.ToLower(account)
	st, ok := s.failures[key]
	if !ok {
		return nil
	}
	if st.lockedTill.IsZero() {
		return nil
	}
	if time.Now().Before(st.lockedTill) {
		return apperr.RateLimited("登录失败次数过多，请稍后再试")
	}
	// 锁定已到期：清掉状态让他重试
	delete(s.failures, key)
	return nil
}

// normalizeContact 把空联系方式转成 NULL，满足「邮箱/手机至少其一」的列约束。
func normalizeContact(email, phone string) (*string, *string) {
	var e, p *string
	if email = strings.TrimSpace(email); email != "" {
		e = &email
	}
	if phone = strings.TrimSpace(phone); phone != "" {
		p = &phone
	}
	return e, p
}
