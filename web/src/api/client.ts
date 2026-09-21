/**
 * KidStudy 前端唯一出网口（设计文档 §2.3）。
 *
 * 三条不可动摇的纪律：
 *  1. Access Token 只存在**模块级内存变量**里 —— 不写 localStorage、不进 URL、不进日志。
 *     Refresh Token 在后端是 HttpOnly Cookie，由浏览器自动携带，JS 摸不到。
 *  2. 401 只在「非刷新请求」上触发一次静默刷新并重放，刷新本身单飞（并发请求共享同一次刷新）。
 *  3. 4xx 一律不重试（重试改变不了客户端的错），5xx 与网络错误最多重试 3 次并指数退避。
 *
 * 错误统一抛 ApiError：UI 只需读 status/code，文案映射见 readableError()。
 */
import type { PageMeta } from './types'

const RAW_BASE = import.meta.env.VITE_API_BASE_URL ?? ''

/** 后端基地址（已去掉尾部斜杠）。构建期来自 VITE_API_BASE_URL，绝不在代码里写死主机。 */
export const API_BASE_URL = RAW_BASE.replace(/\/+$/, '')

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export type QueryValue = string | number | boolean | undefined | null

export type RequestOptions = {
  method?: HttpMethod
  body?: unknown
  query?: Record<string, QueryValue | QueryValue[]>
  timeoutMs?: number
  signal?: AbortSignal
  /** 匿名接口与刷新接口自身：401 时不触发刷新重放，避免递归 */
  noAuthRefresh?: boolean
}

/** 后端错误响应体（internal/platform/response）。 */
type ErrorEnvelope = {
  error?: { code?: string; message?: string; details?: unknown }
}

/** 成功响应体：{ data, meta }。 */
type SuccessEnvelope<T> = {
  data: T
  meta?: { request_id?: string; page?: PageMeta }
}

const DEFAULT_TIMEOUT_MS = 15_000
const MAX_RETRY = 3

/** 后端规范化错误。status=0 表示连请求都没发出去（离线/DNS/连接被拒）。 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details?: unknown
  readonly requestId?: string

  constructor(status: number, code: string, message: string, details?: unknown, requestId?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
    this.requestId = requestId
  }

  get isOffline(): boolean {
    return this.status === 0
  }
  get isAuthError(): boolean {
    return this.status === 401
  }
  get isForbidden(): boolean {
    return this.status === 403
  }
  get isNotFound(): boolean {
    return this.status === 404
  }
  get isConflict(): boolean {
    return this.status === 409
  }
  get isValidation(): boolean {
    return this.status === 422
  }
  get isServerError(): boolean {
    return this.status >= 500
  }
}

// ---------------------------------------------------------------- 令牌

let accessToken: string | null = null
const tokenListeners = new Set<(token: string | null) => void>()

/** 写入/清除内存中的 Access Token，并通知订阅者（路由守卫据此跳转）。 */
export function setAccessToken(token: string | null): void {
  accessToken = token
  tokenListeners.forEach((fn) => fn(token))
}

export function getAccessToken(): string | null {
  return accessToken
}

export function hasAccessToken(): boolean {
  return accessToken !== null
}

/** 订阅登录态变化（登录成功 / 刷新成功 / 登出 / 刷新失败）。返回取消订阅函数。 */
export function onAuthChange(fn: (token: string | null) => void): () => void {
  tokenListeners.add(fn)
  return () => {
    tokenListeners.delete(fn)
  }
}

// ---------------------------------------------------------------- 刷新（单飞）

let refreshInFlight: Promise<boolean> | null = null

/**
 * 刷新 Access Token。并发调用共享同一个 Promise ——
 * 否则十个 401 会同时打出十次刷新，后端每次刷新都轮换令牌，九次会互相作废。
 */
export function refreshSession(): Promise<boolean> {
  if (!refreshInFlight) {
    refreshInFlight = doRefresh().finally(() => {
      refreshInFlight = null
    })
  }
  return refreshInFlight
}

async function doRefresh(): Promise<boolean> {
  try {
    const res = await fetch(`${API_BASE_URL}/auth/refresh`, {
      method: 'POST',
      credentials: 'include',
    })
    if (!res.ok) {
      setAccessToken(null)
      return false
    }
    const env = (await res.json()) as SuccessEnvelope<{ access_token?: string }>
    const token = env.data?.access_token
    if (!token) {
      setAccessToken(null)
      return false
    }
    setAccessToken(token)
    return true
  } catch {
    // 网络异常也按「刷新失败」处理：宁可跳登录，也不要留一个假登录态
    setAccessToken(null)
    return false
  }
}

