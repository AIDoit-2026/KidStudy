package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testLimiter 造一个时钟可控的限流器。
func testLimiter(perMin, burst int) (*Limiter, *time.Time) {
	l := NewLimiter(perMin, burst)
	clock := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return clock }
	return l, &clock
}

func TestLimiter_BurstThenRefill(t *testing.T) {
	l, clock := testLimiter(60, 3) // 每秒补 1 个，桶容量 3

	for i := 1; i <= 3; i++ {
		ok, remaining, _ := l.Allow("1.2.3.4")
		if !ok {
			t.Fatalf("第 %d 次应放行", i)
		}
		if want := 3 - i; remaining != want {
			t.Fatalf("第 %d 次剩余令牌 = %d，期望 %d", i, remaining, want)
		}
	}

	ok, _, retry := l.Allow("1.2.3.4")
	if ok {
		t.Fatal("桶已空，第 4 次不应放行")
	}
	// 距下一个令牌 1 秒（rate=1/s），给 1 秒余量容忍浮点
	if retry < 900*time.Millisecond || retry > 1100*time.Millisecond {
		t.Fatalf("retryAfter = %v，期望约 1s", retry)
	}

	// 过 1 秒补 1 个令牌 → 可再放行一次
	*clock = clock.Add(time.Second)
	if ok, _, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("过 1 秒后应补充 1 个令牌并放行")
	}
	// 紧接着又不放行
	if ok, _, _ := l.Allow("1.2.3.4"); ok {
		t.Fatal("补充的令牌已用掉，不应再次放行")
	}
}

func TestLimiter_CapsAtBurst(t *testing.T) {
	l, clock := testLimiter(60, 2)
	l.Allow("k") // 用掉 1 个（剩 1）

	// 空闲 1 小时也只补到桶容量 2，不会溢出
	*clock = clock.Add(time.Hour)
	if ok, remaining, _ := l.Allow("k"); !ok || remaining != 1 {
		t.Fatalf("长时间空闲后应封顶在 burst=2：ok=%v remaining=%d", ok, remaining)
	}
}

func TestLimiter_KeysAreIsolated(t *testing.T) {
	l, _ := testLimiter(60, 1)
	if ok, _, _ := l.Allow("a"); !ok {
		t.Fatal("a 首次应放行")
	}
	if ok, _, _ := l.Allow("a"); ok {
		t.Fatal("a 应被限流")
	}
	if ok, _, _ := l.Allow("b"); !ok {
		t.Fatal("b 是另一个桶，不应受 a 影响")
	}
}

func TestRateLimit_MiddlewareReports429(t *testing.T) {
	l, _ := testLimiter(60, 2)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(l, log)(next)

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for i := 1; i <= 2; i++ {
		rec := do()
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次应 200，实际 %d", i, rec.Code)
		}
		if got := rec.Header().Get("X-RateLimit-Limit"); got != "2" {
			t.Fatalf("X-RateLimit-Limit = %q，期望 2", got)
		}
	}

	rec := do()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超限应 429，实际 %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 响应缺少 Retry-After")
	}
	if rec.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("超限时剩余应为 0，实际 %q", rec.Header().Get("X-RateLimit-Remaining"))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("429 应返回 JSON 错误体，Content-Type = %q", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "RATE_LIMITED") {
		t.Fatalf("429 响应体应含稳定错误码 RATE_LIMITED，实际 %q", body)
	}
}

func TestRateLimit_PerKeyByIP(t *testing.T) {
	l, _ := testLimiter(60, 1)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(l, log)(next)

	code := func(ip string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := code("10.0.0.1"); got != http.StatusOK {
		t.Fatalf("A 首次应 200，实际 %d", got)
	}
	if got := code("10.0.0.1"); got != http.StatusTooManyRequests {
		t.Fatalf("A 再次应 429，实际 %d", got)
	}
	if got := code("10.0.0.2"); got != http.StatusOK {
		t.Fatalf("换 IP 应重新计数并 200，实际 %d", got)
	}
}

func TestNewLimiters_DisabledIsPassthrough(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	lims := NewLimiters(false,
		LimitSpec{PerMin: 1, Burst: 1},
		LimitSpec{PerMin: 1, Burst: 1},
		LimitSpec{PerMin: 1, Burst: 1},
		log,
	)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := lims.Login(next)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "10.0.0.9:1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("关闭限流后第 %d 次应 200，实际 %d", i+1, rec.Code)
		}
		if rec.Header().Get("Retry-After") != "" {
			t.Fatal("关闭限流后不应出现 Retry-After")
		}
	}
}

func TestNewLimiters_ScopesUseOwnBurst(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	lims := NewLimiters(true,
		LimitSpec{PerMin: 12, Burst: 6},
		LimitSpec{PerMin: 120, Burst: 40},
		LimitSpec{PerMin: 30, Burst: 10},
		log,
	)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	check := func(name string, mw func(http.Handler) http.Handler, want string) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "10.1.1.1:1"
		rec := httptest.NewRecorder()
		mw(next).ServeHTTP(rec, req)
		if got := rec.Header().Get("X-RateLimit-Limit"); got != want {
			t.Fatalf("%s 的 X-RateLimit-Limit = %q，期望 %q", name, got, want)
		}
	}
	check("Login", lims.Login, "6")
	check("QR", lims.QR, "40")
	check("Print", lims.Print, "10")
}
