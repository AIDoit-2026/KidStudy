package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/auth"
	"kidstudy/internal/platform/requestctx"
	"kidstudy/internal/platform/response"
)

// RequireAuth 校验 Bearer Access 令牌，把家长 ID 放进请求上下文。
//
// 令牌无效一律返回同一个 401 文案，不区分过期/签名错/用途错，避免给攻击者反馈。
func RequireAuth(tokens *auth.TokenService, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parentID, ok := parentIDFromHeader(r, tokens)
			if !ok {
				response.Error(w, r, log, apperr.Unauthorized("登录已失效，请重新登录"))
				return
			}
			ctx := requestctx.WithParentID(r.Context(), parentID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireUnlock 校验 PIN 解锁令牌（作用在敏感操作上，5 分钟有效）。
func RequireUnlock(tokens *auth.TokenService, log *slog.Logger) func(http.Handler) http.Handler {
	return verifyPurpose(tokens, log, auth.TypeUnlock)
}

func verifyPurpose(tokens *auth.TokenService, log *slog.Logger, want string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearer(r)
			if raw == "" {
				response.Error(w, r, log, apperr.Unauthorized("需要二次验证"))
				return
			}
			if _, err := tokens.ParseToken(raw, want); err != nil {
				response.Error(w, r, log, apperr.Unauthorized("验证已失效，请重新验证"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func parentIDFromHeader(r *http.Request, tokens *auth.TokenService) (uuid.UUID, bool) {
	raw := bearer(r)
	if raw == "" {
		return uuid.Nil, false
	}
	sub, err := tokens.ParseToken(raw, auth.TypeAccess)
	if err != nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(sub)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
