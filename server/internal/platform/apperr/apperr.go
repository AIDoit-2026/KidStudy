// Package apperr 定义全服务统一的类型化错误体系。
//
// 约定：
//  1. 业务层只构造本包的错误（或通过 Errors 包内工具包装底层错误）；
//  2. HTTP 层只做 Code → 状态码的映射，绝不把堆栈或内部细节返回给客户端；
//  3. 面向用户的 message 必须是中文可读文案。
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Code 是面向客户端的稳定错误码。
type Code string

const (
	CodeValidationFailed Code = "VALIDATION_FAILED"
	CodeUnauthorized     Code = "UNAUTHORIZED"
	CodeForbidden        Code = "FORBIDDEN"
	CodeNotFound         Code = "NOT_FOUND"
	CodeConflict         Code = "CONFLICT"
	CodeMethodNotAllowed Code = "METHOD_NOT_ALLOWED"
	CodeRateLimited      Code = "RATE_LIMITED"
	CodeInternal         Code = "INTERNAL"
)

// Error 是携带 HTTP 语义的应用错误。
type Error struct {
	Code    Code   `json:"code"`
	Status  int    `json:"-"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
	err     error  // 内部原因，只进日志，不出网
}

// New 构造应用错误。msg 必须是可以直接展示给用户的中文文案。
func New(code Code, status int, msg string) *Error {
	return &Error{Code: code, Status: status, Message: msg}
}

// Wrap 用内部原因包装（原因只用于日志与排查）。
func Wrap(code Code, status int, msg string, cause error) *Error {
	e := New(code, status, msg)
	e.err = cause
	return e
}

// WithDetails 附带字段级细节（如表单校验失败的具体字段）。
func (e *Error) WithDetails(d any) *Error {
	e.Details = d
	return e
}

func (e *Error) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap 让 errors.Is / errors.As 可以穿透到内部原因。
func (e *Error) Unwrap() error { return e.err }

// 常用构造器。
var (
	BadRequest = func(msg string) *Error {
		return New(CodeValidationFailed, http.StatusUnprocessableEntity, msg)
	}
	ValidationFailed = func(msg string, details any) *Error {
		return New(CodeValidationFailed, http.StatusUnprocessableEntity, msg).WithDetails(details)
	}
	Unauthorized = func(msg string) *Error {
		return New(CodeUnauthorized, http.StatusUnauthorized, msg)
	}
	Forbidden = func(msg string) *Error {
		return New(CodeForbidden, http.StatusForbidden, msg)
	}
	NotFound = func(msg string) *Error {
		return New(CodeNotFound, http.StatusNotFound, msg)
	}
	MethodNotAllowed = func(msg string) *Error {
		return New(CodeMethodNotAllowed, http.StatusMethodNotAllowed, msg)
	}
	Conflict = func(msg string) *Error {
		return New(CodeConflict, http.StatusConflict, msg)
	}
	RateLimited = func(msg string) *Error {
		return New(CodeRateLimited, http.StatusTooManyRequests, msg)
	}
	Internal = func(cause error) *Error {
		return Wrap(CodeInternal, http.StatusInternalServerError, "服务开小差了，请稍后再试", cause)
	}
)

// From 把任意错误转成 *Error：已是的直接返回，其余视为内部错误。
func From(err error) *Error {
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	return Internal(err)
}

// StatusOf 返回错误对应的 HTTP 状态码。
func StatusOf(err error) int { return From(err).Status }
