/**
 * 打印中心（§六 /parent/print）—— 把 M5 后端那套「模板 → 作业 → 预览/PDF → 纸质补录」跑通。
 *
 * 后端的契约值得在这里写一遍，因为前端整页都是围着它设计的：
 *  - 模板自带 `params` 参数 schema（name/label/type/default/min/max/options/help），
 *    所以表单是**照着后端描述动态渲染**的，加模板不用改前端；
 *  - `POST /print/jobs` 只建作业并返回快照，`POST /print/jobs/{id}/pdf` 仅置 queued 返回 202，
 *    真正的渲染由 `cmd/worker -print` 消费 —— 所以前端必须**轮询 status**，而不是等一次响应；
 *  - 预览 HTML 与 PDF 同源，所以「预览」直接跳到独立的 /print/:jobId；
 *  - `mark-done` 把纸上的作答写回掌握度，幂等（重复提交返回 already_done）。
 */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { readableError } from '../../api/client'
import { listChildren } from '../../api/endpoints/children'
import {
  createJob,
  downloadPdf,
  getJob,
  listJobs,
  listTemplates,
  markDone,
  queuePdf,
  type CreateJobBody,
} from '../../api/endpoints/print'
import type { PrintJob, PrintParamSpec, PrintTemplate } from '../../api/types'
import { Button } from '../../components/Button'
import { useChildStore } from '../../stores/childStore'

const STATUS_LABEL: Record<string, string> = {
  created: '已创建',
  queued: '排队中',
  rendering: '渲染中',
  ready: '已就绪',
  failed: '渲染失败',
}

/** 渲染未完成时前端要轮询；这两个状态之外的都不必再查。 */
const IN_FLIGHT = new Set(['created', 'queued', 'rendering'])

