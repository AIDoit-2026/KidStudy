// Package requestctx 集中管理请求上下文中的键值。
//
// 它刻意不依赖任何其他内部包：middleware 写入、response 读取、handler 读取，
// 三者都只依赖本包，从而避免 middleware ↔ response 的循环依赖。
package requestctx

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/google/uuid"
)

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyParentID
)

// RequestIDHeader 是透传/返回给客户端的请求 ID 头。
const RequestIDHeader = "X-Request-ID"

// NewRequestID 生成请求 ID（无外部依赖，短小够用）。
func NewRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "req-fallback"
	}
	return "req-" + hex.EncodeToString(b)
}

// WithRequestID 写入请求 ID。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyRequestID, id)
}

// RequestIDFromContext 取请求 ID，缺失返回空串。
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(keyRequestID).(string); ok {
		return v
	}
	return ""
}

// WithParentID 写入当前登录家长 ID。
func WithParentID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, keyParentID, id)
}

// ParentIDFromContext 取当前登录家长 ID，未登录返回 false。
func ParentIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(keyParentID).(uuid.UUID)
	return id, ok
}
