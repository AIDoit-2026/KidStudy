/** 四档断点（设计文档 §六）：手机 / 平板 / 桌面 / 大屏。 */
import { useEffect, useState } from 'react'

export type Breakpoint = 'phone' | 'tablet' | 'desktop' | 'big'

export const BREAKPOINTS: Record<Breakpoint, number> = {
  phone: 0,
  tablet: 640,
  desktop: 1024,
  big: 1600,
}

function classify(width: number): Breakpoint {
  if (width >= BREAKPOINTS.big) return 'big'
  if (width >= BREAKPOINTS.desktop) return 'desktop'
  if (width >= BREAKPOINTS.tablet) return 'tablet'
  return 'phone'
}

/**
 * 用 matchMedia 而不是 resize 事件：前者只在跨档时回调，不会在拖窗口时每帧触发重渲染。
 */
export function useBreakpoint(): Breakpoint {
  const [bp, setBp] = useState<Breakpoint>(() => classify(window.innerWidth))

  useEffect(() => {
    const queries: Array<[Breakpoint, MediaQueryList]> = (['tablet', 'desktop', 'big'] as const).map(
      (name) => [name, window.matchMedia(`(min-width: ${BREAKPOINTS[name]}px)`)],
    )
    const update = () => setBp(classify(window.innerWidth))
    queries.forEach(([, mq]) => mq.addEventListener('change', update))
    update()
    return () => queries.forEach(([, mq]) => mq.removeEventListener('change', update))
  }, [])

  return bp
}

/** 是否大屏（≥1600px）：观看距离提示与键盘导航只在它上面出现。 */
export function useIsBigScreen(): boolean {
  return useBreakpoint() === 'big'
}
