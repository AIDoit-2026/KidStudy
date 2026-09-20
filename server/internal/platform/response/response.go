// Package response 统一 HTTP 响应格式。
//
//	成功：{ "data": {...}, "meta": { "request_id": "..." } }
//	失败：{ "error": { "code": "...", "message": "...", "details": [...] } }
package response

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/middleware"
)

// Meta 携带与请求相关的元信息。后续会补部分页字段。
type Meta struct {
	RequestID string `json:"request_id"`
}

type successEnvelope struct {
	Data any  `json:"data"`
	Meta Meta `json:"meta"`
}

type errPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type errEnvelope struct {
	Error errPayload `json:"error"`
	Meta  Meta       `json:"meta"`
}

// JSON 写成功响应。data 为 nil 时仍返回 {"data":null}。
func JSON(w http.ResponseWriter, r *http.Request, status int, data any) {
	writeJSON(w, r, status, successEnvelope{
		Data: data,
		Meta: Meta{RequestID: middleware.RequestIDFromContext(r.Context())},
	})
}

// Error 写错误响应。内部错误只回固定文案，原始错误进日志，不出现堆栈。
func Error(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	ae := apperr.From(err)

	switch {
	case ae.Status >= 500:
		log.Error("request failed",
			"request_id", middleware.RequestIDFromContext(r.Context()),
			"error", err,
		)
	case ae.Status >= 400:
		log.Warn("request rejected",
			"request_id", middleware.RequestIDFromContext(r.Context()),
			"code", ae.Code,
			"status", ae.Status,
		)
	}

	writeJSON(w, r, ae.Status, errEnvelope{
		Error: errPayload{Code: string(ae.Code), Message: ae.Message, Details: ae.Details},
		Meta:  Meta{RequestID: middleware.RequestIDFromContext(r.Context())},
	})
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// 编码失败已无法挽回写入中的数据，记录后忽略
		_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL","message":"响应序列化失败"}}`))
	}
}
