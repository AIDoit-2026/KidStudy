import { lazy, Suspense } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'

import { FullPageLoading } from '../components/FullPageLoading'
import { KidLayout, ParentLayout, PublicLayout, RequireAuth, RequireChild } from './layouts'

/**
 * 路由级代码分割（§10.10 M7 第 5 条）。
 *
 * 页面是**命名导出**（不是 default），所以 lazy 里要把 `m.XxxPage` 包成 `{ default }`。
 * 分割后孩子端与家长端各自成 chunk：孩子端永远不会下载 qrcode.react / 报表 / 打印中心，
 * 首屏只加载当前路由真正需要的那一份。
 *
 * 为什么放在这里而不是逐个 import()：一处集中声明，新增页面时只改这一屏，
 * 也不会有人不小心写出静态 import 把整包又粘回去。
 */
const LoginPage = lazy(() => import('../features/auth/LoginPage').then((m) => ({ default: m.LoginPage })))
const QrLoginPage = lazy(() =>
  import('../features/auth/QrLoginPage').then((m) => ({ default: m.QrLoginPage })),
)
const HomePage = lazy(() => import('../features/child/HomePage').then((m) => ({ default: m.HomePage })))
const PracticePage = lazy(() =>
  import('../features/child/PracticePage').then((m) => ({ default: m.PracticePage })),
)
const SelectChildPage = lazy(() =>
  import('../features/child/SelectChildPage').then((m) => ({ default: m.SelectChildPage })),
)
const PrintCenterPage = lazy(() =>
  import('../features/parent/PrintCenterPage').then((m) => ({ default: m.PrintCenterPage })),
)
const ReportPage = lazy(() =>
  import('../features/parent/ReportPage').then((m) => ({ default: m.ReportPage })),
)
const SettingsPage = lazy(() =>
  import('../features/parent/SettingsPage').then((m) => ({ default: m.SettingsPage })),
)
const PrintPreviewPage = lazy(() =>
  import('../features/print/PrintPreviewPage').then((m) => ({ default: m.PrintPreviewPage })),
)

export function AppRouter() {
  return (
    <BrowserRouter>
      {/* 懒加载期间显示整页加载态，避免布局跳动（切换路由时才出现，不影响已渲染页面） */}
      <Suspense fallback={<FullPageLoading label="正在打开…" />}>
        <Routes>
          {/*
            打印预览走独立路由：不挂导航、不挂护眼计时。
            家长在电脑上按 Ctrl+P 时不该被「该休息了」打断，
            而且这一页与后端渲染 PDF 用的是同一份 HTML。
          */}
          <Route path="/print/:jobId" element={<PrintPreviewPage />} />

          <Route element={<PublicLayout />}>
            <Route path="/login" element={<LoginPage />} />
            <Route path="/qr-login" element={<QrLoginPage />} />
          </Route>

          <Route element={<RequireAuth />}>
            <Route path="/select-child" element={<SelectChildPage />} />

            <Route element={<RequireChild />}>
              <Route element={<KidLayout />}>
                <Route path="/child/home" element={<HomePage />} />
                <Route path="/child/practice/:subject" element={<PracticePage />} />
              </Route>
            </Route>

            <Route element={<ParentLayout />}>
              <Route path="/parent/report" element={<ReportPage />} />
              <Route path="/parent/settings" element={<SettingsPage />} />
              <Route path="/parent/print" element={<PrintCenterPage />} />
            </Route>
          </Route>

          <Route path="*" element={<Navigate to="/child/home" replace />} />
        </Routes>
      </Suspense>
    </BrowserRouter>
  )
}
