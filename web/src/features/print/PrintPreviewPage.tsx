/**
 * 打印预览（§六 /print/:jobId）—— 刻意做成**独立路由，不挂任何外壳**。
 *
 * 理由有两条，都很硬：
 *  1. 这一页的 HTML 与后端 worker 用 chromedp 渲染 PDF 的 HTML 是**同一份字符串**，
 *     家长在浏览器里 Ctrl+P 得到的结果与「下载 PDF」一致。多套一层导航/护眼计时，
 *     屏幕上的东西就和纸上不一样了，这个保证就废了。
 *  2. 家长在电脑前按打印键时不该被孩子的「该休息了」打断，所以护眼计时不在这里启用。
 *
 * 工具栏只在屏幕上出现，`@media print` 里隐藏（.print-hidden，见 styles/index.css），
 * 并且 Ctrl+P 被接管为「只打印 iframe 内的那一段」—— 否则浏览器会把工具栏也印上去。
 */
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'

import { readableError } from '../../api/client'
import { downloadPdf, getJob, previewHtml } from '../../api/endpoints/print'
import { Button } from '../../components/Button'
import { FullPageLoading } from '../../components/FullPageLoading'

export function PrintPreviewPage() {
  const { jobId = '' } = useParams()
  const navigate = useNavigate()
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const [downloading, setDownloading] = useState(false)
  const [downloadError, setDownloadError] = useState('')

  const previewQuery = useQuery({
    queryKey: ['print-preview', jobId],
    queryFn: () => previewHtml(jobId),
    enabled: jobId !== '',
    // 预览与作业是同一份快照；来回跳转时短期缓存一下，不必每次重拉。
    staleTime: 30_000,
  })

  const jobQuery = useQuery({
    queryKey: ['print-job', jobId],
    queryFn: () => getJob(jobId),
    enabled: jobId !== '',
  })

  const job = jobQuery.data

  // 文档标题跟着作业走：家长在打印对话框里能一眼认出来打的是哪一份
  useEffect(() => {
    if (!job) return
    const previous = document.title
    document.title = `${job.title || job.template_name} · 打印预览`
    return () => {
      document.title = previous
    }
  }, [job])

  /** 打印 iframe 里的内容，而不是外层页面（外层只剩一条工具栏）。 */
  const handlePrint = () => {
    const frame = iframeRef.current
    if (frame?.contentWindow) {
      frame.contentWindow.focus()
      frame.contentWindow.print()
      return
    }
    window.print()
  }

  // 把最新的 handlePrint 放进 ref，键盘监听只需挂一次，不随渲染重建。
  const printRef = useRef(handlePrint)
  printRef.current = handlePrint

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'p') {
        event.preventDefault()
        printRef.current()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  const handleDownload = async () => {
    setDownloadError('')
    setDownloading(true)
    try {
      await downloadPdf(jobId)
    } catch (err) {
      setDownloadError(readableError(err))
    } finally {
      setDownloading(false)
    }
  }

  if (previewQuery.isLoading) return <FullPageLoading label="正在生成预览…" />

  if (previewQuery.isError) {
    return (
      <div className="mx-auto mt-16 w-full max-w-md p-4">
        <div className="card text-center">
          <p className="mb-3">{readableError(previewQuery.error)}</p>
          <div className="flex justify-center gap-2">
            <Button onClick={() => void previewQuery.refetch()}>重试</Button>
            <Button variant="ghost" onClick={() => navigate(-1)}>
              返回
            </Button>
          </div>
        </div>
      </div>
    )
  }

  const pageLabel = job
    ? job.page_count > 0
      ? `约 ${job.page_count} 页`
      : job.planned_pages > 0
        ? `预计 ${job.planned_pages} 页`
        : ''
    : ''

  return (
    <div className="flex h-full flex-col bg-paper">
      {/* 工具栏：屏幕可见、打印隐藏 */}
      <div className="print-hidden flex flex-wrap items-center gap-2 border-b border-line bg-raised px-4 py-2">
        <span className="min-w-0 flex-1 truncate text-sm">
          {job?.title || job?.template_name || '打印预览'}
          {pageLabel && <span className="ml-2 text-xs text-ink-soft">{pageLabel}</span>}
          {job?.pdf_ready && <span className="ml-1 text-xs text-ink-soft">· PDF 已就绪</span>}
        </span>

        {downloadError && <span className="text-xs text-danger">{downloadError}</span>}

        <Button variant="primary" onClick={handlePrint}>
          打印（Ctrl+P）
        </Button>
        <Button
          disabled={downloading || (job ? !job.pdf_ready : false)}
          title={job && !job.pdf_ready ? 'PDF 还在后台渲染，请稍候…' : undefined}
          onClick={() => void handleDownload()}
        >
          {downloading ? '下载中…' : '下载 PDF'}
        </Button>
        <Button variant="ghost" onClick={() => navigate(-1)}>
          关闭
        </Button>
      </div>

      {/*
        srcdoc 直接塞后端返回的 HTML：与 worker 渲染 PDF 用的是同一份。
        sandbox 给 allow-same-origin 与 allow-modals（沙箱内弹打印框需要），
        但**不给 allow-scripts** —— 模板本来就没有脚本，多给的权限只会是风险。
      */}
      <iframe
        ref={iframeRef}
        title="打印预览"
        className="min-h-0 w-full flex-1 border-0 bg-white"
        sandbox="allow-same-origin allow-modals"
        srcDoc={previewQuery.data}
      />
    </div>
  )
}
