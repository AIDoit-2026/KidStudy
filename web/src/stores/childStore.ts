/**
 * 当前孩子与投屏模式。
 *
 * kidMode 的用途（§六 /select-child）：进入孩子端后隐藏家长入口，
 * 防止孩子在平板上点进家长报表或设置。
 */
import { create } from 'zustand'
import { persist } from 'zustand/middleware'

type ChildState = {
  currentChildId: string | null
  kidMode: boolean
  setCurrentChild: (id: string | null) => void
  setKidMode: (v: boolean) => void
  clear: () => void
}

export const useChildStore = create<ChildState>()(
  persist(
    (set) => ({
      currentChildId: null,
      kidMode: false,
      setCurrentChild: (currentChildId) => set({ currentChildId }),
      setKidMode: (kidMode) => set({ kidMode }),
      clear: () => set({ currentChildId: null, kidMode: false }),
    }),
    {
      name: 'kidstudy.child',
      // 只持久化这两个非敏感偏好；其余运行时状态不落盘
      partialize: (state) => ({ currentChildId: state.currentChildId, kidMode: state.kidMode }) as ChildState,
    },
  ),
)
