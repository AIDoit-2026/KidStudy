/**
 * 大屏扫码登录（§六 /qr-login）。
 *
 * 四态：等待扫码 → 已扫码待确认 → 成功（自动跳转）→ 已过期（自动刷新二维码）。
 * SSE 由浏览器原生 EventSource 承载，后端每 15 秒发一次心跳防代理断连。
 */
import { QRCodeSVG } from 'qrcode.react'
import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import { readableError } from '../../api/client'
import { qrEventsUrl, qrExchange, qrStart } from '../../api/endpoints/auth'
import type { QRConfirmedPayload, QRPhase, QRScannedPayload, QRStartResult } from '../../api/types'
import { Button } from '../../components/Button'

/** 二维码过期后自动重来，给用户一点反应时间 */
const AUTO_REFRESH_DELAY_MS = 1_500

export function QrLoginPage() {
  const navigate = useNavigate()
  const [phase, setPhase] = useState<QRPhase>('loading')
  const [qr, setQr] = useState<QRStartResult | null>(null)
  const [scanned, setScanned] = useState<QRScannedPayload | null>(null)
  const [error, setError] = useState('')

  const start = useCallback(async () => {
    setPhase('loading')
    setError('')
    setScanned(null)
    try {
      setQr(await qrStart())
      setPhase('waiting')
    } catch (err) {
      setError(readableError(err))
      setPhase('failed')
    }
  }, [])

  useEffect(() => {
    void start()
  }, [start])

  useEffect(() => {
    if (!qr) return

    const source = new EventSource(qrEventsUrl(qr.qr_token))

    source.addEventListener('scanned', (event) => {
      setScanned(JSON.parse((event as MessageEvent).data) as QRScannedPayload)
      setPhase('scanned')
    })

    source.addEventListener('confirmed', (event) => {
      const payload = JSON.parse((event as MessageEvent).data) as QRConfirmedPayload
      setPhase('confirmed')
      source.close()
      void qrExchange(qr.qr_token, payload.exchange_code)
        .then(() => navigate('/select-child', { replace: true }))
        .catch((err: unknown) => {
          setError(readableError(err))
          setPhase('failed')
        })
    })

    source.addEventListener('expired', () => {
      source.close()
      setPhase('expired')
    })

    source.addEventListener('failed', (event) => {
      source.close()
      const payload = JSON.parse((event as MessageEvent).data) as { reason?: string }
      setError(payload.reason ?? '这次扫码没能完成，请重新扫码')
      setPhase('failed')
    })

    return () => source.close()
  }, [qr, navigate])

  // 过期后自动换一张新码，不用家长手动点
  useEffect(() => {
    if (phase !== 'expired') return
    const timer = window.setTimeout(() => void start(), AUTO_REFRESH_DELAY_MS)
    return () => window.clearTimeout(timer)
  }, [phase, start])

  return (
    <div className="card text-center">
      <h1 className="mb-1 text-2xl font-bold">扫码登录</h1>
      <p className="mb-5 text-sm text-ink-soft">用已经登录的手机扫一下，大屏就能同步登录。</p>

      {/* role=status + aria-live：扫码四态变化（已扫码 / 登录成功 / 已过期）要能被读屏听到 */}
      <div
        className="mx-auto mb-4 flex h-56 w-56 items-center justify-center rounded-2xl border border-line bg-white p-3"
        role="status"
        aria-live="polite"
      >
        {phase === 'waiting' && qr ? (
          <span role="img" aria-label="登录二维码，请用已登录的手机扫描">
            <QRCodeSVG value={qr.deep_link} size={200} level="M" />
          </span>
        ) : phase === 'scanned' ? (
          <div className="text-ink-soft">
            <p className="text-lg font-semibold text-ink">已扫码</p>
            <p className="mt-1 text-sm">
              {scanned?.device_hint || '手机'} 正在确认…
            </p>
          </div>
        ) : phase === 'confirmed' ? (
          <p className="text-lg font-semibold text-success">登录成功，正在进入…</p>
        ) : phase === 'expired' ? (
          <p className="text-ink-soft">二维码已过期，正在刷新…</p>
        ) : phase === 'loading' ? (
          <p className="text-ink-soft">正在生成二维码…</p>
        ) : (
          <p className="text-danger">{error || '二维码生成失败了'}</p>
        )}
      </div>

      {phase === 'scanned' && scanned && (
        <p className="mb-3 text-sm text-ink-soft">
          来源设备：{scanned.device_hint || '未知'} · IP 段 {scanned.ip_prefix || '未知'}
        </p>
      )}
      {phase === 'failed' && (
        <Button className="mb-3" onClick={() => void start()}>
          重新生成
        </Button>
      )}

      <p className="text-sm">
        <Link className="text-brand underline" to="/login">
          改用账号密码登录
        </Link>
      </p>
    </div>
  )
}
