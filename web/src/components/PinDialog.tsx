/**
 * 家长 PIN 弹窗。护眼锁屏解锁、休息跳过、切换孩子都要它。
 *
 * 校验走后端 /auth/pin/verify（失败锁定也在服务端，前端改不了），
 * 成功后拿到的是短期 unlock 令牌，不是 access 令牌 —— 两者用途隔离。
 */
import { useEffect, useRef, useState } from 'react'

import { readableError } from '../api/client'
import { verifyPin } from '../api/endpoints/auth'

type Props = {
  open: boolean
  title?: string
  description?: string
  onClose: () => void
  onUnlocked: (unlockToken: string) => void
}

export function PinDialog({
  open,
  title = '请输入家长 PIN',
  description,
  onClose,
  onUnlocked,
}: Props) {
  const [pin, setPin] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const unlockedRef = useRef(onUnlocked)

  useEffect(() => {
    unlockedRef.current = onUnlocked
  }, [onUnlocked])

  useEffect(() => {
    if (!open) return
    setPin('')
    setError('')
    setBusy(false)
    const timer = window.setTimeout(() => inputRef.current?.focus(), 60)
    return () => window.clearTimeout(timer)
  }, [open])

  if (!open) return null

  const submit = async () => {
    const value = pin.trim()
    if (value.length < 4) {
      setError('PIN 是 4–6 位数字')
      return
    }
    setBusy(true)
    setError('')
    try {
      const res = await verifyPin(value)
      setPin('')
      unlockedRef.current(res.unlock_token)
    } catch (err) {
      setError(readableError(err))
      setPin('')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4"
      role="dialog"
      aria-modal="true"
      aria-label={title}
    >
      <div className="card w-full max-w-sm">
        <h2 className="mb-1 text-lg font-bold">{title}</h2>
        {description && <p className="mb-3 text-sm text-ink-soft">{description}</p>}
        <input
          ref={inputRef}
          type="password"
          inputMode="numeric"
          autoComplete="off"
          maxLength={6}
          value={pin}
          onChange={(event) => setPin(event.target.value.replace(/\D/g, ''))}
          onKeyDown={(event) => {
            if (event.key === 'Enter') void submit()
          }}
          className="mb-2 w-full rounded-xl border border-line bg-surface px-4 py-3 text-center text-2xl tracking-[0.5em] text-ink"
          placeholder="••••"
          aria-label="家长 PIN"
        />
        {error && <p className="mb-2 text-sm text-danger">{error}</p>}
        <div className="flex gap-2">
          <button type="button" className="btn flex-1" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button
            type="button"
            className="btn btn-primary flex-1"
            onClick={() => void submit()}
            disabled={busy}
          >
            {busy ? '校验中…' : '解锁'}
          </button>
        </div>
      </div>
    </div>
  )
}
