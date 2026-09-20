package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 令牌用途（typ）：不同用途的令牌互不通用，避免拿 Access 令牌去换 PIN 解锁。
const (
	TypeAccess = "access"
	TypeUnlock = "unlock"
)

// ErrInvalidToken 表示令牌无效（过期、签名不符、用途不对）。
var ErrInvalidToken = errors.New("令牌无效或已过期")

// TokenService 负责 Access / Unlock 这类自包含令牌的签发与校验。
// Refresh 与 QR 令牌走数据库，不在 service 里签发，但共用同一份密钥与 TTL 配置。
type TokenService struct {
	secret     []byte
	accessTTL  time.Duration
	unlockTTL  time.Duration
	parseOpts  []jwt.ParserOption
	validTypes map[string]struct{}
}

// NewTokenService 构造令牌服务。
func NewTokenService(secret string, accessTTL, unlockTTL time.Duration) *TokenService {
	return &TokenService{
		secret:    []byte(secret),
		accessTTL: accessTTL,
		unlockTTL: unlockTTL,
		parseOpts: []jwt.ParserOption{
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		},
		validTypes: map[string]struct{}{TypeAccess: {}, TypeUnlock: {}},
	}
}

// AccessTTL 返回访问令牌有效期，供上层写入响应体。
func (s *TokenService) AccessTTL() time.Duration { return s.accessTTL }

// IssueAccessToken 签发给指定家长的访问令牌。
func (s *TokenService) IssueAccessToken(parentID string) (token string, expiresAt time.Time, err error) {
	return s.issue(TypeAccess, parentID, s.accessTTL)
}

// IssueUnlockToken 签发 PIN 校验通过后的短时解锁令牌。
func (s *TokenService) IssueUnlockToken(parentID string) (token string, expiresAt time.Time, err error) {
	return s.issue(TypeUnlock, parentID, s.unlockTTL)
}

func (s *TokenService) issue(typ, parentID string, ttl time.Duration) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)
	claims := jwt.MapClaims{
		"sub": parentID,
		"typ": typ,
		"iat": now.Unix(),
		"exp": expiresAt.Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("签发令牌失败: %w", err)
	}
	return signed, expiresAt, nil
}

// ParseToken 校验令牌并返回家长 ID 与用途。任何异常统一收敛为 ErrInvalidToken，
// 不把「签名错/过期/用途不符」这些细节抛给上层，防止令牌探测。
func (s *TokenService) ParseToken(token string, wantType string) (parentID string, err error) {
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return s.secret, nil
	}, s.parseOpts...); err != nil {
		return "", ErrInvalidToken
	}

	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return "", ErrInvalidToken
	}
	typ, ok := claims["typ"].(string)
	if !ok {
		return "", ErrInvalidToken
	}
	if _, known := s.validTypes[typ]; !known {
		return "", ErrInvalidToken
	}
	if typ != wantType {
		return "", ErrInvalidToken
	}
	return sub, nil
}
