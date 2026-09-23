/**
 * 家长学习报告（§4.7 / §4.9）—— M4 后端早就跑通了 overview/trend/suggestions，
 * 但前端一直没有入口；这里补一个「简版报表页」，让家长能看到并一键执行建议。
 *
 * 三条纪律照旧：
 *  - 结论与措辞全来自后端（后端 value，前端不再自己下判断，更不排名）；
 *  - 错误一律经 readableError 翻成可读文案；
 *  - PDF 导出是异步的（复用 M5 周报模板），所以是先建打印任务、轮询、再下载。
 */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { readableError } from '../../api/client'
import { listChildren } from '../../api/endpoints/children'
import { downloadPdf, getJob } from '../../api/endpoints/print'
import {
  applySuggestionAction,
  downloadCsv,
  exportPdf,
  getOverview,
  getSuggestions,
  getTrend,
} from '../../api/endpoints/report'
import type { ReportSuggestion, SuggestionActionRequest, TrendPoint } from '../../api/types'
import { Button } from '../../components/Button'
import { useChildStore } from '../../stores/childStore'

const TREND_DAYS = 30

export function ReportPage() {
  const queryClient = useQueryClient()
  const storeChildId = useChildStore((s) => s.currentChildId)

  const [picked, setPicked] = useState<string | null>(null)
  const childrenQuery = useQuery({ queryKey: ['children'], queryFn: listChildren })
  const children = childrenQuery.data ?? []

  // 默认落在「孩子端当前选中的那个」；没有就用第一个。用 picked 覆盖，不改全局选择。
  const defaultId = children.find((c) => c.id === storeChildId)?.id ?? children[0]?.id ?? ''
  const childId = picked ?? defaultId

  const overviewQuery = useQuery({
    queryKey: ['report-overview', childId],
    queryFn: () => getOverview(childId),
    enabled: childId !== '',
  })
  const trendQuery = useQuery({
    queryKey: ['report-trend', childId, TREND_DAYS],
    queryFn: () => getTrend(childId, TREND_DAYS),
    enabled: childId !== '',
  })
  const suggestionsQuery = useQuery({
    queryKey: ['report-suggestions', childId],
    queryFn: () => getSuggestions(childId),
    enabled: childId !== '',
  })

  const [notice, setNotice] = useState('')
  const actionMutation = useMutation({
    mutationFn: (body: SuggestionActionRequest) => applySuggestionAction(childId, body),
    onSuccess: (res) => {
      setNotice(res.detail)
      window.setTimeout(() => setNotice(''), 4_000)
      // 动作可能改动家长设置（复习日 / 每日量）或掌握度，相关的都刷一遍
      void queryClient.invalidateQueries({ queryKey: ['report-suggestions', childId] })
      void queryClient.invalidateQueries({ queryKey: ['report-overview', childId] })
      void queryClient.invalidateQueries({ queryKey: ['parent-settings'] })
    },
  })

  const [pdfBusy, setPdfBusy] = useState(false)
  const [pdfMsg, setPdfMsg] = useState('')
  const [csvBusy, setCsvBusy] = useState(false)

  async function handleExportPdf() {
    if (childId === '') return
    setPdfBusy(true)
    setPdfMsg('正在生成周报…')
    try {
      const { job_id } = await exportPdf(childId, TREND_DAYS)
      // 渲染是后端 Chromium 的活：轮询到 ready 再下载，最多等约 1 分钟
      for (let i = 0; i < 40; i += 1) {
        const job = await getJob(job_id)
        if (job.pdf_ready) {
          await downloadPdf(job_id)
          setPdfMsg('周报已开始下载')
          return
        }
        if (job.status === 'failed') {
          throw new Error(job.error_message || '周报生成失败，请稍后再试')
        }
        await new Promise((resolve) => window.setTimeout(resolve, 1_500))
      }
      setPdfMsg('生成时间较长，可稍后在打印中心下载')
    } catch (err) {
      setPdfMsg(readableError(err))
    } finally {
      setPdfBusy(false)
    }
  }

  async function handleExportCsv() {
    if (childId === '') return
    setCsvBusy(true)
    try {
      await downloadCsv(childId, TREND_DAYS)
    } catch (err) {
      setPdfMsg(readableError(err))
    } finally {
      setCsvBusy(false)
    }
  }

  if (childrenQuery.isLoading) return <p className="text-ink-soft">正在加载…</p>
  if (childrenQuery.isError) {
    return (
      <div className="card">
        <p className="mb-3">{readableError(childrenQuery.error)}</p>
        <Button onClick={() => void childrenQuery.refetch()}>重试</Button>
      </div>
    )
  }
  if (children.length === 0) {
    return (
      <div className="card">
        <h1 className="mb-2 text-xl font-bold">学习报告</h1>
        <p className="mb-3 text-ink-soft">还没有孩子档案，先去「家长设置」添加一个吧。</p>
        <Link className="text-brand underline" to="/parent/settings">
          去添加孩子
        </Link>
      </div>
    )
  }

  const overview = overviewQuery.data
  const loading = overviewQuery.isLoading && !overview

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-bold">学习报告</h1>
        <div className="flex gap-2">
          <Button disabled={csvBusy || childId === ''} onClick={() => void handleExportCsv()}>
            {csvBusy ? '导出中…' : '导出 CSV'}
          </Button>
          <Button
            variant="primary"
            disabled={pdfBusy || childId === ''}
            onClick={() => void handleExportPdf()}
          >
            {pdfBusy ? '生成中…' : '导出周报 PDF'}
          </Button>
        </div>
      </div>

      {/* 孩子选择：多孩时才出现，避免只有一个孩子时多一层点击 */}
      {children.length > 1 && (
        <div className="flex flex-wrap gap-2" role="tablist" aria-label="选择要查看的孩子">
          {children.map((child) => (
            <button
              key={child.id}
              type="button"
              role="tab"
              aria-selected={child.id === childId}
              className={`btn ${child.id === childId ? 'is-active' : ''}`}
              onClick={() => setPicked(child.id)}
            >
              {child.nickname}
            </button>
          ))}
        </div>
      )}

      {pdfMsg && (
        <p className="rounded-xl border border-line bg-surface px-4 py-2 text-sm" role="status">
          {pdfMsg}
        </p>
      )}
      {notice && (
        <p
          className="rounded-xl border border-line bg-surface px-4 py-2 text-sm"
          style={{ color: 'var(--c-success)' }}
          role="status"
        >
          {notice}
        </p>
      )}

      {loading ? (
        <p className="text-ink-soft">正在汇总学习数据…</p>
      ) : overviewQuery.isError && !overview ? (
        <div className="card">
          <p className="mb-3">{readableError(overviewQuery.error)}</p>
          <Button onClick={() => void overviewQuery.refetch()}>重试</Button>
        </div>
      ) : overview ? (
        <>
          <section className="card">
            <h2 className="mb-3 font-semibold">总览</h2>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Metric label="累计掌握" value={overview.mastered_total} />
              <Metric label="连续达标" value={`${overview.streak_days} 天`} />
              <Metric label="待复习" value={overview.due_total} />
              <Metric label="获得徽章" value={`${overview.badges_earned} / ${overview.badges_total}`} />
            </div>
            <p className="mt-3 text-xs text-ink-soft">
              累计练习 {overview.totals.question_count} 题 · 平均正确率{' '}
              {Math.round((overview.totals.avg_accuracy || 0) * 100)}% · 学习{' '}
              {overview.totals.active_days} 天
            </p>
          </section>

          <section className="card">
            <h2 className="mb-3 font-semibold">学科进度</h2>
            {overview.subjects.length === 0 ? (
              <p className="text-sm text-ink-soft">还没有学习记录。</p>
            ) : (
              <ul className="flex flex-col gap-3">
                {overview.subjects.map((s) => {
                  const ratio = s.planned_total > 0 ? s.mastered / s.planned_total : 0
                  return (
                    <li key={s.subject_code}>
                      <div className="mb-1 flex items-center justify-between text-sm">
                        <span>{s.subject_name}</span>
                        <span className="text-ink-soft">
                          {s.mastered} / {s.planned_total} · 待复习 {s.due}
                        </span>
                      </div>
                      <div className="h-2 overflow-hidden rounded-full bg-line" role="presentation">
                        <div
                          className="h-full rounded-full"
                          style={{
                            width: `${Math.round(ratio * 100)}%`,
                            background: 'var(--c-brand)',
                          }}
                        />
                      </div>
                    </li>
                  )
                })}
              </ul>
            )}
          </section>

          <section className="card">
            <h2 className="mb-3 font-semibold">
              近 {TREND_DAYS} 天趋势
              {trendQuery.data?.note ? (
                <span className="ml-2 text-xs font-normal text-ink-soft">{trendQuery.data.note}</span>
              ) : null}
            </h2>
            {trendQuery.isError ? (
              <p className="text-sm text-danger">{readableError(trendQuery.error)}</p>
            ) : (trendQuery.data?.points.length ?? 0) === 0 ? (
              <p className="text-sm text-ink-soft">这段时间还没有学习记录。</p>
            ) : (
              <TrendChart points={trendQuery.data?.points ?? []} />
            )}
          </section>

          <section className="card">
            <h2 className="mb-1 font-semibold">下一步建议</h2>
            <p className="mb-3 text-xs text-ink-soft">建议都可关闭，只作参考，不做排名。</p>
            {suggestionsQuery.isLoading ? (
              <p className="text-sm text-ink-soft">正在生成建议…</p>
            ) : suggestionsQuery.isError ? (
              <p className="text-sm text-danger">{readableError(suggestionsQuery.error)}</p>
            ) : (suggestionsQuery.data?.suggestions.length ?? 0) === 0 ? (
              <p className="text-sm text-ink-soft">目前没有需要提醒的事项，保持节奏就好。</p>
            ) : (
              <ul className="flex flex-col gap-3">
                {(suggestionsQuery.data?.suggestions ?? []).map((sg) => (
                  <SuggestionCard
                    key={sg.code}
                    suggestion={sg}
                    busy={actionMutation.isPending}
                    onAction={(body) => actionMutation.mutate(body)}
                  />
                ))}
              </ul>
            )}
            {actionMutation.isError && (
              <p className="mt-3 text-sm text-danger">{readableError(actionMutation.error)}</p>
            )}
          </section>
        </>
      ) : null}
    </div>
  )
}

