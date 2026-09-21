import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 前端固定 5173；后端在 18080，跨域由后端 CORS_ALLOWED_ORIGINS 显式放行。
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: true,
  },
  resolve: {
    // 用 root 相对路径做别名：够用，且不必为了 path.resolve 多引一个 @types/node
    alias: { '@': '/src' },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
})
