/**
 * 选择孩子（§六 /select-child）。
 *
 * 进入孩子端会打开「投屏模式」（kidMode），孩子端 UI 里不出现家长入口；
 * 如果家长设过 PIN，切换孩子要先验 PIN，避免孩子自己换到别的档案。
 */
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

import { listChildren } from '../../api/endpoints/children'
import { me } from '../../api/endpoints/auth'
import type { Child } from '../../api/types'
import { Button } from '../../components/Button'
import { ChildAvatar } from '../../components/ChildAvatar'
import { PinDialog } from '../../components/PinDialog'
import { useChildStore } from '../../stores/childStore'

export function SelectChildPage() {
  const navigate = useNavigate()
  const setCurrentChild = useChildStore((s) => s.setCurrentChild)
  const setKidMode = useChildStore((s) => s.setKidMode)
  const [pinTarget, setPinTarget] = useState<Child | null>(null)

  const childrenQuery = useQuery({ queryKey: ['children'], queryFn: listChildren })
  const meQuery = useQuery({ queryKey: ['me'], queryFn: me })

  const hasPin = meQuery.data?.has_pin ?? false

  const enter = (child: Child) => {
    setCurrentChild(child.id)
    setKidMode(true)
    navigate('/child/home', { replace: true })
  }

  const handlePick = (child: Child) => {
    if (hasPin) {
      setPinTarget(child)
      return
    }
    enter(child)
  }

  return (
    <div className="mx-auto w-full max-w-3xl">
      <div className="mb-5 flex items-center justify-between">
        <h1 className="text-xl font-bold">
          {meQuery.data?.display_name ? `${meQuery.data.display_name}，` : ''}今天谁学？
        </h1>
        <Button variant="ghost" onClick={() => navigate('/parent/settings')}>
          家长设置
        </Button>
      </div>

      {childrenQuery.isLoading && <p className="text-ink-soft">正在加载孩子档案…</p>}

      {childrenQuery.isError && (
        <div className="card">
          <p className="mb-3">孩子档案没能加载出来。</p>
          <Button onClick={() => void childrenQuery.refetch()}>重试</Button>
        </div>
      )}

      {childrenQuery.data && childrenQuery.data.length === 0 && (
        <div className="card text-center">
          <p className="mb-3">还没有孩子档案，先建一个吧。</p>
          <Button variant="primary" onClick={() => navigate('/parent/settings')}>
            去创建
          </Button>
        </div>
      )}

      <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
        {(childrenQuery.data ?? []).map((child) => (
          <button
            key={child.id}
            type="button"
            className="card focusable flex flex-col items-center gap-2 py-6 transition-transform duration-ui"
            onClick={() => handlePick(child)}
          >
            <ChildAvatar id={child.avatar_id} />
            <span className="text-lg font-semibold">{child.nickname}</span>
            {child.stage_code && <span className="text-xs text-ink-soft">{child.stage_code}</span>}
          </button>
        ))}
      </div>

      <PinDialog
        open={pinTarget !== null}
        title="切换孩子"
        description="请输入家长 PIN 确认切换"
        onClose={() => setPinTarget(null)}
        onUnlocked={() => {
          const target = pinTarget
          setPinTarget(null)
          if (target) enter(target)
        }}
      />
    </div>
  )
}