export function PrintCenterPage() {
  const queryClient = useQueryClient()
  const currentChildId = useChildStore((s) => s.currentChildId) ?? ''

  const [templateCode, setTemplateCode] = useState('')
  const [childId, setChildId] = useState(currentChildId)
  const [params, setParams] = useState<Record<string, unknown>>({})
  const [activeJobId, setActiveJobId] = useState('')
  const [notice, setNotice] = useState('')

  const templatesQuery = useQuery({ queryKey: ['print-templates'], queryFn: listTemplates })
  const childrenQuery = useQuery({ queryKey: ['children'], queryFn: listChildren })
  const jobsQuery = useQuery({
    queryKey: ['print-jobs', childId],
    queryFn: () => listJobs(childId || undefined),
  })

  const templates = templatesQuery.data?.templates ?? []
  const children = childrenQuery.data ?? []
  // listJobs 走的是分页封装，外层是 { data, page }，别把包装当数组用。
  const jobs = jobsQuery.data?.data ?? []
  const selected = useMemo(
    () => templates.find((t) => t.code === templateCode) ?? null,
    [templates, templateCode],
  )

  // 选中模板 → 用后端给的 default 铺一份初始参数，用户只需改想改的
  const pickTemplate = (tpl: PrintTemplate) => {
    setTemplateCode(tpl.code)
    setActiveJobId('')
    setNotice('')
    const initial: Record<string, unknown> = {}
    tpl.params.forEach((spec) => {
      initial[spec.name] = spec.default
    })
    setParams(initial)
  }

  const createMutation = useMutation({
    mutationFn: (body: CreateJobBody) => createJob(body),
    onSuccess: (job) => {
      setActiveJobId(job.id)
      setNotice('作业已创建，正在生成预览…')
      void queryClient.invalidateQueries({ queryKey: ['print-jobs'] })
    },
  })

  const queueMutation = useMutation({
    mutationFn: (id: string) => queuePdf(id),
    onSuccess: () => {
      setNotice('已加入渲染队列，稍等片刻即可下载。')
      void queryClient.invalidateQueries({ queryKey: ['print-job', activeJobId] })
    },
  })

  const doneMutation = useMutation({
    mutationFn: (id: string) => markDone(id),
    onSuccess: (result) => {
      setNotice(
        result.already_done
          ? '这份作业之前已经补录过了。'
          : `补录完成：${result.correct_count}/${result.item_count} 题正确，新增掌握 ${result.mastered} 个知识点。`,
      )
      void queryClient.invalidateQueries({ queryKey: ['print-jobs'] })
    },
  })

  const [downloading, setDownloading] = useState(false)
  const handleDownload = async (id: string) => {
    setDownloading(true)
    try {
      await downloadPdf(id)
    } catch (err) {
      setNotice(readableError(err))
    } finally {
      setDownloading(false)
    }
  }

  // 单个作业的状态轮询：只在渲染中的作业上开着，就绪/失败立刻停。
  const activeJobQuery = useQuery({
    queryKey: ['print-job', activeJobId],
    queryFn: () => getJob(activeJobId),
    enabled: activeJobId !== '',
    refetchInterval: (query) => {
      const status = query.state.data?.status ?? ''
      return IN_FLIGHT.has(status) ? 1_500 : false
    },
  })

  const canCreate =
    selected !== null && (!selected.needs_child || childId !== '') && !createMutation.isPending

  const handleCreate = () => {
    if (!selected) return
    setNotice('')
    createMutation.mutate({
      template_code: selected.code,
      child_id: childId || undefined,
      params,
    })
  }

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">打印中心</h1>
        <Link className="text-sm text-brand underline" to="/parent/settings">
          家长设置
        </Link>
      </div>

      {notice && (
        <p className="rounded-xl border border-line bg-surface px-4 py-2 text-sm" role="status">
          {notice}
        </p>
      )}

      {/* ---------------------------------------------------- 选模板 */}
      <section className="card">
        <h2 className="mb-3 font-semibold">1. 选模板</h2>
        {templatesQuery.isLoading ? (
          <p className="text-ink-soft">正在加载模板…</p>
        ) : templatesQuery.isError ? (
          <div>
            <p className="mb-3">{readableError(templatesQuery.error)}</p>
            <Button onClick={() => void templatesQuery.refetch()}>重试</Button>
          </div>
        ) : templates.length === 0 ? (
          <p className="text-ink-soft">暂时没有可用模板。</p>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {templates.map((tpl) => (
              <button
                key={tpl.code}
                type="button"
                className={`card text-left hoverable ${templateCode === tpl.code ? 'is-active' : ''}`}
                aria-pressed={templateCode === tpl.code}
                onClick={() => pickTemplate(tpl)}
              >
                <div className="flex items-start justify-between gap-2">
                  <span className="font-semibold">{tpl.name}</span>
                  <span className="shrink-0 rounded-full border border-line px-2 py-0.5 text-xs text-ink-soft">
                    {tpl.category}
                  </span>
                </div>
                <p className="mt-1 text-xs text-ink-soft">{tpl.description}</p>
                <p className="mt-2 text-xs text-ink-soft">
                  {tpl.paper_size}
                  {tpl.double_sided ? ' · 双面' : ''}
                  {tpl.answerable ? ' · 可补录' : ''}
                </p>
              </button>
            ))}
          </div>
        )}
      </section>

      {/* ---------------------------------------------------- 填参数 */}
      {selected && (
        <section className="card">
          <h2 className="mb-3 font-semibold">2. 参数</h2>

          {selected.needs_child && (
            <label className="mb-3 flex items-center gap-3">
              <span className="w-40 shrink-0 text-sm text-ink-soft">孩子</span>
              <select
                className="flex-1 rounded-xl border border-line bg-surface px-3 py-2 text-ink"
                value={childId}
                onChange={(event) => setChildId(event.target.value)}
              >
                <option value="">请选择…</option>
                {children.map((child) => (
                  <option key={child.id} value={child.id}>
                    {child.nickname}
                  </option>
                ))}
              </select>
            </label>
          )}

          {selected.params.length === 0 ? (
            <p className="text-sm text-ink-soft">这个模板没有可调参数，直接生成就行。</p>
          ) : (
            selected.params.map((spec) => (
              <ParamField
                key={spec.name}
                spec={spec}
                value={params[spec.name]}
                onChange={(v) => setParams((prev) => ({ ...prev, [spec.name]: v }))}
              />
            ))
          )}

          {createMutation.isError && (
            <p className="mb-2 text-sm text-danger">{readableError(createMutation.error)}</p>
          )}

          <div className="mt-2 flex gap-2">
            <Button variant="primary" disabled={!canCreate} onClick={handleCreate}>
              {createMutation.isPending ? '生成中…' : '生成预览'}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setTemplateCode('')
                setActiveJobId('')
                setNotice('')
              }}
            >
              取消
            </Button>
          </div>

          {selected.needs_child && childId === '' && (
            <p className="mt-2 text-xs text-ink-soft">这个模板需要先选定孩子。</p>
          )}
        </section>
      )}

      {/* ---------------------------------------------------- 当前作业 */}
      {activeJobId && (
        <JobPanel
          job={activeJobQuery.data}
          loading={activeJobQuery.isLoading}
          error={activeJobQuery.isError ? readableError(activeJobQuery.error) : ''}
          downloading={downloading}
          onQueue={() => queueMutation.mutate(activeJobId)}
          queuePending={queueMutation.isPending}
          onDownload={() => void handleDownload(activeJobId)}
          onMarkDone={() => doneMutation.mutate(activeJobId)}
          markDonePending={doneMutation.isPending}
        />
      )}

      {/* ---------------------------------------------------- 历史作业 */}
      <section className="card">
        <h2 className="mb-3 font-semibold">最近打印</h2>
        {jobsQuery.isLoading ? (
          <p className="text-ink-soft">正在加载…</p>
        ) : jobsQuery.isError ? (
          <div>
            <p className="mb-3">{readableError(jobsQuery.error)}</p>
            <Button onClick={() => void jobsQuery.refetch()}>重试</Button>
          </div>
        ) : jobs.length === 0 ? (
          <p className="text-sm text-ink-soft">还没有打印记录。</p>
        ) : (
          <ul className="flex flex-col divide-y divide-line">
            {jobs.map((job) => (
              <li key={job.id} className="flex flex-wrap items-center gap-2 py-2">
                <span className="min-w-0 flex-1 truncate">
                  <span className="font-medium">{job.title || job.template_name}</span>
                  <span className="ml-2 text-xs text-ink-soft">
                    {STATUS_LABEL[job.status] ?? job.status}
                    {job.page_count > 0 ? ` · ${job.page_count} 页` : ''}
                    {job.marked_done ? ' · 已补录' : ''}
                  </span>
                </span>
                <Link className="text-sm text-brand underline" to={`/print/${job.id}`}>
                  预览
                </Link>
                <Button
                  className="!px-3 !py-1.5 text-sm"
                  disabled={!job.pdf_ready || downloading}
                  onClick={() => void handleDownload(job.id)}
                >
                  下载
                </Button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

// ---------------------------------------------------------------- 子组件

/** 单个作业的状态面板：轮询到这里，把「排队 → 就绪 → 下载/补录」串起来。 */
function JobPanel({
  job,
  loading,
  error,
  downloading,
  onQueue,
  queuePending,
  onDownload,
  onMarkDone,
  markDonePending,
}: {
  job: PrintJob | undefined
  loading: boolean
  error: string
  downloading: boolean
  onQueue: () => void
  queuePending: boolean
  onDownload: () => void
  onMarkDone: () => void
  markDonePending: boolean
}) {
  if (loading && !job) return <section className="card text-ink-soft">正在读取作业状态…</section>
  if (error && !job) return <section className="card text-danger">{error}</section>
  if (!job) return null

  const inFlight = IN_FLIGHT.has(job.status)

  return (
    <section className="card">
      <h2 className="mb-2 font-semibold">3. 生成与打印</h2>
      <div className="mb-3 flex flex-wrap items-center gap-2 text-sm">
        <span className="rounded-full border border-line px-3 py-1">
          {STATUS_LABEL[job.status] ?? job.status}
        </span>
        <span className="text-ink-soft">
          {job.page_count > 0
            ? `共 ${job.page_count} 页`
            : job.planned_pages > 0
              ? `预计 ${job.planned_pages} 页`
              : ''}
        </span>
        {inFlight && <span className="text-ink-soft">渲染中，页面会自动刷新…</span>}
      </div>

      {job.error_message && <p className="mb-2 text-sm text-danger">{job.error_message}</p>}

      <div className="flex flex-wrap gap-2">
        <Link className="btn" to={`/print/${job.id}`}>
          打开预览（Ctrl+P 打印）
        </Link>

        {!job.pdf_ready && (
          <Button variant="primary" disabled={queuePending || job.status === 'rendering'} onClick={onQueue}>
            {queuePending || job.status === 'rendering' ? '排队中…' : '渲染 PDF'}
          </Button>
        )}

        <Button
          disabled={!job.pdf_ready || downloading}
          title={job.pdf_ready ? undefined : 'PDF 还没渲染好'}
          onClick={onDownload}
        >
          {downloading ? '下载中…' : '下载 PDF'}
        </Button>

        {job.pdf_ready && (
          <Button
            disabled={job.marked_done || markDonePending}
            title={job.marked_done ? '这份作业已经补录过了' : undefined}
            onClick={onMarkDone}
          >
            {job.marked_done ? '已补录' : markDonePending ? '补录中…' : '纸上做完了，补录'}
          </Button>
        )}
      </div>

      <p className="mt-3 text-xs text-ink-soft">
        建议流程：先打开预览在一张纸上试打，确认排版没问题再点「下载 PDF」批量打印。
      </p>
    </section>
  )
}

/** 按后端给的参数 schema 渲染一个输入控件（加模板不用动这里）。 */
function ParamField({
  spec,
  value,
  onChange,
}: {
  spec: PrintParamSpec
  value: unknown
  onChange: (v: unknown) => void
}) {
  const label = (
    <span className="w-40 shrink-0">
      <span className="block text-sm">{spec.label || spec.name}</span>
      {spec.help && <span className="block text-xs text-ink-soft">{spec.help}</span>}
    </span>
  )

  if (spec.type === 'bool') {
    return (
      <label className="mb-3 flex items-center gap-3">
        <input
          type="checkbox"
          className="h-5 w-5"
          checked={Boolean(value)}
          onChange={(event) => onChange(event.target.checked)}
        />
        <span className="text-sm">{spec.label || spec.name}</span>
        {spec.help && <span className="text-xs text-ink-soft">{spec.help}</span>}
      </label>
    )
  }

  if (spec.type === 'enum') {
    return (
      <label className="mb-3 flex items-center gap-3">
        {label}
        <select
          className="flex-1 rounded-xl border border-line bg-surface px-3 py-2 text-ink"
          value={String(value ?? '')}
          onChange={(event) => onChange(event.target.value)}
        >
          {(spec.options ?? []).map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
      </label>
    )
  }

  if (spec.type === 'int') {
    return (
      <label className="mb-3 flex items-center gap-3">
        {label}
        <input
          type="number"
          className="w-28 rounded-xl border border-line bg-surface px-3 py-2 text-center text-ink"
          min={spec.min}
          max={spec.max}
          value={Number(value ?? 0)}
          onChange={(event) => {
            let next = Number(event.target.value) || 0
            if (typeof spec.min === 'number') next = Math.max(spec.min, next)
            if (typeof spec.max === 'number') next = Math.min(spec.max, next)
            onChange(next)
          }}
        />
      </label>
    )
  }

  return (
    <label className="mb-3 flex items-center gap-3">
      {label}
      <input
        type="text"
        className="flex-1 rounded-xl border border-line bg-surface px-3 py-2 text-ink"
        value={String(value ?? '')}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  )
}
