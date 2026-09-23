/**
 * 大屏观看距离提示（§4.5）：>1600px 首次进入时提示一次，看过就不再打扰。
 * 记在 localStorage —— 是显示偏好，不是敏感数据。
 */
import { useEffect, useState } from 'react'

import { useIsBigScreen } from '../../hooks/useBreakpoint'

const SEEN_KEY = 'kidstudy.viewingHintSeen'

export function ViewingDistanceHint() {
  const isBigScreen = useIsBigScreen()
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    if (!isBigScreen) return
    try {
      if (localStorage.getItem(SEEN_KEY) === '1') return
    } catch {
      // 隐私模式下读不到 storage：那就每次都提示，不影响功能
    }
    setVisible(true)
  }, [isBigScreen])

  if (!visible) return null

  const dismiss = () => {
    try {
      localStorage.setItem(SEEN_KEY, '1')
    } catch {
      // 存不下也无所谓
    }
    setVisible(false)
  }

  return (
    <div
      className="fixed inset-x-0 bottom-4 z-30 mx-auto flex w-[min(92vw,34rem)] items-center gap-3 rounded-2xl border border-line bg-raised p-4 shadow-lg"
      role="status"
    >
      <span className="text-2xl" aria-hidden="true">
        👀
      </span>
      <p className="flex-1 text-sm">
        大屏观看请保持 <strong>2 米以上</strong> 距离，坐正、视线略低于屏幕中心。
      </p>
      <button type="button" className="btn" onClick={dismiss}>
        知道了
      </button>
    </div>
  )
}
