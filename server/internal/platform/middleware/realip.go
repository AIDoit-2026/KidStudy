package middleware

import (
	"net"
	"net/http"
	"strings"
)

// remoteIP 解析客户端 IP。是否信任代理头由配置决定，默认不信任，防止伪造。
func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return host
	}
	return r.RemoteAddr
}

// RealIP 在信任反向代理时取 X-Forwarded-For 的最左段。
// Caddy 在本服务前，生产环境应开启 TRUSTED_PROXY。
func RealIP(trustedProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if trustedProxy {
				if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
					if idx := strings.IndexByte(xff, ','); idx > 0 {
						r.RemoteAddr = strings.TrimSpace(xff[:idx])
					} else {
						r.RemoteAddr = strings.TrimSpace(xff)
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
