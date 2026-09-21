import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { AppProviders } from './app/providers'
import { AppRouter } from './app/router'
import './styles/index.css'

const container = document.getElementById('root')
if (!container) {
  throw new Error('找不到 #root 挂载点：index.html 被改坏了？')
}

createRoot(container).render(
  <StrictMode>
    <AppProviders>
      <AppRouter />
    </AppProviders>
  </StrictMode>,
)
