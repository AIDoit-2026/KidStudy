/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      screens: {
        // 四档断点（设计文档 §六）：手机 <640 / 平板 640–1023 / 桌面 1024–1599 / 大屏 ≥1600
        sm: '640px',
        md: '768px',
        lg: '1024px',
        xl: '1280px',
        '2xl': '1600px',
      },
      colors: {
        // 颜色全部走 CSS 变量：换主题只换变量，不用改任何组件类名
        paper: 'var(--c-paper)',
        surface: 'var(--c-surface)',
        raised: 'var(--c-raised)',
        ink: 'var(--c-ink)',
        'ink-soft': 'var(--c-ink-soft)',
        brand: 'var(--c-brand)',
        'brand-ink': 'var(--c-brand-ink)',
        accent: 'var(--c-accent)',
        line: 'var(--c-line)',
        danger: 'var(--c-danger)',
        success: 'var(--c-success)',
      },
      borderRadius: {
        card: '1rem',
      },
      transitionDuration: {
        // 尊重 prefers-reduced-motion：变量在 reduced 时归零（见 styles/index.css）
        ui: 'var(--motion-duration, 180ms)',
      },
    },
  },
  plugins: [],
}
