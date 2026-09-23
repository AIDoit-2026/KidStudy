import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'

import { LoginPage } from '../features/auth/LoginPage'
import { QrLoginPage } from '../features/auth/QrLoginPage'
import { HomePage } from '../features/child/HomePage'
import { PracticePage } from '../features/child/PracticePage'
import { SelectChildPage } from '../features/child/SelectChildPage'
import { PrintCenterPage } from '../features/parent/PrintCenterPage'
import { ReportPage } from '../features/parent/ReportPage'
import { SettingsPage } from '../features/parent/SettingsPage'
import { PrintPreviewPage } from '../features/print/PrintPreviewPage'
import { KidLayout, ParentLayout, PublicLayout, RequireAuth, RequireChild } from './layouts'

export function AppRouter() {
  return (
    <BrowserRouter>
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
    </BrowserRouter>
  )
}