// ---------------------------------------------------------------- URL 与序列化

function buildUrl(path: string, query?: RequestOptions['query']): string {
  const base = path.startsWith('http') ? path : `${API_BASE_URL}${path.startsWith('/') ? '' : '/'}${path}`
  if (!query) return base
  const sp = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      value.forEach((v) => {
        if (v !== undefined && v !== null && v !== '') sp.append(key, String(v))
      })
    } else {
      sp.append(key, String(value))
    }
  }
  const qs = sp.toString()
  return qs ? `${base}${base.includes('?') ? '&' : '?'}${qs}` : base
}

function buildHeaders(hasBody: boolean): Headers {
  const headers = new Headers({ Accept: 'application/json' })
  if (hasBody) headers.set('Content-Type', 'application/json')
  if (accessToken) headers.set('Authorization', `Bearer ${accessToken}`)
  return headers
}

function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

/** 指数退避 + 抖动：避免多个请求同时重试把后端打出一个尖峰。 */
function backoffDelay(attempt: number): number {
  return Math.min(300 * 2 ** attempt, 3_000) + Math.floor(Math.random() * 120)
}

function offlineMessage(): string {
  return typeof navigator !== 'undefined' && navigator.onLine === false
    ? '当前网络不可用，请检查网络连接后重试'
    : '连接不上服务器，请确认后台服务已启动'
}

// ---------------------------------------------------------------- 响应解析

async function toApiError(res: Response): Promise<ApiError> {
  let body: ErrorEnvelope | null = null
  try {
    body = (await res.json()) as ErrorEnvelope
  } catch {
    body = null
  }
  const code = body?.error?.code ?? `HTTP_${res.status}`
  const message = body?.error?.message ?? fallbackMessage(res.status)
  return new ApiError(res.status, code, message, body?.error?.details)
}

function fallbackMessage(status: number): string {
  if (status === 401) return '登录已失效，请重新登录'
  if (status === 403) return '没有权限进行这个操作'
  if (status === 404) return '没有找到对应的内容'
  if (status === 409) return '当前状态不允许这个操作，请刷新后重试'
  if (status === 422) return '填写的内容不符合要求，请检查后重试'
  if (status === 429) return '操作太频繁了，请稍后再试'
  if (status >= 500) return '服务器开小差了，请稍后再试'
  return `请求失败（${status}）`
}

async function unwrap<T>(res: Response): Promise<T> {
  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (!text) return undefined as T
  const env = JSON.parse(text) as SuccessEnvelope<T>
  return env.data
}

async function unwrapPaged<T>(res: Response): Promise<{ data: T; page?: PageMeta }> {
  const text = await res.text()
  if (!text) return { data: undefined as T }
  const env = JSON.parse(text) as SuccessEnvelope<T>
  return { data: env.data, page: env.meta?.page }
}

// ---------------------------------------------------------------- 核心请求

function isAuthEndpoint(path: string): boolean {
  return path.includes('/auth/refresh') || path.includes('/auth/login') || path.includes('/auth/register')
}

/** 发一次请求；网络异常统一抛 ApiError，重试交给上层 send()。 */
async function sendOnce(path: string, opts: RequestOptions): Promise<Response> {
  const hasBody = opts.body !== undefined
  const controller = new AbortController()
  let timedOut = false
  const timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS
  const timer = setTimeout(() => {
    timedOut = true
    controller.abort()
  }, timeoutMs)
  const onExternalAbort = () => controller.abort()
  if (opts.signal) {
    if (opts.signal.aborted) controller.abort()
    else opts.signal.addEventListener('abort', onExternalAbort, { once: true })
  }

  try {
    return await fetch(buildUrl(path, opts.query), {
      method: opts.method ?? 'GET',
      headers: buildHeaders(hasBody),
      body: hasBody ? JSON.stringify(opts.body) : undefined,
      // 跨域必须显式 include，否则浏览器不会带上 HttpOnly 的 Refresh Cookie
      credentials: 'include',
      signal: controller.signal,
    })
  } catch (err) {
    if (isAbortError(err)) {
      // 调用方主动取消：原样上抛，不当作错误重试
      if (!timedOut) throw err
      throw new ApiError(0, 'TIMEOUT', '请求超时了，请稍后再试')
    }
    throw new ApiError(0, 'NETWORK_ERROR', offlineMessage())
  } finally {
    clearTimeout(timer)
    if (opts.signal) opts.signal.removeEventListener('abort', onExternalAbort)
  }
}

