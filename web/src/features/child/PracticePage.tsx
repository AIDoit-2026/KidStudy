/**
 * 练习页（§六 /child/math/practice 等的通用实现）。
 *
 * 三件事值得说明：
 *  1. 答案永远不在前端：题目下发时不含 answer_key，作答后后端才回传 correct_answer / explain。
 *  2. 大屏用方向键答题：选中态由 activeIndex 驱动（不是 :hover），触屏与大屏都能看见。
 *  3. 描红 / 跟读这类主观题后端不判分（is_correct 为 null），由家长在结算时确认。
 */
import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'

import { readableError } from '../../api/client'
import { answer, finish, getSession, startSession, today, type AnswerBody } from '../../api/endpoints/practice'
import type { AnswerResult, ItemView, Question, SessionSummary } from '../../api/types'
import { Button } from '../../components/Button'
import { useKeyboardNav } from '../../hooks/useKeyboardNav'
import { useChildStore } from '../../stores/childStore'
import { SUBJECT_LABEL } from './HomePage'

/** 主观题：不判分，孩子做完点一下即可 */
const SUBJECTIVE_TYPES = new Set(['trace', 'say'])

type QuestionKind = 'options' | 'match' | 'order' | 'text' | 'subjective'

function kindOf(question: Question): QuestionKind {
  if (SUBJECTIVE_TYPES.has(question.type)) return 'subjective'
  if ((question.options?.length ?? 0) > 0) return 'options'
  if ((question.match_left?.length ?? 0) > 0 && (question.match_right?.length ?? 0) > 0) return 'match'
  if ((question.order_items?.length ?? 0) > 0) return 'order'
  return 'text'
}

