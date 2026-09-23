/** 全页加载态：任何「等数据」的地方都该有它，而不是留白。 */
import { usePrefersReducedMotion } from '../hooks/usePrefersReducedMotion'

export function FullPageLoading({ label = '加载中…' }: { label?: string }) {
  // 系统开了「减少动态效果」就不转：CSS 的 reduced-motion 媒体查询已把动画时长压到 0.01ms，
  // 这里再显式去掉 animate-spin，语义更清楚（别让评审以为旋转被漏掉了）。
  const reduced = usePrefersReducedMotion()
  return (
    <div className="flex min-h-full items-center justify-center p-8" role="status" aria-live="polite">
      <div className="text-center text-ink-soft">
        <div
          className={`mx-auto mb-3 h-8 w-8 rounded-full border-2 border-line border-t-brand ${
            reduced ? '' : 'animate-spin'
          }`}
        />
        {label}
      </div>
    </div>
  )
}
