// Package auth 实现家长身份相关业务：注册（邀请码）、登录、令牌刷新与登出、PIN 二次校验。
//
// 分层：handler 只解析 HTTP；service 承载规则不 import net/http；repository 只管 SQL。
package auth

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
)

var (
	emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	phoneRe = regexp.MustCompile(`^1[3-9]\d{9}$`)
	pinRe   = regexp.MustCompile(`^\d{4,6}$`)
)

// Parent 是家长领域模型（不含任何凭据哈希）。
type Parent struct {
	ID          uuid.UUID
	Email       *string
	Phone       *string
	DisplayName string
	Status      string
	HasPIN      bool
	CreatedAt   time.Time
}

// Credentials 仅用于登录校验，包含哈希，不应对外序列化。
type Credentials struct {
	ID           uuid.UUID
	DisplayName  string
	Status       string
	PasswordHash string
	PINHash      *string
}

// ParentView 是对外返回的家长视图，刻意不含任何内部字段。
type ParentView struct {
	ID          string  `json:"id"`
	Email       *string `json:"email,omitempty"`
	Phone       *string `json:"phone,omitempty"`
	DisplayName string  `json:"display_name"`
	HasPIN      bool    `json:"has_pin"`
	CreatedAt   string  `json:"created_at"`
}

