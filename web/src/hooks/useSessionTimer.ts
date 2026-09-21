/**
 * 护眼计时（设计文档 §4.5）：20-20-20 休息提醒 + 单次时长上限。
 *
 * 它只影响体验，不影响判定 —— 真实时长由后端在 finish / today 时按
 * started_at / answered_at 计算，改本地时间绕不过去。
 *
 * 只有一个实例在跑（挂在 AppProviders 上），避免每个页面各起一个 ticker。
 */
import { useEffect } from 'react'
import { REST_SECONDS, useEyesStore } from '../stores/eyesStore'

export type SessionTimerOptions = {
  /** 只在孩子端学习态计时；家长端不弹休息页 */
  enabled: boolean
  /** 20-20-20 间隔（分钟），来自家长设置 rest_interval_min */
  restIntervalMin: number
  /** 单次时长上限（分钟），0 = 不限 */
  sessionLimitMin: number
}

export function useSessionTimer({ enabled, restIntervalMin, sessionLimitMin }: SessionTimerOptions): void {
  const locked = useEyesStore((s) => s.locked)

  useEffect(() => {
    if (!enabled || locked) return

    const startedAt = Date.now()
    let lastRestAt = Date.now()

    const id = window.setInterval(() => {
      const state = useEyesStore.getState()
      if (state.locked) return
      const now = Date.now()

      // 正在休息：只走休息倒计时，并把「距上次休息」重新起算（望远处不算用眼）
      if (state.restVisible) {
        state.tickRest()
        lastRestAt = now
        return
      }

      if (sessionLimitMin > 0 && now - startedAt >= sessionLimitMin * 60_000) {
        state.lock('session')
        return
      }

      if (restIntervalMin > 0 && now - lastRestAt >= restIntervalMin * 60_000) {
        state.startRest(REST_SECONDS)
        lastRestAt = now
      }
    }, 1_000)

    return () => window.clearInterval(id)
  }, [enabled, locked, restIntervalMin, sessionLimitMin])
}
