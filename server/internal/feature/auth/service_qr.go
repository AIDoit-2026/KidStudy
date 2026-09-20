package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
	platformauth "kidstudy/internal/platform/auth"
)

// 扫码登录阈值。
const (
	maxQRScansPerMinute = 5 // 每家长每分钟扫码上限
	maxExchangeFailures = 5 // 同一二维码兑换失败上限
	scanWindow          = time.Minute
)

// 扫码接口的统一错误：不告诉对方二维码处于哪个具体状态。
var errQRUnusable = apperr.BadRequest("二维码已失效，请刷新后重试")

// QRStatus 是对 SSE 可见的会话快照。
type QRStatus struct {
	SessionID uuid.UUID
	Status    string
	ExpiresAt time.Time
}

// StartQR 生成二维码令牌。端点匿名可用——还没有登录，这正是它的用途。
func (s *Service) StartQR(ctx context.Context, ip, ua string) (QRStartResult, error) {
	token, tokenHash, err := platformauth.NewOpaqueToken()
	if err != nil {
		return QRStartResult{}, apperr.Internal(err)
	}

	now := time.Now()
	expiresAt := now.Add(s.cfg.QRCodeTTL)

	id, err := s.repo.CreateQRSession(ctx, tokenHash, ip, ua, expiresAt)
	if err != nil {
		return QRStartResult{}, apperr.Internal(err)
	}
	s.log.Debug("生成扫码二维码", "session_id", id, "ttl", s.cfg.QRCodeTTL.String())

	return QRStartResult{
		QRToken:   token,
		DeepLink:  strings.TrimRight(s.cfg.BaseURL, "/") + "/qr-confirm?token=" + token,
		ExpiresIn: int64(expiresAt.Sub(now).Seconds()),
	}, nil
}

// LookupQR 供 SSE 建立连接前确认二维码有效并拿到过期时间。
func (s *Service) LookupQR(ctx context.Context, qrToken string) (QRStatus, error) {
	sess, err := s.lookup(ctx, qrToken)
	if err != nil {
		return QRStatus{}, err
	}
	if time.Now().After(sess.ExpiresAt) {
		return QRStatus{}, apperr.NotFound("二维码已过期")
	}
	return QRStatus{SessionID: sess.ID, Status: sess.Status, ExpiresAt: sess.ExpiresAt}, nil
}

// SubscribeQR 订阅会话事件。返回取消函数，由 HTTP 层在连接断开时调用。
func (s *Service) SubscribeQR(sessionID uuid.UUID) (<-chan QREvent, func()) {
	return s.qr.Subscribe(sessionID.String())
}

// MarkExpired 标记二维码过期。由 SSE 在过期时调用，失败不影响连接收尾。
func (s *Service) MarkExpired(ctx context.Context, sessionID uuid.UUID) error {
	return s.repo.MarkExpired(ctx, sessionID)
}

// ScanQR 手机端扫码。必须已登录；记录扫码者并把事件推给桌面端。
func (s *Service) ScanQR(ctx context.Context, parentID uuid.UUID, req QRScanRequest, ip, ua string) (QRScanResult, error) {
	if err := req.Validate(); err != nil {
		return QRScanResult{}, err
	}
	sess, err := s.lookup(ctx, req.QRToken)
	if err != nil {
		return QRScanResult{}, err
	}

	// 每家长每分钟扫码上限，防止拿二维码刷接口试探
	n, err := s.repo.CountRecentScans(ctx, parentID, scanWindow)
	if err != nil {
		return QRScanResult{}, apperr.Internal(err)
	}
	if n >= maxQRScansPerMinute {
		return QRScanResult{}, apperr.RateLimited("扫码过于频繁，请稍后再试")
	}

	if err := s.repo.MarkScanned(ctx, sess.ID, parentID); err != nil {
		if errors.Is(err, errQRNotAvailable) {
			return QRScanResult{}, errQRUnusable
		}
		return QRScanResult{}, apperr.Internal(err)
	}
	s.repo.Audit(ctx, &parentID, "qr_scan", ip, ua, true)

	payload := QRScannedPayload{
		Status:     "scanned",
		DeviceHint: deviceHint(deref(sess.CreatorUA)),
		IPPrefix:   ipPrefix(deref(sess.CreatorIP)),
	}
	s.qr.Publish(sess.ID.String(), QREvent{Name: "scanned", Data: payload})

	return QRScanResult(payload), nil
}

