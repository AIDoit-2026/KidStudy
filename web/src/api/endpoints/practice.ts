/**
 * 练习：今日任务、会话、判分、结算、家长确认。
 *
 * 注意：答案永不下发 —— 直到某题作答后，后端才把 correct_answer / explain 回传。
 * 前端不要在本地保存答案，也不要在判分前展示。
 */
import { api } from '../client'
import type {
  AnswerResult,
  SessionSummary,
  SessionView,
  TodayPlan,
} from '../types'

/** 今日任务（§4.2）。subjects 为空表示三科全要。 */
export const today = (childId: string, subjects?: string[]) =>
  api.get<TodayPlan>('/practice/today', {
    query: { child_id: childId, subject: subjects?.length ? subjects.join(',') : undefined },
  })

export const startSession = (body: { child_id: string; subject: string; device_type: string }) =>
  api.post<SessionView>('/practice/session', body)

export const getSession = (sessionId: string, childId: string) =>
  api.get<SessionView>(`/practice/session/${sessionId}`, { query: { child_id: childId } })

export type AnswerBody = {
  child_id: string
  item_id: string
  /** single/match/sequence 传数组；free 传字符串 */
  answer: string | string[]
  elapsed_ms?: number
  used_hint?: boolean
}

export const answer = (sessionId: string, body: AnswerBody) =>
  api.post<AnswerResult>(`/practice/session/${sessionId}/answer`, body)

export const skip = (sessionId: string, body: { child_id: string; item_id: string }) =>
  api.post<{ item_id: string; state: string }>(`/practice/session/${sessionId}/skip`, body)

/** 结算：返回星级与正确率，并触发成就评测。 */
export const finish = (sessionId: string, childId: string) =>
  api.post<SessionSummary>(`/practice/session/${sessionId}/finish`, { child_id: childId })

export type ConfirmBody = {
  child_id: string
  /** 1–5，主观项（描红/跟读）的家长评分 */
  parent_score?: number
  parent_note?: string
  /** 兜底：家长手动标记今日完成 */
  mark_done?: boolean
}

export const confirm = (sessionId: string, body: ConfirmBody) =>
  api.post<Record<string, unknown>>(`/practice/session/${sessionId}/confirm`, body)
