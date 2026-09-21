import { Component, type ErrorInfo, type ReactNode } from 'react'

type Props = { children: ReactNode }
type State = { error: Error | null }

/**
 * 顶层错误边界：任何页面渲染崩了都不该变成白屏。
 * 这里只展示可读文案 + 重载按钮，堆栈进控制台（不给用户看内部细节）。
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error('[KidStudy] 页面渲染异常', error, info.componentStack)
  }

  private handleReload = (): void => {
    window.location.reload()
  }

  render(): ReactNode {
    const { error } = this.state
    if (!error) return this.props.children

    return (
      <div className="flex min-h-full items-center justify-center p-6">
        <div className="card max-w-md text-center">
          <h1 className="mb-2 text-xl font-bold">页面出了点小问题</h1>
          <p className="mb-4 text-ink-soft">{error.message || '未知错误'}</p>
          <button type="button" className="btn btn-primary" onClick={this.handleReload}>
            重新加载
          </button>
        </div>
      </div>
    )
  }
}