// ToView 转成对外视图。
func (p Parent) ToView() ParentView {
	return ParentView{
		ID:          p.ID.String(),
		Email:       p.Email,
		Phone:       p.Phone,
		DisplayName: p.DisplayName,
		HasPIN:      p.HasPIN,
		CreatedAt:   p.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// Session 是登录成功后返回给客户端的令牌组合。
// Refresh 令牌通过 HttpOnly Cookie 下发，不放进响应体，避免被 JS 读取。
type Session struct {
	AccessToken string     `json:"access_token"`
	ExpiresIn   int64      `json:"expires_in"`
	Parent      ParentView `json:"parent"`
}

// RegisterRequest 注册请求。邮箱与手机号至少其一。
type RegisterRequest struct {
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	InviteCode  string `json:"invite_code"`
}

// Validate 校验注册参数，字段级错误通过 details 返回给前端表单。
func (r RegisterRequest) Validate() error {
	var details []map[string]string
	add := func(field, reason string) {
		details = append(details, map[string]string{"field": field, "reason": reason})
	}

	email := strings.TrimSpace(r.Email)
	phone := strings.TrimSpace(r.Phone)
	// 邮箱与手机至少提供一个，这是数据层约束，在边界处先拦一次
	if email == "" && phone == "" {
		add("email", "邮箱与手机号至少填写一个")
	}
	if email != "" && !emailRe.MatchString(email) {
		add("email", "邮箱格式不正确")
	}
	if phone != "" && !phoneRe.MatchString(phone) {
		add("phone", "手机号格式不正确")
	}
	if len(r.Password) < 8 {
		add("password", "密码至少 8 位")
	}
	if len(strings.TrimSpace(r.DisplayName)) == 0 {
		add("display_name", "昵称不能为空")
	}
	if len(strings.TrimSpace(r.InviteCode)) == 0 {
		add("invite_code", "邀请码不能为空")
	}

	if len(details) > 0 {
		return apperr.ValidationFailed("注册信息有误", details)
	}
	return nil
}

// LoginRequest 登录请求，account 可以是邮箱或手机号。
type LoginRequest struct {
	Account  string `json:"account"`
	Password string `json:"password"`
}

// Validate 校验登录参数。此处不校验密码强度，只拦必填，避免泄漏账号是否存在之外的多余信息。
func (r LoginRequest) Validate() error {
	var details []map[string]string
	if strings.TrimSpace(r.Account) == "" {
		details = append(details, map[string]string{"field": "account", "reason": "请填写邮箱或手机号"})
	}
	if strings.TrimSpace(r.Password) == "" {
		details = append(details, map[string]string{"field": "password", "reason": "请填写密码"})
	}
	if len(details) > 0 {
		return apperr.ValidationFailed("请填写完整登录信息", details)
	}
	return nil
}

// SetPINRequest 设置或修改 PIN。
type SetPINRequest struct {
	PIN string `json:"pin"`
}

// Validate PIN 只允许 4–6 位数字。
func (r SetPINRequest) Validate() error {
	if !pinRe.MatchString(strings.TrimSpace(r.PIN)) {
		return apperr.ValidationFailed("PIN 格式有误", []map[string]string{
			{"field": "pin", "reason": "PIN 需为 4-6 位数字"},
		})
	}
	return nil
}

// VerifyPINRequest PIN 校验，用于解锁敏感操作。
type VerifyPINRequest struct {
	PIN string `json:"pin"`
}

// Validate 校验 PIN 必填（格式由 SetPIN 保证，校验时只拦空值以细化提示）。
func (r VerifyPINRequest) Validate() error {
	if strings.TrimSpace(r.PIN) == "" {
		return apperr.ValidationFailed("请输入 PIN", []map[string]string{
			{"field": "pin", "reason": "PIN 不能为空"},
		})
	}
	return nil
}

// UnlockResult PIN 校验通过后的短时解锁令牌。
type UnlockResult struct {
	UnlockToken string `json:"unlock_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// QRStartResult 桌面端拿到的二维码内容。qr_token 只用于订阅事件，
// deep_link 供手机扫码后跳转到确认页。
type QRStartResult struct {
	QRToken   string `json:"qr_token"`
	DeepLink  string `json:"deep_link"`
	ExpiresIn int64  `json:"expires_in"`
}

// QRScanRequest 手机端扫码上报。
type QRScanRequest struct {
	QRToken string `json:"qr_token"`
}

// Validate 校验扫码参数。
func (r QRScanRequest) Validate() error {
	if r.QRToken == "" {
		return apperr.ValidationFailed("缺少二维码令牌", []map[string]string{
			{"field": "qr_token", "reason": "不能为空"},
		})
	}
	return nil
}

// QRScanResult 扫码成功回给手机端的信息，用于在手机上展示「确认页」。
// IP 只回前缀，不全量暴露，够家长判断是不是自己的设备即可。
type QRScanResult struct {
	Status     string `json:"status"`
	DeviceHint string `json:"device_hint"`
	IPPrefix   string `json:"ip_prefix"`
}

// QRConfirmResult 确认后回给手机端的状态。
type QRConfirmResult struct {
	Status string `json:"status"`
}

// QRExchangeRequest 桌面端用一次性兑换码换令牌。
type QRExchangeRequest struct {
	QRToken      string `json:"qr_token"`
	ExchangeCode string `json:"exchange_code"`
}

// Validate 校验兑换参数。
func (r QRExchangeRequest) Validate() error {
	var details []map[string]string
	if r.QRToken == "" {
		details = append(details, map[string]string{"field": "qr_token", "reason": "不能为空"})
	}
	if r.ExchangeCode == "" {
		details = append(details, map[string]string{"field": "exchange_code", "reason": "不能为空"})
	}
	if len(details) > 0 {
		return apperr.ValidationFailed("兑换参数不完整", details)
	}
	return nil
}

// QRScannedPayload / QRConfirmedPayload 是 SSE 事件体，刻意只带必要信息。
type QRScannedPayload struct {
	Status     string `json:"status"`
	DeviceHint string `json:"device_hint"`
	IPPrefix   string `json:"ip_prefix"`
}

// QRConfirmedPayload 携带一次性兑换码。
type QRConfirmedPayload struct {
	Status       string `json:"status"`
	ExchangeCode string `json:"exchange_code"`
	ExpiresIn    int64  `json:"expires_in"`
}

// QRExpiredPayload 过期或失败事件体。
type QRExpiredPayload struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}
