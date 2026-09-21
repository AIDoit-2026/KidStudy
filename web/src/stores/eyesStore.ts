/**
 * 护眼运行时状态（刻意**不持久化**）。
 *
 * 计时是「本次学习」的事：刷新页面就当重新坐下，不该把上次的累计时长继承过来。
 * 真正的时长权威在后端（§4.5 倒计时权威性），这里只负责体验。
 */
import { create } from 'zustand'

/** 锁屏原因：单次时长到点 / 当日总时长到点 */
export type LockReason = 'session' | 'daily'

/** 20-20-20 的休息时长：看屏幕 20 分钟，望远处 20 秒。 */
export const REST_SECONDS = 20

type EyesState = {
  restVisible: boolean
  restSecondsLeft: number
  locked: boolean
  lockReason: LockReason | null
  startRest: (seconds?: number) => void
  /** 递减休息倒计时；返回剩余秒数（到 0 自动结束休息） */
  tickRest: () => number
  endRest: () => void
  lock: (reason: LockReason) => void
  unlock: () => void
}

export const useEyesStore = create<EyesState>((set, get) => ({
  restVisible: false,
  restSecondsLeft: 0,
  locked: false,
  lockReason: null,

  startRest: (seconds = REST_SECONDS) => set({ restVisible: true, restSecondsLeft: seconds }),

  tickRest: () => {
    const left = get().restSecondsLeft - 1
    if (left <= 0) {
      set({ restVisible: false, restSecondsLeft: 0 })
      return 0
    }
    set({ restSecondsLeft: left })
    return left
  },

  endRest: () => set({ restVisible: false, restSecondsLeft: 0 }),

  lock: (reason) => set({ locked: true, lockReason: reason }),

  unlock: () => set({ locked: false, lockReason: null }),
}))
