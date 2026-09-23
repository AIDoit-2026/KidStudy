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
    rollupOptions: {
      output: {
        // 框架与数据层单独成块：它们很少随业务改动，命中长效缓存；
        // 业务页面已由 router 的 lazy 各自成 chunk（见 app/router.tsx）。
        // 注意别加「catch-all vendor」——否则 qrcode.react 这类只被单个懒加载页
        // 用到的库会被提到公共块，首屏白白多下几十 KB。
        manualChunks: {
          'react-vendor': ['react', 'react-dom', 'react-router-dom'],
          'query-vendor': ['@tanstack/react-query', 'zustand'],
        },
      },
    },
  },
})
