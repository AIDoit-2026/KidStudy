/**
 * 大屏键盘导航（M6 验收项之一：大屏方向键可答题）。
 *
 * 为什么不依赖 :hover：大屏 + 触屏都存在，选中态必须由「状态」驱动而不是「鼠标位置」，
 * 所以这里返回 activeIndex，由页面给对应元素加 .is-active 类。
 */
import { useCallback, useEffect, useRef, useState } from 'react'

export type KeyboardNavOptions = {
  count: number
  /** 关掉时（例如正在显示弹窗）不响应按键 */
  enabled?: boolean
  /** 网格布局的列数；1 表示单列，上下移动 */
  columns?: number
  onConfirm?: (index: number) => void
  onCancel?: () => void
}

export function useKeyboardNav({
  count,
  enabled = true,
  columns = 1,
  onConfirm,
  onCancel,
}: KeyboardNavOptions) {
  const [activeIndex, setActiveIndexState] = useState(0)
  const activeIndexRef = useRef(0)

  // 回调放 ref：父组件每次渲染都会传新函数，直接进 deps 会不停解绑/重绑键盘监听
  const confirmRef = useRef(onConfirm)
  const cancelRef = useRef(onCancel)
  useEffect(() => {
    confirmRef.current = onConfirm
  }, [onConfirm])
  useEffect(() => {
    cancelRef.current = onCancel
  }, [onCancel])

  const setActiveIndex = useCallback((index: number) => {
    activeIndexRef.current = index
    setActiveIndexState(index)
  }, [])

  // 题目数量变化时把选中项收进合法范围
  useEffect(() => {
    const max = Math.max(0, count - 1)
    if (activeIndexRef.current > max) setActiveIndex(max)
  }, [count, setActiveIndex])

  useEffect(() => {
    if (!enabled || count <= 0) return

    const move = (delta: number) => {
      const next = activeIndexRef.current + delta
      if (next < 0 || next >= count) return
      setActiveIndex(next)
    }

    const onKeyDown = (event: KeyboardEvent) => {
      // 正在输入框里打字时别抢键
      const target = event.target as HTMLElement | null
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
        return
      }

      switch (event.key) {
        case 'ArrowDown':
          move(columns)
          break
        case 'ArrowUp':
          move(-columns)
          break
        case 'ArrowRight':
          move(columns > 1 ? 1 : columns)
          break
        case 'ArrowLeft':
          move(columns > 1 ? -1 : -columns)
          break
        case 'Home':
          setActiveIndex(0)
          break
        case 'End':
          setActiveIndex(count - 1)
          break
        case 'Enter':
          event.preventDefault()
          confirmRef.current?.(activeIndexRef.current)
          return
        case 'Escape':
          cancelRef.current?.()
          return
        default:
          // 数字键直选第 N 项
          if (/^[1-9]$/.test(event.key)) {
            const index = Number(event.key) - 1
            if (index < count) setActiveIndex(index)
            event.preventDefault()
          }
          return
      }
      // 方向键默认会滚动页面，必须拦掉
      event.preventDefault()
    }

    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [enabled, count, columns, setActiveIndex])

  return { activeIndex, setActiveIndex }
}