export function PracticePage() {
  const { subject = 'math' } = useParams()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const childId = useChildStore((s) => s.currentChildId) ?? ''
  const sessionId = searchParams.get('session') ?? ''

  // 直接进页面（没有 session）时现开一个
  const createMutation = useMutation({
    mutationFn: () => startSession({ child_id: childId, subject, device_type: 'web' }),
    onSuccess: (session) => setSearchParams({ session: session.id }, { replace: true }),
  })

  useEffect(() => {
    if (!sessionId && childId) createMutation.mutate()
    // createMutation 的引用是稳定的，不放进依赖以免反复开会话
  }, [sessionId, childId])

  const sessionQuery = useQuery({
    queryKey: ['session', sessionId],
    queryFn: () => getSession(sessionId, childId),
    enabled: sessionId !== '' && childId !== '',
  })

  const planQuery = useQuery({
    queryKey: ['today', childId],
    queryFn: () => today(childId),
    enabled: childId !== '',
  })

  const [result, setResult] = useState<AnswerResult | null>(null)
  const [summary, setSummary] = useState<SessionSummary | null>(null)

  const session = sessionQuery.data
  const items: ItemView[] = useMemo(() => session?.items ?? [], [session])
  const currentIndex = items.findIndex((item) => item.state === 'pending')
  const current = currentIndex >= 0 ? items[currentIndex] : null

  // 换题时清掉上一题的作答状态
  useEffect(() => {
    setResult(null)
  }, [current?.id])

  const answerMutation = useMutation({
    mutationFn: (body: AnswerBody) => answer(sessionId, body),
    onSuccess: (res) => {
      setResult(res)
      void sessionQuery.refetch()
    },
  })

  const finishMutation = useMutation({
    mutationFn: () => finish(sessionId, childId),
    onSuccess: (res) => {
      setSummary(res)
      void planQuery.refetch()
    },
  })

  const submit = (payload: string | string[]) => {
    if (!current) return
    answerMutation.mutate({
      child_id: childId,
      item_id: current.id,
      answer: payload,
      elapsed_ms: 0,
    })
  }

  const goNext = () => {
    setResult(null)
    void sessionQuery.refetch()
  }

  const kind = current ? kindOf(current.question) : 'text'
  const optionCount = current?.question.options?.length ?? 0

  // 大屏方向键答题：只有选项题需要，且作答后停用（防误触改答案）
  const { activeIndex, setActiveIndex } = useKeyboardNav({
    count: optionCount,
    enabled: kind === 'options' && result === null && !answerMutation.isPending,
    columns: 1,
    onConfirm: (index) => {
      const option = current?.question.options?.[index]
      if (option) submit([option.id])
    },
    onCancel: () => navigate('/child/home'),
  })

  // ---------------- 渲染分支 ----------------

  if (!sessionId || createMutation.isPending) {
    return <p className="text-center text-ink-soft">正在准备题目…</p>
  }

  if (createMutation.isError) {
    return (
      <div className="card text-center">
        <p className="mb-3">{readableError(createMutation.error)}</p>
        <Button onClick={() => navigate('/child/home')}>回到今日任务</Button>
      </div>
    )
  }

  if (sessionQuery.isLoading) {
    return <p className="text-center text-ink-soft">正在加载题目…</p>
  }

  if (sessionQuery.isError) {
    return (
      <div className="card text-center">
        <p className="mb-3">{readableError(sessionQuery.error)}</p>
        <Button onClick={() => void sessionQuery.refetch()}>重试</Button>
      </div>
    )
  }

  // 结算页
  if (summary) {
    return (
      <div className="card mx-auto max-w-lg text-center">
        <h1 className="mb-2 text-2xl font-bold">这一节完成啦</h1>
        <p className="mb-4 text-ink-soft">{summary.message}</p>
        <div className="mb-4 grid grid-cols-3 gap-3">
          <div className="card">
            <div className="text-2xl font-bold text-brand">
              {summary.star_count > 0 ? '⭐'.repeat(summary.star_count) : '—'}
            </div>
            <div className="text-xs text-ink-soft">星级</div>
          </div>
          <div className="card">
            <div className="text-2xl font-bold text-brand">{Math.round(summary.accuracy * 100)}%</div>
            <div className="text-xs text-ink-soft">正确率</div>
          </div>
          <div className="card">
            <div className="text-2xl font-bold text-brand">{summary.answered_count}</div>
            <div className="text-xs text-ink-soft">作答题数</div>
          </div>
        </div>
        <Button variant="primary" onClick={() => navigate('/child/home')}>
          回到今日任务
        </Button>
      </div>
    )
  }

  // 全部答完 → 引导学生结算
  if (!current) {
    return (
      <div className="card mx-auto max-w-lg text-center">
        <h1 className="mb-2 text-xl font-bold">题目都做完啦</h1>
        <p className="mb-4 text-ink-soft">点下面的按钮看看这次的表现。</p>
        <Button
          variant="primary"
          disabled={finishMutation.isPending}
          onClick={() => finishMutation.mutate()}
        >
          {finishMutation.isPending ? '正在结算…' : '看看成绩'}
        </Button>
        {finishMutation.isError && (
          <p className="mt-3 text-sm text-danger">{readableError(finishMutation.error)}</p>
        )}
      </div>
    )
  }

  const progress = `${currentIndex + 1} / ${items.length}`

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-4">
      <div className="flex items-center justify-between">
        <span className="text-sm text-ink-soft">
          {SUBJECT_LABEL[current.subject_code] ?? current.subject_code}
        </span>
        <span className="text-sm text-ink-soft">{progress}</span>
      </div>

      <section className="card">
        {current.question.prompt_sub && (
          <p className="mb-1 text-sm text-ink-soft">{current.question.prompt_sub}</p>
        )}
        <p className="text-2xl font-semibold leading-relaxed md:text-3xl">{current.question.prompt}</p>
        {current.question.audio_text && (
          <p className="mt-2 text-sm text-ink-soft">朗读：{current.question.audio_text}</p>
        )}
      </section>

      {kind === 'options' && (
        <OptionList
          question={current.question}
          activeIndex={activeIndex}
          disabled={result !== null || answerMutation.isPending}
          onHover={setActiveIndex}
          onPick={(optionId) => submit([optionId])}
        />
      )}

      {kind === 'match' && (
        <MatchAnswer
          question={current.question}
          disabled={result !== null || answerMutation.isPending}
          onSubmit={(pairs) => submit(pairs)}
        />
      )}

      {kind === 'order' && (
        <OrderAnswer
          items={current.question.order_items ?? []}
          disabled={result !== null || answerMutation.isPending}
          onSubmit={(ordered) => submit(ordered)}
        />
      )}

      {kind === 'text' && (
        <TextAnswer
          disabled={result !== null || answerMutation.isPending}
          onSubmit={(value) => submit(value)}
        />
      )}

      {kind === 'subjective' && (
        <div className="card text-center">
          <p className="mb-3 text-sm text-ink-soft">
            这一题由家长在结算时确认，写完点下面的按钮就行。
          </p>
          <Button
            variant="primary"
            disabled={result !== null || answerMutation.isPending}
            onClick={() => submit([])}
          >
            做完了
          </Button>
        </div>
      )}

      {answerMutation.isError && (
        <p className="text-sm text-danger">{readableError(answerMutation.error)}</p>
      )}

      {result && (
        <section className="card" role="status" aria-live="polite">
          <p className="mb-1 text-lg font-semibold">
            {result.is_correct === null
              ? '已记录，等家长确认'
              : result.is_correct
                ? '答对啦 🎉'
                : '再想想也没关系'}
          </p>
          {result.correct_text && (
            <p className="text-sm text-ink-soft">正确答案：{result.correct_text}</p>
          )}
          {result.explain && <p className="mt-1 text-sm text-ink-soft">{result.explain}</p>}
          <Button variant="primary" className="mt-3" onClick={goNext}>
            下一题
          </Button>
        </section>
      )}

      {!result && (
        <div className="text-center">
          <Button variant="ghost" onClick={() => navigate('/child/home')}>
            先不做了
          </Button>
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------- 各题型作答组件

function OptionList({
  question,
  activeIndex,
  disabled,
  onHover,
  onPick,
}: {
  question: Question
  activeIndex: number
  disabled: boolean
  onHover: (index: number) => void
  onPick: (optionId: string) => void
}) {
  return (
    <ul className="flex flex-col gap-2">
      {(question.options ?? []).map((option, index) => (
        <li key={option.id}>
          <button
            type="button"
            className={`card focusable flex w-full items-center gap-3 text-left ${
              index === activeIndex ? 'is-active' : ''
            }`}
            disabled={disabled}
            onMouseEnter={() => onHover(index)}
            onFocus={() => onHover(index)}
            onClick={() => onPick(option.id)}
          >
            <span className="text-sm text-ink-soft">{index + 1}</span>
            {option.image && (
              <img src={option.image} alt="" className="h-10 w-10 rounded-lg object-cover" />
            )}
            <span className="text-lg">{option.label}</span>
          </button>
        </li>
      ))}
    </ul>
  )
}

function MatchAnswer({
  question,
  disabled,
  onSubmit,
}: {
  question: Question
  disabled: boolean
  onSubmit: (pairs: string[]) => void
}) {
  const left = question.match_left ?? []
  const right = question.match_right ?? []
  const [choices, setChoices] = useState<Record<string, string>>({})

  const ready = left.every((item) => choices[item])

  return (
    <div className="card flex flex-col gap-3">
      {left.map((item) => (
        <div key={item} className="flex items-center gap-3">
          <span className="w-1/3 truncate font-semibold">{item}</span>
          <select
            className="flex-1 rounded-xl border border-line bg-surface px-3 py-2"
            value={choices[item] ?? ''}
            disabled={disabled}
            onChange={(event) => setChoices((prev) => ({ ...prev, [item]: event.target.value }))}
          >
            <option value="">请选择</option>
            {right.map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </select>
        </div>
      ))}
      <Button
        variant="primary"
        disabled={disabled || !ready}
        onClick={() => onSubmit(left.map((item) => `${item}|${choices[item]}`))}
      >
        提交
      </Button>
    </div>
  )
}

function OrderAnswer({
  items,
  disabled,
  onSubmit,
}: {
  items: string[]
  disabled: boolean
  onSubmit: (ordered: string[]) => void
}) {
  const [picked, setPicked] = useState<string[]>([])
  const remaining = items.filter((item) => !picked.includes(item))

  return (
    <div className="card flex flex-col gap-3">
      <p className="text-sm text-ink-soft">按正确顺序依次点击：</p>
      <div className="flex flex-wrap gap-2">
        {remaining.map((item) => (
          <button
            key={item}
            type="button"
            className="btn"
            disabled={disabled}
            onClick={() => setPicked((prev) => [...prev, item])}
          >
            {item}
          </button>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-2 rounded-xl border border-line p-2">
        {picked.length === 0 ? (
          <span className="text-sm text-ink-soft">还没选</span>
        ) : (
          picked.map((item, index) => (
            <span key={item} className="rounded-full border border-line px-3 py-1 text-sm">
              {index + 1}. {item}
            </span>
          ))
        )}
      </div>
      <div className="flex gap-2">
        <Button disabled={disabled || picked.length === 0} onClick={() => setPicked([])}>
          重来
        </Button>
        <Button
          variant="primary"
          disabled={disabled || remaining.length > 0}
          onClick={() => onSubmit(picked)}
        >
          提交
        </Button>
      </div>
    </div>
  )
}

function TextAnswer({ disabled, onSubmit }: { disabled: boolean; onSubmit: (value: string) => void }) {
  const [value, setValue] = useState('')

  return (
    <div className="card flex gap-2">
      <input
        className="flex-1 rounded-xl border border-line bg-surface px-4 py-3 text-lg text-ink"
        value={value}
        disabled={disabled}
        inputMode="text"
        onChange={(event) => setValue(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && value.trim()) onSubmit(value.trim())
        }}
        aria-label="作答"
      />
      <Button variant="primary" disabled={disabled || !value.trim()} onClick={() => onSubmit(value.trim())}>
        提交
      </Button>
    </div>
  )
}