// ---------------------------------------------------------------- 子组件

function Metric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-xl border border-line bg-surface px-3 py-2">
      <div className="text-xs text-ink-soft">{label}</div>
      <div className="text-lg font-bold">{value}</div>
    </div>
  )
}

/** 极简柱状图：每天一根，高度按正确率，达标日高亮。不引第三方图表库。 */
function TrendChart({ points }: { points: TrendPoint[] }) {
  const recent = points.slice(-TREND_DAYS)
  return (
    <div className="flex items-end gap-1" style={{ height: 120 }} role="img" aria-label="每日正确率趋势">
      {recent.map((pt) => {
        const accuracy = Math.max(0, Math.min(1, pt.accuracy || 0))
        return (
          <div
            key={pt.date}
            className="flex-1 rounded-t"
            style={{
              height: `${Math.max(4, accuracy * 100)}%`,
              background: pt.passed ? 'var(--c-success)' : 'var(--c-brand)',
              opacity: pt.passed ? 1 : 0.55,
            }}
            title={`${pt.date}：正确率 ${Math.round(accuracy * 100)}%${pt.passed ? ' · 达标' : ''} · ${pt.question_count} 题`}
          />
        )
      })}
    </div>
  )
}

/** 一条建议 + 它的动作按钮。动作是否可点、按钮文案都由后端 actions 驱动。 */
function SuggestionCard({
  suggestion,
  busy,
  onAction,
}: {
  suggestion: ReportSuggestion
  busy: boolean
  onAction: (body: SuggestionActionRequest) => void
}) {
  const accent = suggestion.severity === 'notice' ? 'var(--c-danger)' : 'var(--c-brand)'
  return (
    <li className="rounded-xl border border-line bg-surface p-3">
      <div className="mb-1 flex items-center gap-2">
        <span
          aria-hidden="true"
          className="inline-block h-2 w-2 rounded-full"
          style={{ background: accent }}
        />
        <span className="font-medium">{suggestion.title}</span>
      </div>
      <p className="mb-2 text-sm text-ink-soft">{suggestion.detail}</p>
      {suggestion.actions.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {suggestion.actions.map((action) => (
            <Button
              key={action.type}
              className="!px-3 !py-1.5 text-sm"
              disabled={busy}
              onClick={() =>
                onAction({
                  type: action.type,
                  // 后端按 type 取用：专项/降档要 kp_id，均衡要 subject
                  kp_id: typeof suggestion.data?.kp_id === 'string' ? suggestion.data.kp_id : undefined,
                  subject: suggestion.subject || undefined,
                })
              }
            >
              {action.label}
            </Button>
          ))}
        </div>
      )}
    </li>
  )
}
