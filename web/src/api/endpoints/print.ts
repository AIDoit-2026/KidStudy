/**
 * 打印中心（M5 后端已就绪）。
 *
 * 关键点：预览 HTML 与 worker 渲染出的 PDF 是**同一份字符串**，
 * 所以前端只要把这份 HTML 塞进 iframe，家长看到的就与打印出来的完全一致。
 */
import { api, requestBlob } from '../client'
import type { PrintJob, PrintPayload, PrintTemplate } from '../types'

export const listTemplates = () => api.get<{ templates: PrintTemplate[] }>('/print/templates')

export type CreateJobBody = {
  template_code: string
  child_id?: string
  params?: Record<string, unknown>
}

export const createJob = (body: CreateJobBody) => api.post<PrintJob>('/print/jobs', body)

export const getJob = (id: string) => api.get<PrintJob>(`/print/jobs/${id}`)

export const getPayload = (id: string) => api.get<PrintPayload>(`/print/jobs/${id}/data`)

/** 入队渲染（后端返回 202）；实际渲染由后端 worker 消费，前端轮询 status 即可。 */
export const queuePdf = (id: string) => api.post<PrintJob>(`/print/jobs/${id}/pdf`)

/** 取预览 HTML（自包含，CSS 已内联，可直接作为 iframe 的 srcdoc）。 */
export async function previewHtml(id: string): Promise<string> {
  const blob = await requestBlob(`/print/jobs/${id}/preview`)
  return blob.text()
}

/** 下载 PDF（后端未就绪时返回 409，会被翻译成「请稍后再试」）。 */
export async function downloadPdf(id: string): Promise<void> {
  const blob = await requestBlob(`/print/jobs/${id}/pdf`)
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement('a')
    a.href = url
    a.download = `kidstudy-${id}.pdf`
    document.body.appendChild(a)
    a.click()
    a.remove()
  } finally {
    // 立刻 revoke 会让下载中断：交给浏览器读完再释放
    window.setTimeout(() => URL.revokeObjectURL(url), 10_000)
  }
}

export type MarkDoneItem = { kp_id: string; correct?: boolean }

export type MarkDoneResult = {
  job_id: string
  child_id: string
  session_id: string
  item_count: number
  correct_count: number
  mastered: number
  already_done: boolean
}

/** 纸质补录：纸上做完了，把结果写回掌握度（幂等，重复提交返回 already_done）。 */
export const markDone = (id: string, body: { items?: MarkDoneItem[]; note?: string } = {}) =>
  api.post<MarkDoneResult>(`/print/jobs/${id}/mark-done`, body)

export const listJobs = (childId?: string, offset = 0, limit = 20) =>
  api.paged<PrintJob[]>('/parent/print-jobs', { query: { childId, offset, limit } })
