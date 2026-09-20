// Package middleware 提供 HTTP 中间件链。顺序在 server.NewRouter 中显式定义，勿随意调整。
package middleware

import (
	"net/http"

	"kidstudy/internal/platform/requestctx"
)

// RequestIDHeader 转发自 requestctx，便于 HTTP 层统一引用。
const RequestIDHeader = requestctx.RequestIDHeader

// NewRequestID 生成请求 ID。
var NewRequestID = requestctx.NewRequestID

// RequestID 保证每个请求都有 ID：优先信任上游代理传来的值，否则生成；
// 同时写回响应头，便于客户端与日志对照。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = NewRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(requestctx.WithRequestID(r.Context(), id)))
	})
}
