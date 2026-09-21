/** 全页加载态：任何「等数据」的地方都该有它，而不是留白。 */
export function FullPageLoading({ label = '加载中…' }: { label?: string }) {
  return (
    <div className="flex min-h-full items-center justify-center p-8" role="status" aria-live="polite">
      <div className="text-center text-ink-soft">
        <div className="mx-auto mb-3 h-8 w-8 animate-spin rounded-full border-2 border-line border-t-brand" />
        {label}
      </div>
    </div>
  )
}
