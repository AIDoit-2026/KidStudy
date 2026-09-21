// Package pagination 解析列表接口的 offset / limit 参数。
//
// 所有列表端点共用一套规则，避免每个 handler 各写一遍并各错一遍：
// 参数缺失走默认值，非法值直接 422 带字段级 details。
package pagination

import (
	"net/http"
	"strconv"
	"strings"

	"kidstudy/internal/platform/apperr"
)

const (
	DefaultLimit = 20
	MaxLimit     = 200
)

// Page 是解析后的分页参数。
type Page struct {
	Offset int
	Limit  int
}

// Parse 从查询串读取 offset / limit。
func Parse(r *http.Request) (Page, error) {
	p := Page{Offset: 0, Limit: DefaultLimit}

	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			return Page{}, apperr.ValidationFailed("分页参数有误", []map[string]string{
				{"field": "offset", "reason": "必须是不小于 0 的整数"},
			})
		}
		p.Offset = v
	}

	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return Page{}, apperr.ValidationFailed("分页参数有误", []map[string]string{
				{"field": "limit", "reason": "必须是大于 0 的整数"},
			})
		}
		if v > MaxLimit {
			return Page{}, apperr.ValidationFailed("分页参数有误", []map[string]string{
				{"field": "limit", "reason": "不能超过 200"},
			})
		}
		p.Limit = v
	}

	return p, nil
}
