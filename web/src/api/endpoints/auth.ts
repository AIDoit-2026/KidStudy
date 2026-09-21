/** 认证：注册 / 登录 / 刷新 / 登出 / 家长信息 / PIN / 扫码登录。 */
import { api, setAccessToken, sseUrl } from '../client'
import type {
  LoginRequest,
  ParentView,
  QRStartResult,
  RegisterRequest,
  Session,
  UnlockResult,
} from '../types'

export { refreshSession } from '../client'

/** 注册并直接登录（后端同时下发 HttpOnly Refresh Cookie）。 */
export async function register(req: RegisterRequest): Promise<Session> {
  const session = await api.post<Session>('/auth/register', req, { noAuthRefresh: true })
  setAccessToken(session.access_token)
  return session
}

export async function login(req: LoginRequest): Promise<Session> {
  const session = await api.post<Session>('/auth/login', req, { noAuthRefresh: true })
  setAccessToken(session.access_token)
  return session
}

/** 登出：后端撤销 Refresh 令牌；无论后端结果如何，本地内存令牌必须清干净。 */
export async function logout(): Promise<void> {
  try {
    await api.post<{ logged_out: boolean }>('/auth/logout')
  } finally {
    setAccessToken(null)
  }
}

export const me = () => api.get<ParentView>('/auth/me')

export const setPin = (pin: string) => api.put<{ has_pin: boolean }>('/auth/pin', { pin })

/** 校验 PIN，成功后拿到短期解锁令牌（护眼锁屏与切换孩子都要它）。 */
export const verifyPin = (pin: string) => api.post<UnlockResult>('/auth/pin/verify', { pin })

// ---------------------------------------------------------------- 扫码登录

export const qrStart = () => api.get<QRStartResult>('/auth/qrcode', { noAuthRefresh: true })

/**
 * 扫码事件流地址。EventSource 无法携带 Authorization 头，所以后端把一次性令牌
 * 放在路径里（该令牌 60 秒过期且只能订阅一次，风险可控）。
 */
export const qrEventsUrl = (qrToken: string) =>
  sseUrl(`/auth/qrcode/${encodeURIComponent(qrToken)}/events`)

/** 手机端扫码上报 / 确认（这两个需要已登录，即「手机已登录，用来授权大屏」）。 */
export const qrScan = (qrToken: string) =>
  api.post<{ status: string }>('/auth/qrcode/scan', { qr_token: qrToken })

export const qrConfirm = (qrToken: string) =>
  api.post<{ status: string }>('/auth/qrcode/confirm', { qr_token: qrToken })

/** 大屏端凭一次性兑换码换取会话。 */
export async function qrExchange(qrToken: string, exchangeCode: string): Promise<Session> {
  const session = await api.post<Session>(
    '/auth/qrcode/exchange',
    { qr_token: qrToken, exchange_code: exchangeCode },
    { noAuthRefresh: true },
  )
  setAccessToken(session.access_token)
  return session
}