/**
 * 统一重试策略：只有「网络错误 / 超时 / 5xx」会重试，最多 MAX_RETRY 次并指数退避。
 * 4xx 一律不重试 —— 客户端的错，重发一百次也还是错的。
 */
async function send(path: string, opts: RequestOptions): Promise<Response> {
  for (let attempt = 0; ; attempt += 1) {
    try {
      const res = await sendOnce(path, opts)
      if (res.status >= 500 && attempt < MAX_RETRY) {
        await sleep(backoffDelay(attempt))
        continue
      }
      return res
    } catch (err) {
      const retryable = err instanceof ApiError && (err.isOffline || err.code === 'TIMEOUT')
      if (retryable && attempt < MAX_RETRY) {
        await sleep(backoffDelay(attempt))
        continue
      }
      throw err
    }
  }
}

/** 主入口：发请求 → 401 静默刷新重放一次 → 返回 data。 */
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await send(path, opts)

  if (res.status === 401 && !opts.noAuthRefresh && !isAuthEndpoint(path)) {
    if (await refreshSession()) {
      const retried = await send(path, opts)
      if (!retried.ok) throw await toApiError(retried)
      return unwrap<T>(retried)
    }
  }
  if (!res.ok) throw await toApiError(res)
  return unwrap<T>(res)
}

/** 分页列表：同时拿到 data 与 meta.page。 */
export async function requestPaged<T>(
  path: string,
  opts: RequestOptions = {},
): Promise<{ data: T; page?: PageMeta }> {
  const res = await send(path, opts)
  if (res.status === 401 && !opts.noAuthRefresh && !isAuthEndpoint(path)) {
    if (await refreshSession()) {
      const retried = await send(path, opts)
      if (!retried.ok) throw await toApiError(retried)
      return unwrapPaged<T>(retried)
    }
  }
  if (!res.ok) throw await toApiError(res)
  return unwrapPaged<T>(res)
}

/** 下载二进制（打印 PDF）。失败同样抛 ApiError，但读的是 JSON 错误体。 */
export async function requestBlob(path: string, opts: RequestOptions = {}): Promise<Blob> {
  const res = await send(path, opts)
  if (res.status === 401 && !opts.noAuthRefresh) {
    if (await refreshSession()) {
      const retried = await send(path, opts)
      if (!retried.ok) throw await toApiError(retried)
      return retried.blob()
    }
  }
  if (!res.ok) throw await toApiError(res)
  return res.blob()
}

/** 组装 SSE 地址（EventSource 无法带请求头，二维码事件流把令牌放在路径里）。 */
export function sseUrl(path: string): string {
  return buildUrl(path)
}

// ---------------------------------------------------------------- 便捷方法

export const api = {
  get: <T>(path: string, opts?: Omit<RequestOptions, 'method' | 'body'>) =>
    request<T>(path, { ...opts, method: 'GET' }),
  post: <T>(path: string, body?: unknown, opts?: Omit<RequestOptions, 'method' | 'body'>) =>
    request<T>(path, { ...opts, method: 'POST', body }),
  put: <T>(path: string, body?: unknown, opts?: Omit<RequestOptions, 'method' | 'body'>) =>
    request<T>(path, { ...opts, method: 'PUT', body }),
  patch: <T>(path: string, body?: unknown, opts?: Omit<RequestOptions, 'method' | 'body'>) =>
    request<T>(path, { ...opts, method: 'PATCH', body }),
  del: <T>(path: string, opts?: Omit<RequestOptions, 'method' | 'body'>) =>
    request<T>(path, { ...opts, method: 'DELETE' }),
  paged: <T>(path: string, opts?: Omit<RequestOptions, 'method' | 'body'>) =>
    requestPaged<T>(path, { ...opts, method: 'GET' }),
}

// ---------------------------------------------------------------- 面向 UI 的文案

/**
 * 把任意异常翻成能读给家长听的一句话。
 * 4xx 用后端给的 message（后端文案本来就是面向用户的），网络类错误用本地兜底。
 */
export function readableError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.isOffline || err.code === 'TIMEOUT') return err.message
    return err.message || fallbackMessage(err.status)
  }
  if (err instanceof Error) return err.message
  return '出了点小问题，请稍后再试'
}
