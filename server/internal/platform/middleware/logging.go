package middleware

import (
	"kidstudy/internal/platform/requestctx"
	"log/slog"
	"net/http"
	"time"

	"kidstudy/internal/platform/apperr"
)

// responseWriter 捕获状态码与响应大小。
type responseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (w *responseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

// Flush 透传给底层：否则 SSE 这类需要逐帧推送的连接会被包装层憋住，
// 上层做 http.Flusher 类型断言也会失败。
func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 让标准库的 ResponseController 等能力可以穿透到原始 writer。
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Logger 输出每条请求的访问日志：route / status / duration / request_id。
// 注意：这里绝不记录 body、密码、令牌与任何儿童隐私字段。
func Logger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			attrs := []any{
				"request_id", requestctx.RequestIDFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.status,
				"bytes", wrapped.size,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_ip", remoteIP(r),
				"user_agent", r.UserAgent(),
			}
			lvl := slog.LevelInfo
			if wrapped.status >= 500 {
				lvl = slog.LevelError
			} else if wrapped.status >= 400 {
				lvl = slog.LevelWarn
			}
			log.Log(r.Context(), lvl, "http request", attrs...)
		})
	}
}

// Recoverer 兜底 panic：转成统一内部错误响应，避免进程崩溃与堆栈泄漏。
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered",
						"request_id", requestctx.RequestIDFromContext(r.Context()),
						"panic", rec,
						"path", r.URL.Path,
					)
					writePanicResponse(w)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// writePanicResponse 避免与 response 包循环依赖，这里内联一个最小错误响应。
func writePanicResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(apperr.StatusOf(apperr.Internal(nil)))
	_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL","message":"服务开小差了，请稍后再试"}}`))
}
