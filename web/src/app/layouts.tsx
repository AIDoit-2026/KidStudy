/**
 * 路由外壳与守卫。
 *
 * 三条守卫：
 *  - RequireAuth：没有内存令牌时先尝试一次静默刷新（Refresh 在 HttpOnly Cookie 里，
 *    刷新页面后靠它恢复登录态），失败才跳登录页。
 *  - RequireChild：孩子端必须先选定孩子。
 *  - 打印预览路由不套任何外壳（它要的就是「屏幕上看到的 = 打印出来的」，多一层导航都不行）。
 */
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Navigate, Outlet, useNavigate } from 'react-router-dom'

import { hasAccessToken, onAuthChange } from '../api/client'
import { refreshSession } from '../api/endpoints/auth'
import { getSettings } from '../api/endpoints/parent'
import { today } from '../api/endpoints/practice'
import { FullPageLoading } from '../components/FullPageLoading'
import { LockScreen } from '../components/eyes/LockScreen'
import { RestOverlay } from '../components/eyes/RestOverlay'
import { TimeBar } from '../components/eyes/TimeBar'
import { ViewingDistanceHint } from '../components/eyes/ViewingDistanceHint'
import { useSessionTimer } from '../hooks/useSessionTimer'
import { useChildStore } from '../stores/childStore'
import { Button } from '../components/Button'

type AuthState = 'checking' | 'authed' | 'anonymous'

/** 需要登录的路由。 */
export function RequireAuth() {
  const [state, setState] = useState<AuthState>(() => (hasAccessToken() ? 'authed' : 'checking'))

  useEffect(() => {
    let alive = true
    if (!hasAccessToken()) {
      void refreshSession().then((ok) => {
        if (alive) setState(ok ? 'authed' : 'anonymous')
      })
    }
    const unsubscribe = onAuthChange((token) => setState(token ? 'authed' : 'anonymous'))
    return () => {
      alive = false
      unsubscribe()
    }
  }, [])

  if (state === 'checking') return <FullPageLoading label="正在恢复登录状态…" />
  if (state === 'anonymous') return <Navigate to="/login" replace />
  return <Outlet />
}

/** 孩子端路由：必须先选定孩子。 */
export function RequireChild() {
  const childId = useChildStore((s) => s.currentChildId)
  if (!childId) return <Navigate to="/select-child" replace />
  return <Outlet />
}

/** 登录 / 扫码页的外壳：居中卡片。 */
export function PublicLayout() {
  return (
    <main className="flex min-h-full items-center justify-center p-4">
      <div className="w-full max-w-md">
        <Outlet />
      </div>
    </main>
  )
}

/** 家长端外壳：顶部条 + 内容区。 */
export function ParentLayout() {
  const navigate = useNavigate()
  const clearChild = useChildStore((s) => s.clear)

  return (
    <div className="flex min-h-full flex-col">
      <header className="flex items-center justify-between gap-3 border-b border-line bg-surface px-4 py-3">
        <div className="flex items-center gap-3">
          <Button variant="ghost" onClick={() => navigate('/parent/report')}>
            学习报告
          </Button>
          <Button variant="ghost" onClick={() => navigate('/parent/settings')}>
            家长设置
          </Button>
          <Button variant="ghost" onClick={() => navigate('/parent/print')}>
            打印中心
          </Button>
        </div>
        <Button
          variant="ghost"
          onClick={() => {
            clearChild()
            navigate('/select-child')
          }}
        >
          切换孩子
        </Button>
      </header>
      <main className="flex-1 p-4">
        <div className="mx-auto w-full max-w-5xl">
          <Outlet />
        </div>
      </main>
    </div>
  )
}

/**
 * 孩子端外壳：顶部时长条 + 护眼机制（20-20-20 休息页 / 时长锁屏 / 大屏观看提示）。
 * 护眼只在这里启用 —— 家长端不该被孩子的休息节奏打断。
 */
export function KidLayout() {
  const childId = useChildStore((s) => s.currentChildId) ?? ''
  const navigate = useNavigate()

  const planQuery = useQuery({
    queryKey: ['today', childId],
    queryFn: () => today(childId),
    enabled: childId !== '',
  })
  const settingsQuery = useQuery({ queryKey: ['parent-settings'], queryFn: getSettings })

  const restIntervalMin = settingsQuery.data?.rest_interval_min ?? 20
  const sessionLimitMin = settingsQuery.data?.session_limit_min ?? 0

  useSessionTimer({
    enabled: childId !== '',
    restIntervalMin,
    sessionLimitMin,
  })

  // 孩子被归档/换号后 today 会 404：清掉本地选择，回到选孩子页
  useEffect(() => {
    const error = planQuery.error as { status?: number } | null
    if (error && error.status === 404) navigate('/select-child', { replace: true })
  }, [planQuery.error, navigate])

  const plan = planQuery.data

  return (
    <div className="flex min-h-full flex-col">
      <header className="border-b border-line bg-surface px-4 py-3">
        <TimeBar
          remainingMinutes={plan?.remaining_minutes ?? 0}
          usedMinutes={plan?.used_minutes ?? 0}
          dailyLimitMin={plan?.daily_limit_min ?? 0}
        />
      </header>

      <main className="flex-1 p-4">
        {planQuery.isLoading ? (
          <FullPageLoading label="正在准备今天的任务…" />
        ) : planQuery.isError ? (
          <div className="card text-center">
            <p className="mb-3">今天的任务没能加载出来。</p>
            <Button onClick={() => void planQuery.refetch()}>重试</Button>
          </div>
        ) : (
          <Outlet />
        )}
      </main>

      <RestOverlay />
      <LockScreen
        stats={[
          { label: '今日用时', value: `${plan?.used_minutes ?? 0} 分` },
          { label: '待复习', value: String(plan?.due_count ?? 0) },
          { label: '今日剩余', value: `${Math.max(0, plan?.remaining_minutes ?? 0)} 分` },
        ]}
      />
      <ViewingDistanceHint />
    </div>
  )
}
