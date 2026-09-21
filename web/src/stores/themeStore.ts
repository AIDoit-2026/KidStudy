/**
 * 主题 / 字号 / 亮度（设计文档 §4.5：Zustand 持久化，米黄纸感默认，日落自动切深色）。
 *
 * 为什么这些能进 localStorage：它们是纯粹的显示偏好，不含任何身份信息。
 * （Access Token 依然只在内存里，两件事不冲突。）
 */
import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export type ThemeMode = 'sepia' | 'light' | 'dark' | 'auto'
/** 解析后的实际主题（auto 已按时间落定） */
export type ResolvedTheme = 'sepia' | 'light' | 'dark'

export const FONT_SCALE_MIN = 0.9
export const FONT_SCALE_MAX = 1.4
export const BRIGHTNESS_MIN = 0.6
export const BRIGHTNESS_MAX = 1.2

/** 入夜时间：18:00 之后、07:00 之前算夜间，自动切深色。 */
const NIGHT_START_HOUR = 18
const NIGHT_END_HOUR = 7

export function resolveThemeMode(mode: ThemeMode, followSunset: boolean, now = new Date()): ResolvedTheme {
  if (mode === 'light' || mode === 'dark' || mode === 'sepia') {
    // 明确选了浅色/纸感时，只有「跟随日落」开着才在夜里压成深色
    if (followSunset && mode !== 'dark') {
      const h = now.getHours()
      if (h >= NIGHT_START_HOUR || h < NIGHT_END_HOUR) return 'dark'
    }
    return mode
  }
  const h = now.getHours()
  return h >= NIGHT_START_HOUR || h < NIGHT_END_HOUR ? 'dark' : 'sepia'
}

/** 把状态写进 DOM：主题属性 + 三个 CSS 变量。 */
export function applyThemeToDocument(mode: ThemeMode, followSunset: boolean, fontScale: number, brightness: number): void {
  if (typeof document === 'undefined') return
  const el = document.documentElement
  el.dataset.theme = resolveThemeMode(mode, followSunset)

  const scale = clamp(fontScale, FONT_SCALE_MIN, FONT_SCALE_MAX)
  el.style.setProperty('--font-scale', String(scale))

  const b = clamp(brightness, BRIGHTNESS_MIN, BRIGHTNESS_MAX)
  // 降亮度叠黑、提亮度叠白。alpha 在 JS 里算好：CSS 的 opacity 不接受负值，
  // 若把 max(0, x) 写在 CSS 里会让整条声明失效。
  const dim = b < 1 ? (1 - b) * 0.65 : 0
  const boost = b > 1 ? (b - 1) * 0.45 : 0
  el.style.setProperty('--veil-dark', dim.toFixed(4))
  el.style.setProperty('--veil-light', boost.toFixed(4))
}

function clamp(v: number, min: number, max: number): number {
  if (Number.isNaN(v)) return min
  return Math.min(max, Math.max(min, v))
}

type ThemeState = {
  mode: ThemeMode
  followSunset: boolean
  fontScale: number
  brightness: number
  setMode: (mode: ThemeMode) => void
  setFollowSunset: (v: boolean) => void
  setFontScale: (v: number) => void
  setBrightness: (v: number) => void
  reset: () => void
}

const DEFAULTS = {
  mode: 'sepia' as ThemeMode,
  followSunset: true,
  fontScale: 1,
  brightness: 1,
}

export const useThemeStore = create<ThemeState>()(
  persist(
    (set, get) => ({
      ...DEFAULTS,
      setMode: (mode) => {
        set({ mode })
        applyThemeToDocument(mode, get().followSunset, get().fontScale, get().brightness)
      },
      setFollowSunset: (followSunset) => {
        set({ followSunset })
        applyThemeToDocument(get().mode, followSunset, get().fontScale, get().brightness)
      },
      setFontScale: (fontScale) => {
        const v = clamp(fontScale, FONT_SCALE_MIN, FONT_SCALE_MAX)
        set({ fontScale: v })
        applyThemeToDocument(get().mode, get().followSunset, v, get().brightness)
      },
      setBrightness: (brightness) => {
        const v = clamp(brightness, BRIGHTNESS_MIN, BRIGHTNESS_MAX)
        set({ brightness: v })
        applyThemeToDocument(get().mode, get().followSunset, get().fontScale, v)
      },
      reset: () => {
        set({ ...DEFAULTS })
        applyThemeToDocument(DEFAULTS.mode, DEFAULTS.followSunset, DEFAULTS.fontScale, DEFAULTS.brightness)
      },
    }),
    {
      name: 'kidstudy.theme',
      // index.html 的首屏内联脚本按同样的结构读这份数据（避免刷新闪一下），改动要同步
      onRehydrateStorage: () => (state) => {
        if (state) {
          applyThemeToDocument(state.mode, state.followSunset, state.fontScale, state.brightness)
        }
      },
    },
  ),
)
