import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEffect, type ReactNode } from 'react'

import { BrightnessVeil } from '../components/eyes/BrightnessVeil'
import { applyThemeToDocument, useThemeStore } from '../stores/themeStore'
import { ErrorBoundary } from './ErrorBoundary'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // 网络错误与 5xx 的重试、退避已经在 api/client 里做过（那里知道 4xx 不该重试）。
      // 这里再开一层重试就变成「重试的重试」，只会把后端压得更狠。
      retry: false,
      staleTime: 30_000,
      refetchOnWindowFocus: false,
    },
    mutations: { retry: false },
  },
})

/** 主题、数据缓存、错误边界的统一入口。 */
export function AppProviders({ children }: { children: ReactNode }) {
  const mode = useThemeStore((s) => s.mode)
  const followSunset = useThemeStore((s) => s.followSunset)
  const fontScale = useThemeStore((s) => s.fontScale)
  const brightness = useThemeStore((s) => s.brightness)

  // 每次偏好变化就把主题写进 DOM
  useEffect(() => {
    applyThemeToDocument(mode, followSunset, fontScale, brightness)
  }, [mode, followSunset, fontScale, brightness])

  // 日落切换：定时对表即可，不必精确到分秒（切主题差异不易察觉）
  useEffect(() => {
    if (!followSunset) return
    const id = window.setInterval(() => {
      const s = useThemeStore.getState()
      applyThemeToDocument(s.mode, s.followSunset, s.fontScale, s.brightness)
    }, 5 * 60_000)
    return () => window.clearInterval(id)
  }, [followSunset])

  return (
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        {children}
        <BrightnessVeil />
      </QueryClientProvider>
    </ErrorBoundary>
  )
}
