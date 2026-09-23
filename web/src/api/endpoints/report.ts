/**
 * 家长报表（M4 后端就绪，M7 补前端入口）。
 *
 * 三块口径都在后端算好，前端只做展示，不再自己拼话术（§4.9 文案纪律）：
 *  - overview：总览卡片 + 分学科进度；
 *  - trend：逐日趋势（含是否达标、节奏偏差）；
 *  - suggestions：可解释建议 + 一键动作按钮。
 *
 * 导出：CSV 直出（浏览器存档），PDF 复用 M5 的周学习报告模板——返回一个打印任务，
 * 前端轮询 /print/jobs/{id} 到 pdf_ready 再从 pdf_url 下载（异步渲染，见 §10.7）。
 */
import { api, requestBlob } from '../client'
import type {
  ReportExportPdfResult,
  ReportOverview,
  ReportSuggestions,
  ReportTrend,
  SuggestionActionRequest,
  SuggestionActionResult,
} from '../types'

export const getOverview = (childId: string) =>
  api.get<ReportOverview>(`/reports/${childId}/overview`)

export const getTrend = (childId: string, days = 30, subject = '') =>
  api.get<ReportTrend>(`/reports/${childId}/trend`, { query: { days, subject } })

export const getSuggestions = (childId: string) =>
  api.get<ReportSuggestions>(`/reports/${childId}/suggestions`)

/** 执行建议附带的动作（§4.7）。归属由后端校验，越权 404。 */
export const applySuggestionAction = (childId: string, body: SuggestionActionRequest) =>
  api.post<SuggestionActionResult>(`/reports/${childId}/suggestions/actions`, body)

/** 触发 PDF 导出（复用周报模板），返回待渲染的打印任务。 */
export const exportPdf = (childId: string, days = 30) =>
  api.get<ReportExportPdfResult>(`/reports/${childId}/export`, { query: { format: 'pdf', days } })

/** 下载 CSV（逐日明细）。走后端导出接口，前端只负责落盘。 */
export async function downloadCsv(childId: string, days = 30, subject = ''): Promise<void> {
  const blob = await requestBlob(`/reports/${childId}/export`, {
    query: { format: 'csv', days, subject },
  })
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement('a')
    a.href = url
    a.download = 'report.csv'
    document.body.appendChild(a)
    a.click()
    a.remove()
  } finally {
    window.setTimeout(() => URL.revokeObjectURL(url), 10_000)
  }
}
