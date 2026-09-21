/**
 * 今日任务（§六 /child/home）：复习 / 新学 / 专项 三段 + 剩余时长条（在外壳顶部）。
 */
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'

import { readableError } from '../../api/client'
import { startSession, today } from '../../api/endpoints/practice'
import type { PlanItem } from '../../api/types'
import { Button } from '../../components/Button'
import { useChildStore } from '../../stores/childStore'

export const SUBJECT_LABEL: Record<string, string> = {
  chinese: '语文',
  english: '英语',
  math: '数学',
}

type Buckets = {
  wrong: PlanItem[]
  review: PlanItem[]
  assignment: PlanItem[]
  fresh: PlanItem[]
}

/** 按编排原因粗分三段：reason 是后端给的可读文案，这里按关键词归类。 */
function groupItems(items: PlanItem[]): Buckets {
  const buckets: Buckets = { wrong: [], review: [], assignment: [], fresh: [] }
  for (const item of items) {
    const reason = item.reason ?? ''
    if (reason.includes('错')) buckets.wrong.push(item)
    else if (reason.includes('复习')) buckets.review.push(item)
    else if (reason.includes('专项')) buckets.assignment.push(item)
    else buckets.fresh.push(item)
  }
  return buckets
}

function ItemChips({ items, title, hint }: { items: PlanItem[]; title: string; hint?: string }) {
  if (items.length === 0) return null
  return (
    <section className="card">
      <div className="mb-2 flex items-baseline justify-between">
        <h2 className="font-semibold">{title}</h2>
        <span className="text-xs text-ink-soft">{items.length} 项</span>
      </div>
      {hint && <p className="mb-2 text-xs text-ink-soft">{hint}</p>}
      <ul className="flex flex-wrap gap-2">
        {items.map((item) => (
          <li
            key={`${item.subject_code}-${item.kp_id}`}
            className="rounded-full border border-line bg-raised px-3 py-1 text-sm"
          >
            {item.name || item.code}
            <span className="ml-1 text-xs text-ink-soft">
              {SUBJECT_LABEL[item.subject_code] ?? item.subject_code}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}

export function HomePage() {
  const navigate = useNavigate()
  const childId = useChildStore((s) => s.currentChildId) ?? ''

  const planQuery = useQuery({
    queryKey: ['today', childId],
    queryFn: () => today(childId),
    enabled: childId !== '',
  })

  const startMutation = useMutation({
    mutationFn: (subject: string) =>
      startSession({ child_id: childId, subject, device_type: 'web' }),
    onSuccess: (session, subject) => {
      navigate(`/child/practice/${subject}?session=${session.id}`)
    },
  })

  const plan = planQuery.data
  const buckets = groupItems(plan?.items ?? [])
  const subjects = plan?.subjects ?? []

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-xl font-bold">今天的任务</h1>
        {plan && (
          <span className="text-sm text-ink-soft">
            待复习 {plan.due_count} 项 · 已用 {plan.used_minutes} 分钟
          </span>
        )}
      </div>

      {plan?.message && (
        <p className="rounded-xl border border-line bg-surface px-4 py-3 text-sm">{plan.message}</p>
      )}

      {plan?.backlog && (
        <p className="rounded-xl border border-line bg-surface px-4 py-3 text-sm">
          复习有点积压了，今天先把错题过一遍，新内容少学一点也没关系。
        </p>
      )}

      {planQuery.isLoading && <p className="text-ink-soft">正在准备…</p>}

      {planQuery.isError && (
        <div className="card">
          <p className="mb-3">{readableError(planQuery.error)}</p>
          <Button onClick={() => void planQuery.refetch()}>重试</Button>
        </div>
      )}

      {plan && plan.items.length === 0 && (
        <div className="card text-center">
          <p className="mb-1 text-lg font-semibold">今天的任务都完成啦 🎉</p>
          <p className="text-sm text-ink-soft">休息一下，明天继续。</p>
        </div>
      )}

      <ItemChips items={buckets.wrong} title="先攻错题" hint="错题趁热打铁，效果最好" />
      <ItemChips items={buckets.review} title="复习" />
      <ItemChips items={buckets.fresh} title="新学" />
      <ItemChips items={buckets.assignment} title="专项练习" />

      {subjects.length > 0 && (
        <section className="card">
          <h2 className="mb-3 font-semibold">开始学习</h2>
          <div className="flex flex-wrap gap-3">
            {subjects.map((subject) => (
              <Button
                key={subject}
                variant="primary"
                className="min-w-32"
                disabled={startMutation.isPending}
                onClick={() => startMutation.mutate(subject)}
              >
                {SUBJECT_LABEL[subject] ?? subject}
              </Button>
            ))}
          </div>
          {startMutation.isError && (
            <p className="mt-3 text-sm text-danger">{readableError(startMutation.error)}</p>
          )}
        </section>
      )}
    </div>
  )
}