// ConfirmQR 手机端确认。签发一次性兑换码并推送给桌面端。
func (s *Service) ConfirmQR(ctx context.Context, parentID uuid.UUID, req QRScanRequest, ip, ua string) (QRConfirmResult, error) {
	if err := req.Validate(); err != nil {
		return QRConfirmResult{}, err
	}
	sess, err := s.lookup(ctx, req.QRToken)
	if err != nil {
		return QRConfirmResult{}, err
	}
	// 只允许扫码人自己确认，避免别人扫到二维码后替他登录
	if sess.ParentID != nil && *sess.ParentID != parentID {
		return QRConfirmResult{}, apperr.Forbidden("该二维码已被其他账号扫码")
	}

	code, codeHash, err := platformauth.NewOpaqueToken()
	if err != nil {
		return QRConfirmResult{}, apperr.Internal(err)
	}
	now := time.Now()
	expiresAt := now.Add(s.cfg.ExchangeCodeTTL)

	if err := s.repo.ConfirmQR(ctx, sess.ID, parentID, codeHash, expiresAt); err != nil {
		if errors.Is(err, errQRNotAvailable) {
			return QRConfirmResult{}, errQRUnusable
		}
		return QRConfirmResult{}, apperr.Internal(err)
	}
	s.repo.Audit(ctx, &parentID, "qr_confirm", ip, ua, true)

	s.qr.Publish(sess.ID.String(), QREvent{Name: "confirmed", Data: QRConfirmedPayload{
		Status:       "confirmed",
		ExchangeCode: code,
		ExpiresIn:    int64(expiresAt.Sub(now).Seconds()),
	}})

	return QRConfirmResult{Status: "confirmed"}, nil
}

// ExchangeQR 桌面端用兑换码换令牌。兑换码一次性，错了累计到 5 次即锁死本次二维码。
func (s *Service) ExchangeQR(ctx context.Context, req QRExchangeRequest, ip, ua string) (Token, error) {
	if err := req.Validate(); err != nil {
		return Token{}, err
	}
	sess, err := s.lookup(ctx, req.QRToken)
	if err != nil {
		return Token{}, err
	}

	fail := func(reason string) (Token, error) {
		// 失败计数先在库里加 1，达到阈值本次二维码直接作废
		if nerr := s.repo.NoteExchangeFailure(ctx, sess.ID, maxExchangeFailures); nerr != nil {
			s.log.Warn("记录兑换失败失败", "session_id", sess.ID, "error", nerr)
		}
		s.qr.Publish(sess.ID.String(), QREvent{Name: "failed", Data: QRExpiredPayload{Status: "failed", Reason: reason}})
		s.repo.Audit(ctx, sess.ParentID, "qr_exchange", ip, ua, false)
		return Token{}, apperr.Unauthorized("登录失败，请重新获取二维码")
	}

	switch {
	case sess.Status == "exchanged":
		return fail("已兑换")
	case sess.Status == "failed":
		return fail("尝试次数过多")
	case sess.Status != "confirmed":
		return fail("状态不正确")
	case sess.ExchangeHash == nil:
		return fail("未签发兑换码")
	case sess.ExchangeExpiresAt == nil || time.Now().After(*sess.ExchangeExpiresAt):
		return fail("兑换码已过期")
	case subtle.ConstantTimeCompare([]byte(*sess.ExchangeHash), []byte(platformauth.HashToken(req.ExchangeCode))) != 1:
		return fail("兑换码不正确")
	case sess.ParentID == nil:
		return fail("缺少扫码账号")
	}

	p, err := s.repo.FindByID(ctx, *sess.ParentID)
	if err != nil {
		return Token{}, apperr.Internal(err)
	}
	if p.Status != "active" {
		return Token{}, apperr.Forbidden("账号已被停用，请联系管理员")
	}

	tok, err := s.rotate(ctx, p, nil, ip, ua)
	if err != nil {
		return Token{}, err
	}
	if err := s.repo.MarkExchanged(ctx, sess.ID); err != nil {
		return Token{}, apperr.Internal(err)
	}
	s.repo.Audit(ctx, sess.ParentID, "qr_exchange", ip, ua, true)
	return tok, nil
}

// lookup 按明文二维码令牌查会话，对外统一表现为「不存在」。
func (s *Service) lookup(ctx context.Context, qrToken string) (QRSession, error) {
	if strings.TrimSpace(qrToken) == "" {
		return QRSession{}, errQRUnusable
	}
	sess, err := s.repo.FindQRSession(ctx, platformauth.HashToken(qrToken))
	if err != nil {
		if errors.Is(err, ErrNoRows) {
			return QRSession{}, errQRUnusable
		}
		return QRSession{}, apperr.Internal(err)
	}
	return sess, nil
}

// deviceHint 从 UA 里提取一句人话，够家长判断「是不是我的设备」即可。
func deviceHint(ua string) string {
	switch {
	case ua == "":
		return "未知设备"
	case containsFold(ua, "iPhone"):
		return "iPhone"
	case containsFold(ua, "iPad"):
		return "iPad"
	case containsFold(ua, "Android"):
		return "Android 设备"
	case containsFold(ua, "Macintosh"):
		return "Mac"
	case containsFold(ua, "Windows"):
		return "Windows 电脑"
	default:
		return "未知设备"
	}
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// ipPrefix 只保留 IPv4 前三段（或 IPv6 前两段），够定位又不暴露完整地址。
func ipPrefix(ip string) string {
	if ip == "" {
		return ""
	}
	if v4 := net.ParseIP(ip).To4(); v4 != nil {
		return strings.Join(strings.Split(v4.String(), ".")[:3], ".") + ".*"
	}
	parts := strings.Split(ip, ":")
	if len(parts) > 2 {
		return strings.Join(parts[:2], ":") + "::*"
	}
	return "*"
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
