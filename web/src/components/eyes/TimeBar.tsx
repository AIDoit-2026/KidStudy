/**
 * 顶部剩余时长条（§4.5）。数值来自 GET /practice/today 的 remaining_minutes，
 * 服务端才是权威；前端不做本地扣减，避免刷新后被「还回来的时间」迷惑。
 */
type Props = {
  remainingMinutes: number
  usedMinutes: number
  dailyLimitMin: number
}

export function TimeBar({ remainingMinutes, usedMinutes, dailyLimitMin }: Props) {
  const unlimited = dailyLimitMin <= 0
  const percent = unlimited ? 0 : Math.min(100, Math.max(0, (usedMinutes / dailyLimitMin) * 100))
  const nearlyDone = !unlimited && remainingMinutes <= 5

  return (
    <div className="w-full">
      <div className="mb-1 flex items-center justify-between text-sm">
        <span className="text-ink-soft">
          {unlimited ? '今日不限时' : `今日还剩 ${Math.max(0, remainingMinutes)} 分钟`}
        </span>
        {!unlimited && (
          <span className="text-ink-soft">
            {usedMinutes}/{dailyLimitMin} 分钟
          </span>
        )}
      </div>
      {!unlimited && (
        <div
          className="h-2 w-full overflow-hidden rounded-full bg-line"
          role="progressbar"
          aria-valuenow={Math.round(percent)}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-label="今日学习时长"
        >
          <div
            className="h-full rounded-full"
            style={{
              width: `${percent}%`,
              backgroundColor: nearlyDone ? 'var(--c-danger)' : 'var(--c-brand)',
            }}
          />
        </div>
      )}
    </div>
  )
}
