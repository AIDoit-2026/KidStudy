package auth

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"kidstudy/internal/config"
	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/requestctx"
	"kidstudy/internal/platform/response"
)

// RefreshCookieName 是刷新令牌 Cookie 名。HttpOnly + SameSite=Lax，JS 读不到。
const RefreshCookieName = "kidstudy_refresh"

// Handler 处理 auth 的 HTTP 层：解析请求、调用 service、决定 Cookie 与状态码。
type Handler struct {
	svc *Service
	cfg config.Config
	log *slog.Logger
}

// NewHandler 构造 auth handler。
func NewHandler(svc *Service, cfg config.Config, log *slog.Logger) *Handler {
	return &Handler{svc: svc, cfg: cfg, log: log}
}

// Register 挂载 auth 路由。其中需要登录的端点由 middleware.RequireAuth 保护。
func (h *Handler) Register(r chi.Router, requireAuth func(http.Handler) http.Handler) {
	r.Route("/auth", func(r chi.Router) {
		r.Post("/register", h.register)
		r.Post("/login", h.login)
		r.Post("/refresh", h.refresh)

		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Post("/logout", h.logout)
			r.Get("/me", h.me)
			r.Put("/pin", h.setPIN)
			r.Post("/pin/verify", h.verifyPIN)
		})
	})
}

// register POST /auth/register
func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if !decode(w, r, h.log, &req) {
		return
	}
	tok, err := h.svc.Register(r.Context(), req, clientIP(r), r.UserAgent())
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	h.setRefreshCookie(w, tok.RefreshCipher, tok.RefreshExpireAt)
	response.JSON(w, r, http.StatusCreated, tok.Session)
}

// login POST /auth/login
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if !decode(w, r, h.log, &req) {
		return
	}
	tok, err := h.svc.Login(r.Context(), req, clientIP(r), r.UserAgent())
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	h.setRefreshCookie(w, tok.RefreshCipher, tok.RefreshExpireAt)
	response.JSON(w, r, http.StatusOK, tok.Session)
}

// refresh POST /auth/refresh —— 凭 Cookie 里的 Refresh 换一对新令牌。
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	old := refreshCookie(r)
	tok, err := h.svc.Refresh(r.Context(), old, clientIP(r), r.UserAgent())
	if err != nil {
		// 刷新失败必须清掉 Cookie，否则前端会一直停留在「看似已登录」状态
		h.clearRefreshCookie(w)
		response.Error(w, r, h.log, err)
		return
	}
	h.setRefreshCookie(w, tok.RefreshCipher, tok.RefreshExpireAt)
	response.JSON(w, r, http.StatusOK, tok.Session)
}

// logout POST /auth/logout
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return
	}
	if err := h.svc.Logout(r.Context(), parentID, refreshCookie(r), clientIP(r), r.UserAgent()); err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	h.clearRefreshCookie(w)
	response.JSON(w, r, http.StatusOK, map[string]any{"logged_out": true})
}

// me GET /auth/me
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return
	}
	view, err := h.svc.Me(r.Context(), parentID)
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, view)
}

// setPIN PUT /auth/pin
func (h *Handler) setPIN(w http.ResponseWriter, r *http.Request) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return
	}
	var req SetPINRequest
	if !decode(w, r, h.log, &req) {
		return
	}
	if err := h.svc.SetPIN(r.Context(), parentID, req, clientIP(r), r.UserAgent()); err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, map[string]any{"has_pin": true})
}

// verifyPIN POST /auth/pin/verify
func (h *Handler) verifyPIN(w http.ResponseWriter, r *http.Request) {
	parentID, ok := requestctx.ParentIDFromContext(r.Context())
	if !ok {
		response.Error(w, r, h.log, apperr.Unauthorized("登录已失效，请重新登录"))
		return
	}
	var req VerifyPINRequest
	if !decode(w, r, h.log, &req) {
		return
	}
	res, err := h.svc.VerifyPIN(r.Context(), parentID, req, clientIP(r), r.UserAgent())
	if err != nil {
		response.Error(w, r, h.log, err)
		return
	}
	response.JSON(w, r, http.StatusOK, res)
}

// setRefreshCookie 下发刷新令牌。Secure 随环境切换：本地 http 下不能开，否则浏览器不回传。
func (h *Handler) setRefreshCookie(w http.ResponseWriter, cipher string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    cipher,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   h.cfg.IsProd(),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearRefreshCookie 立即让 Cookie 失效。
func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cfg.IsProd(),
		SameSite: http.SameSiteLaxMode,
	})
}

func refreshCookie(r *http.Request) string {
	if c, err := r.Cookie(RefreshCookieName); err == nil {
		return c.Value
	}
	return ""
}

// decode 解析并限制请求体大小，失败时已写好错误响应，调用方只需 return。
func decode(w http.ResponseWriter, r *http.Request, log *slog.Logger, dst any) bool {
	const maxBody = 1 << 20 // 1MB

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		response.Error(w, r, log, apperr.BadRequest("请求体读取失败"))
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		response.Error(w, r, log, apperr.BadRequest("请求体不是合法的 JSON"))
		return false
	}
	return true
}

// clientIP 取客户端 IP。经 RealIP 中间件后 RemoteAddr 已是可信来源。
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
