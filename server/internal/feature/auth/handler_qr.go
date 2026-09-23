package auth

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"kidstudy/internal/platform/middleware"
	"kidstudy/internal/platform/requestctx"
	"kidstudy/internal/platform/response"
)

// registerQR 挂载扫码登录路由。
// 取二维码与兑换都不需要登录（正因为没登录才要扫码），扫码与确认必须已登录。
// 除 SSE 外的端点都挂扫码档限流：建码/兑换/扫码/确认都是可被脚本刷的动作。
func (h *Handler) registerQR(r chi.Router, requireAuth func(http.Handler) http.Handler, limiters *middleware.Limiters) {
	r.Route("/qrcode", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(limiters.QR)
			r.Get("/", h.qrStart)
			r.Post("/exchange", h.qrExchange)
		})

		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Use(limiters.QR)
			r.Post("/scan", h.qrScan)
			r.Post("/confirm", h.qrConfirm)
		})

		// SSE 走 GET，浏览器原生 EventSource 无法带 Authorization 头，
		// 因此令牌放在路径里；令牌本身是一次性且 60 秒过期，风险可控。
		// 这里不挂限流：长连接 + 断线自动重连，限流会误伤正常重连。
		r.Get("/{token}/events", h.qrEvents)
	})
}

// qrStart GET /auth/qrcode
func (h *Handler) qrStart(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.StartQR(r.Context(), clientIP(r), r.UserAgent())
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, res)
}

// qrScan POST /auth/qrcode/scan —— 手机端扫码上报
func (h *Handler) qrScan(w http.ResponseWriter, r *http.Request) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, errUnauthorized)
		return
	}
	var req QRScanRequest
	if !decode(w, r, h.log, &req) {
		return
	}
	res, err := h.svc.ScanQR(r.Context(), parentID, req, clientIP(r), r.UserAgent())
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, res)
}

// qrConfirm POST /auth/qrcode/confirm —— 手机端确认登录
func (h *Handler) qrConfirm(w http.ResponseWriter, r *http.Request) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, errUnauthorized)
		return
	}
	var req QRScanRequest
	if !decode(w, r, h.log, &req) {
		return
	}
	res, err := h.svc.ConfirmQR(r.Context(), parentID, req, clientIP(r), r.UserAgent())
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, res)
}

// qrExchange POST /auth/qrcode/exchange —— 桌面端凭一次性兑换码取令牌
func (h *Handler) qrExchange(w http.ResponseWriter, r *http.Request) {
	var req QRExchangeRequest
	if !decode(w, r, h.log, &req) {
		return
	}
	tok, err := h.svc.ExchangeQR(r.Context(), req, clientIP(r), r.UserAgent())
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	h.setRefreshCookie(w, tok.RefreshCipher, tok.RefreshExpireAt)
	response.JSON(w, r, http.StatusOK, tok.Session)
}

// qrEvents GET /auth/qrcode/{token}/events —— 桌面端订阅状态变化
func (h *Handler) qrEvents(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	status, err := h.svc.LookupQR(r.Context(), token)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		response.Error(w, r, h.log, errStreamUnsupported)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	// 反向代理别缓冲 SSE，否则事件会攒着不发
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, cancel := h.svc.SubscribeQR(status.SessionID)
	defer cancel()

	ctx := r.Context()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	// 二维码过期时主动推 expired 并断开，让前端立刻刷新而不是干等
	expiry := time.NewTimer(time.Until(status.ExpiresAt))
	defer expiry.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case ev, open := <-events:
			if !open {
				return
			}
			if !writeSSE(w, flusher, ev.Name, ev.Data) {
				return
			}
			// 终态事件之后不再保持连接：桌面端拿到结果后会走下一步
			if isTerminal(ev.Name) {
				return
			}

		case <-heartbeat.C:
			if _, err := w.Write([]byte(":ping\n\n")); err != nil {
				return
			}
			flusher.Flush()

		case <-expiry.C:
			_ = h.svc.MarkExpired(ctx, status.SessionID)
			writeSSE(w, flusher, "expired", QRExpiredPayload{Status: "expired"})
			return
		}
	}
}

// isTerminal 判断事件是否为终态。confirmed 之后桌面端拿兑换码换令牌，
// 无需再订阅；expired/failed 同理。
func isTerminal(name string) bool {
	switch name {
	case "confirmed", "expired", "failed":
		return true
	default:
		return false
	}
}

// writeSSE 写一条 SSE 事件并立即 flush。
func writeSSE(w http.ResponseWriter, f http.Flusher, event string, data any) bool {
	payload, err := json.Marshal(data)
	if err != nil {
		return false
	}
	if _, err := w.Write([]byte("event: " + event + "\n")); err != nil {
		return false
	}
	if _, err := w.Write([]byte("data: " + string(payload) + "\n\n")); err != nil {
		return false
	}
	f.Flush()
	return true
}
