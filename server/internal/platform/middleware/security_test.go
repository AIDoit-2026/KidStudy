package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSecurityHeaders_BaseSet 锁住基础安全头，避免以后有人「顺手删一个」。
func TestSecurityHeaders_BaseSet(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := SecurityHeaders(false)(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/practice/today", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	want := map[string]string{
		"X-Content-Type-Options":            "nosniff",
		"X-Frame-Options":                   "DENY",
		"Referrer-Policy":                   "no-referrer",
		"X-Permitted-Cross-Domain-Policies": "none",
		"Cross-Origin-Resource-Policy":      "same-site",
		"Content-Security-Policy":           "default-src 'none'; frame-ancestors 'none'; base-uri 'none'",
		"Cache-Control":                     "no-store",
		"Cross-Origin-Opener-Policy":        "same-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q，期望 %q", k, got, v)
		}
	}
}

func TestSecurityHeaders_HSTSOnlyInProd(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	dev := httptest.NewRecorder()
	SecurityHeaders(false)(next).ServeHTTP(dev, httptest.NewRequest(http.MethodGet, "/health", nil))
	if got := dev.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("开发环境不应下发 HSTS，实际 %q", got)
	}

	prod := httptest.NewRecorder()
	SecurityHeaders(true)(next).ServeHTTP(prod, httptest.NewRequest(http.MethodGet, "/health", nil))
	hsts := prod.Header().Get("Strict-Transport-Security")
	if hsts != "max-age=31536000; includeSubDomains" {
		t.Errorf("生产环境 HSTS = %q，期望含 max-age 与 includeSubDomains", hsts)
	}
}
