package middleware

import (
	"net/http"
	"strings"
)

// SecurityHeaders 设置基础安全头。
//
// API 只返回 JSON 与 PDF，不渲染页面，因此 CSP 取「全部拒绝」而不是宽松白名单：
// 这层 CSP 面向的是「有人把 /api/v1/... 的响应直接当页面打开」的场景。
// 注意打印预览 HTML 是前端用 srcdoc 注入 iframe 的（不是 iframe.src 指向本端点），
// 所以这里的 X-Frame-Options / frame-ancestors 不会挡住预览；打印功能不受影响。
func SecurityHeaders(isProd bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Permitted-Cross-Domain-Policies", "none")
			h.Set("Cross-Origin-Resource-Policy", "same-site")
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")

			// API 响应一律不缓存：内容含登录态与儿童隐私，不能被浏览器/中间代理存下来。
			// 前端静态资源由 Caddy/Vite 托管，不受这里影响。
			h.Set("Cache-Control", "no-store")
			// 不与任何其他源共享浏览上下文（防跨源窗口引用）。
			h.Set("Cross-Origin-Opener-Policy", "same-origin")

			if isProd {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS 使用显式来源白名单，生产禁用通配符。非预检请求只加头不拦截。
func CORS(allowedOrigins []string, isProd bool) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" && isProd {
			// 配置层已拦截，这里再做一次运行时兜底
			continue
		}
		allowed[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := allowed[origin]; !ok {
				// 来源不在白名单：不设置任何 CORS 头，浏览器会自行拦截
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Max-Age", "600")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
