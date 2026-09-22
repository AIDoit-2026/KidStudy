/**
 * 20-20-20 全屏休息页（§4.5）。
 *
 * 倒计时由 useSessionTimer 每秒推进（集中一处计时），这里只负责展示与「家长跳过」。
 * 跳过要 PIN —— 否则孩子会自己点掉，等于没有这个机制。
 */
import { useEffect, useState } from 'react'

import { useEyesStore } from '../../stores/eyesStore'
import { PinDialog } from '../PinDialog'

export function RestOverlay() {
  const visible = useEyesStore((s) => s.restVisible)
  const secondsLeft = useEyesStore((s) => s.restSecondsLeft)
  const endRest = useEyesStore((s) => s.endRest)
  const [pinOpen, setPinOpen] = useState(false)

  // 休息页铺满屏幕时禁掉背景滚动，否则能看到内容在后面滑
  useEffect(() => {
    if (!visible) return
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = previous
    }
  }, [visible])

  if (!visible) return null

  return (
    <>
      <div data-testid="rest-overlay" className="fixed inset-0 z-50 flex flex-col items-center justify-center gap-6 bg-paper px-6 text-center">
        <p className="text-2xl font-bold md:text-4xl">让眼睛歇一会儿</p>
        <p className="max-w-md text-ink-soft">
          抬头看看 6 米以外的地方，或者窗外最远的那个点。眼睛也要做操。
        </p>
        <div data-testid="rest-countdown" className="text-6xl font-bold tabular-nums text-brand md:text-7xl" aria-live="polite">
          {secondsLeft}
        </div>
        <button type="button" className="btn" onClick={() => setPinOpen(true)}>
          我休息好了（家长解锁）
        </button>
        <p className="text-sm text-ink-soft">不想等倒计时？请家长输入 PIN 直接跳过。</p>
      </div>
      <PinDialog
        open={pinOpen}
        title="跳过休息"
        description="请输入家长 PIN 确认"
        onClose={() => setPinOpen(false)}
        onUnlocked={() => {
          setPinOpen(false)
          endRest()
        }}
      />
    </>
  )
}
