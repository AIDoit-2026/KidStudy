// Package auth 提供认证相关的底层能力：口令哈希、AccessToken / UnlockToken 签发与校验。
//
// 它属于平台层而非业务层：只做密码学与令牌编解码，不碰 HTTP、不查数据库。
// Refresh / QR 的原文生成与哈希也放在这里，保证「令牌怎么来的」只有一处实现。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id 参数：采用 RFC 9106 的第一组推荐值（19MB / 2 轮 / 并行 1）。
// 家庭单机场景登录并发极低，优先保证口令存储强度，同时不把登录耗时拖到肉眼可见。
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrInvalidHash 表示存储的哈希串格式不正确（一般是数据损坏或版本不兼容）。
var ErrInvalidHash = errors.New("口令哈希格式非法")

// HashPassword 生成 argon2id 哈希串，格式：
// argon2id$v=19$m=19456,t=2,p=1$<salt b64>$<key b64>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成口令盐值失败: %w", err)
	}
	return hashWithSalt(password, salt), nil
}

// VerifyPassword 校验明文口令是否匹配哈希串。口令错误与格式错误都返回 false，
// 具体原因只在 error 中体现，避免给调用方（进而给客户端）过多信息。
func VerifyPassword(encoded, password string) (bool, error) {
	// 自生成格式为 argon2id$v=19$m=..,t=..,p=..$<salt>$<key>，共 5 段
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false, ErrInvalidHash
	}
	var memory uint32
	var iterations, threads uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, ErrInvalidHash
	}
	if memory == 0 || iterations == 0 || threads == 0 {
		return false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}

	got := argon2.IDKey([]byte(password), salt, uint32(iterations), memory, uint8(threads), uint32(len(want)))
	// 定长比较，避免通过响应时间侧信道逐字节试探
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func hashWithSalt(password string, salt []byte) string {
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

// randomString 生成 URL 安全的随机串，用于 Refresh / QR / 兑换码的原文部分。
func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewOpaqueToken 生成一次性令牌的原文与其存储哈希。
//
// 数据库只落哈希：即便库被拖走也无法直接重放令牌。Refresh 令牌长度设计：
// 16 字节随机 = 128 位，配合服务端撤销足够安全。
func NewOpaqueToken() (plain, hashed string, err error) {
	p, err := randomString(16)
	if err != nil {
		return "", "", err
	}
	return p, HashToken(p), nil
}

// HashToken 返回令牌原文的 SHA-256（十六进制），用于入库比对。
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return fmt.Sprintf("%x", sum)
}
