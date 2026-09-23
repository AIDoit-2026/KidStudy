/**
 * 单次时长到点锁屏（§4.5）。遮罩上给今日战绩 + 家长 PIN 解锁。
 *
 * 「解锁后继续 20 分钟」的语义由 useSessionTimer 承担：
 * locked 变回 false 会让计时 effect 重跑，startedAt 随之重置。
 */
import { useState } from 'react'

import { useEyesStore } from '../../stores/eyesStore'
import { PinDialog } from '../PinDialog'

export type LockStat = { label: string; value: string }

export function LockScreen({ stats = [] }: { stats?: LockStat[] }) {
  const locked = useEyesStore((s) => s.locked)
  const reason = useEyesStore((s) => s.lockReason)
  const unlock = useEyesStore((s) => s.unlock)
  const [pinOpen, setPinOpen] = useState(false)

  if (!locked) return null

  const title = reason === 'daily' ? '今天的学习时间用完啦' : '这一节的时间到啦'

  return (
    <>
      {/* 同 RestOverlay：外层可滚动 + 内层 min-h-full 居中，内容超高也不裁切 */}
      <div data-testid="lock-screen" className="fixed inset-0 z-50 overflow-y-auto bg-paper">
        <div className="flex min-h-full flex-col items-center justify-center gap-5 px-6 py-8 text-center">
          <p className="text-2xl font-bold md:text-3xl">{title}</p>
          <p className="max-w-md text-ink-soft">
            眼睛和大脑都需要休息。今天就到这里，明天再来吧。
          </p>

          {stats.length > 0 && (
            <div className="grid w-full max-w-md grid-cols-3 gap-3">
              {stats.map((item) => (
                <div key={item.label} className="card text-center">
                  <div className="text-2xl font-bold text-brand">{item.value}</div>
                  <div className="text-xs text-ink-soft">{item.label}</div>
                </div>
              ))}
            </div>
          )}

          <button type="button" className="btn" onClick={() => setPinOpen(true)}>
            家长解锁继续
          </button>
        </div>
      </div>
      <PinDialog
        open={pinOpen}
        title="家长解锁"
        description="输入 PIN 可以再继续一会儿"
        onClose={() => setPinOpen(false)}
        onUnlocked={() => {
          setPinOpen(false)
          unlock()
        }}
      />
    </>
  )
}
