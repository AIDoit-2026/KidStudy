package middleware

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/requestctx"
	"kidstudy/internal/platform/response"
)

// 令牌桶限流。按客户端 IP 分桶，用于挡「撞库式」的登录/扫码/打印刷流量。
//
// 为什么用令牌桶而不是固定窗口计数：固定窗口在窗口边界会放过 2 倍突发
// （第 59 秒打满、第 61 秒又打满），而令牌桶按时间平滑补充，突发上限恒定等于桶容量。
//
// 限流是「本机最后一道」而非唯一防线：真实部署里 Caddy 也会限流（M7 部署产物）。
// 这里的作用是在应用层兜住单个来源的滥用，且不依赖前置代理是否正确配置。
type Limiter struct {
	rate  float64 // 每秒补充的令牌数
	burst float64 // 桶容量（允许的瞬时突发）

	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time // 便于测试注入时钟
}

type bucket struct {
	tokens float64
	last   time.Time
}

// 空闲桶回收：只挡滥用，不无限增长内存。
const (
	maxBuckets    = 10000
	bucketIdleTTL = 10 * time.Minute
)

// NewLimiter 构造限流器：perMin 为每分钟放行量，burst 为瞬时突发上限（<1 时兜底为 1）。
func NewLimiter(perMin, burst int) *Limiter {
	if perMin <= 0 {
		perMin = 1
	}
	if burst <= 0 {
		burst = 1
	}
	return &Limiter{
		rate:    float64(perMin) / 60.0,
		burst:   float64(burst),
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// Allow 判断 key 当前是否可放行，并给出剩余令牌数与「还需等多久」才有下一个令牌。
func (l *Limiter) Allow(key string) (ok bool, remaining int, retryAfter time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
		if len(l.buckets) > maxBuckets {
			l.evictLocked(now)
		}
	} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		// 按经过时间补令牌，封顶在桶容量
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, int(b.tokens), 0
	}
	// 距离攒够 1 个令牌还差多久（rate 恒 >0，不会除零）
	need := (1 - b.tokens) / l.rate
	return false, 0, time.Duration(need * float64(time.Second))
}

// Burst 返回桶容量，供 X-RateLimit-Limit 头使用。
func (l *Limiter) Burst() int { return int(l.burst) }

func (l *Limiter) evictLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.last) > bucketIdleTTL {
			delete(l.buckets, k)
		}
	}
}

// RateLimit 把一个限流器包成中间件。超限返回 429 + Retry-After，
// 并始终带 X-RateLimit-Limit / X-RateLimit-Remaining，便于客户端自适应退避。
func RateLimit(l *Limiter, log *slog.Logger) func(http.Handler) http.Handler {
	limit := strconv.Itoa(l.Burst())
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := remoteIP(r)
			ok, remaining, retryAfter := l.Allow(ip)

			h := w.Header()
			h.Set("X-RateLimit-Limit", limit)
			h.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))

			if !ok {
				secs := int(math.Ceil(retryAfter.Seconds()))
				if secs < 1 {
					secs = 1
				}
				h.Set("Retry-After", strconv.Itoa(secs))
				// response.Error 会带日志（含 code/status），此处补一条策略级日志便于溯源
				log.Warn("请求被限流",
					"request_id", requestctx.RequestIDFromContext(r.Context()),
					"remote_ip", ip,
					"path", r.URL.Path,
					"retry_after_sec", secs,
				)
				response.Error(w, r, log, apperr.RateLimited("操作太频繁了，请稍后再试"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// LimitSpec 一个限流档位的参数。
type LimitSpec struct {
	PerMin int
	Burst  int
}

// Limiters 三类敏感端点各自的限流中间件。
//
// 分开成三档而不是共用一档：扫码轮询（events）是高频读，登录是低频高价值操作，
// 打印是重活（触发 PDF 渲染）。给它们同一个桶要么误伤扫码、要么放过刷打印。
type Limiters struct {
	Login func(http.Handler) http.Handler
	QR    func(http.Handler) http.Handler
	Print func(http.Handler) http.Handler
}

// NewLimiters 构造三类限流中间件；enabled=false 时全部直通（本地开发/测试可一键关闭）。
func NewLimiters(enabled bool, login, qr, printSpec LimitSpec, log *slog.Logger) *Limiters {
	if !enabled {
		passthrough := func(next http.Handler) http.Handler { return next }
		return &Limiters{Login: passthrough, QR: passthrough, Print: passthrough}
	}
	return &Limiters{
		Login: RateLimit(NewLimiter(login.PerMin, login.Burst), log),
		QR:    RateLimit(NewLimiter(qr.PerMin, qr.Burst), log),
		Print: RateLimit(NewLimiter(printSpec.PerMin, printSpec.Burst), log),
	}
}
