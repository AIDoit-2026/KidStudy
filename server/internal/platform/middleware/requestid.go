// Package middleware 提供 HTTP 中间件链。顺序在 server.NewRouter 中显式定义，勿随意调整。
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type ctxKey int

const requestIDKey ctxKey = iota

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

// RequestID 保证每个请求都有 ID：优先信任上游代理传来的值，否则生成；
// 同时写回响应头，便于客户端与日志对照。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = NewRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext 取出请求 ID，缺失时返回空串。
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}
