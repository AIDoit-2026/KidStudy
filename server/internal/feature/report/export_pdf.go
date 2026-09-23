package report

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
)

// ReportPDFExporter 由 print.Service 实现：把「周学习报告」做成打印任务并排队渲染。
//
// 为什么用接口而不是直接 import print：依赖方向是 **print → report**（周报模板复用
// Overview / Trend / Suggestions，见 §10.7）。report 反过来 import print 会立刻成环，
// 所以这里只声明接口、签名只用 uuid / string 这类基础类型，由 cmd/api 后置注入
// （与建议动作 WithActions 同一套办法）。
type ReportPDFExporter interface {
	ExportWeeklyReport(ctx context.Context, parentID, childID uuid.UUID, days int) (jobID string, err error)
}

// errExportPDFUnavailable 导出依赖未注入（worker 进程用不到；API 进程一定注入了）。
var errExportPDFUnavailable = apperr.New(apperr.CodeInternal, http.StatusServiceUnavailable, "报表导出暂时不可用")

// WithPDFExporter 后置注入 PDF 导出能力（见 ReportPDFExporter 的构造环说明）。
func (s *Service) WithPDFExporter(exp ReportPDFExporter) *Service {
	s.pdf = exp
	return s
}

// ExportPDF 触发「周学习报告」PDF 生成，返回打印任务 ID。
//
// 渲染是异步的（Chromium 秒级任务，§8 后台任务约定）：这里只负责排队，
// 调用方拿 job_id 后轮询 /print/jobs/{id}，ready 再去 /pdf 下载。
func (s *Service) ExportPDF(ctx context.Context, parentID, childID uuid.UUID, days int) (string, error) {
	if s.pdf == nil {
		return "", errExportPDFUnavailable
	}
	return s.pdf.ExportWeeklyReport(ctx, parentID, childID, days)
}
