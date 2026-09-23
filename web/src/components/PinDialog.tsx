/**
 * 家长 PIN 弹窗。护眼锁屏解锁、休息跳过、切换孩子都要它。
 *
 * 校验走后端 /auth/pin/verify（失败锁定也在服务端，前端改不了），
 * 成功后拿到的是短期 unlock 令牌，不是 access 令牌 —— 两者用途隔离。
 */
import { useEffect, useRef, useState, type KeyboardEvent } from 'react'

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
  const panelRef = useRef<HTMLDivElement>(null)
  const unlockedRef = useRef(onUnlocked)

  useEffect(() => {
    unlockedRef.current = onUnlocked
  }, [onUnlocked])

  useEffect(() => {
    if (!open) return
    // 记住触发弹窗的元素，关闭时把焦点还回去，键盘用户不至于掉回页面顶部
    const previous = document.activeElement as HTMLElement | null
    setPin('')
    setError('')
    setBusy(false)
    const timer = window.setTimeout(() => inputRef.current?.focus(), 60)
    return () => {
      window.clearTimeout(timer)
      previous?.focus?.()
    }
  }, [open])

  // 焦点陷阱 + Esc 关闭：aria-modal 只声明「背景不可交互」，
  // 但键盘仍能 Tab 出去，所以要把焦点圈在弹窗内（无障碍走查项）。
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      onClose()
      return
    }
    if (event.key !== 'Tab') return
    const root = panelRef.current
    if (!root) return
    const focusables = root.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])',
    )
    if (focusables.length === 0) return
    const first = focusables[0]
    const last = focusables[focusables.length - 1]
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first.focus()
    }
  }

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
      className="fixed inset-0 z-[60] overflow-y-auto bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-label={title}
    >
      {/* 内层负责居中：屏幕矮 + 大字号时弹窗可滚，不至于把「解锁」按钮顶出视口 */}
      <div className="flex min-h-full items-center justify-center p-4">
        <div ref={panelRef} onKeyDown={onKeyDown} className="card w-full max-w-sm">
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
    </div>
  )
}
