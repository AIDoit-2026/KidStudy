/**
 * 家长设置（§六 /parent/settings）—— M6 护眼能力的「控制面板」。
 *
 * 分两块，职责不同：
 *  - 护眼外观（主题 / 字号 / 亮度）：只存在本机，改了立刻见效，不走网络。
 *  - 时长与节奏（每日上限 / 单次上限 / 休息间隔 / 家长确认）：服务端才是权威，
 *    因为锁屏与达标判定都以后端时间为准，本地改不了。
 */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { readableError } from '../../api/client'
import { setPin } from '../../api/endpoints/auth'
import { archiveChild, createChild, listChildren } from '../../api/endpoints/children'
import { getSettings, updateSettings } from '../../api/endpoints/parent'
import type { ParentSettings, ParentSettingsPatch } from '../../api/types'
import { Button } from '../../components/Button'
import { AVATAR_IDS, avatarEmoji } from '../../components/ChildAvatar'
import {
  BRIGHTNESS_MAX,
  BRIGHTNESS_MIN,
  FONT_SCALE_MAX,
  FONT_SCALE_MIN,
  useThemeStore,
  type ThemeMode,
} from '../../stores/themeStore'

const THEME_OPTIONS: Array<{ value: ThemeMode; label: string }> = [
  { value: 'sepia', label: '米黄纸感' },
  { value: 'light', label: '清爽浅色' },
  { value: 'dark', label: '夜间深色' },
  { value: 'auto', label: '跟随系统' },
]

export function SettingsPage() {
  const queryClient = useQueryClient()
  const settingsQuery = useQuery({ queryKey: ['parent-settings'], queryFn: getSettings })
  const [draft, setDraft] = useState<ParentSettingsPatch>({})
  const [notice, setNotice] = useState('')
  const [pinInput, setPinInput] = useState('')

  // 护眼外观：订阅式取值，改完立刻反映到 UI（不是 getState 那种一次性快照）
  const themeMode = useThemeStore((s) => s.mode)
  const followSunset = useThemeStore((s) => s.followSunset)
  const fontScale = useThemeStore((s) => s.fontScale)
  const brightness = useThemeStore((s) => s.brightness)
  const setMode = useThemeStore((s) => s.setMode)
  const setFollowSunset = useThemeStore((s) => s.setFollowSunset)
  const setFontScale = useThemeStore((s) => s.setFontScale)
  const setBrightness = useThemeStore((s) => s.setBrightness)

  const mutation = useMutation({
    mutationFn: (patch: ParentSettingsPatch) => updateSettings(patch),
    onSuccess: (saved) => {
      queryClient.setQueryData(['parent-settings'], saved)
      setDraft({})
      setNotice('已保存')
      window.setTimeout(() => setNotice(''), 2_000)
    },
  })

  const pinMutation = useMutation({
    mutationFn: (pin: string) => setPin(pin),
    onSuccess: () => {
      setPinInput('')
      setNotice('PIN 已设置')
      void queryClient.invalidateQueries({ queryKey: ['me'] })
      window.setTimeout(() => setNotice(''), 2_000)
    },
  })

  const settings = settingsQuery.data
  // draft 是 Partial 补丁（不含 updated_at），索引约束必须跟着补丁类型走，
  // 否则 keyof ParentSettings 里多出来的 updated_at 会让这块索引直接报 TS2536。
  const value = <K extends keyof ParentSettingsPatch>(key: K): ParentSettings[K] =>
    (draft[key] ?? settings?.[key]) as ParentSettings[K]

  const setField = <K extends keyof ParentSettingsPatch>(key: K, v: ParentSettingsPatch[K]) =>
    setDraft((prev) => ({ ...prev, [key]: v }))

  if (settingsQuery.isLoading && !settings) {
    return <p className="text-ink-soft">正在加载设置…</p>
  }
  if (settingsQuery.isError && !settings) {
    return (
      <div className="card">
        <p className="mb-3">{readableError(settingsQuery.error)}</p>
        <Button onClick={() => void settingsQuery.refetch()}>重试</Button>
      </div>
    )
  }

  const dirty = Object.keys(draft).length > 0

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">家长设置</h1>
        <Link className="text-sm text-brand underline" to="/parent/print">
          打印中心
        </Link>
      </div>

      {notice && (
        <p
          className="rounded-xl border border-line bg-surface px-4 py-2 text-sm"
          style={{ color: 'var(--c-success)' }}
          role="status"
        >
          {notice}
        </p>
      )}

      {/* ---------------------------------------------------- 护眼外观 */}
      <section className="card">
        <h2 className="mb-1 font-semibold">护眼外观</h2>
        <p className="mb-3 text-xs text-ink-soft">这几项只保存在这台设备上，改完立刻生效。</p>

        <div className="mb-4">
          <span className="mb-2 block text-sm text-ink-soft">主题</span>
          <div className="flex flex-wrap gap-2">
            {THEME_OPTIONS.map((option) => (
              <button
                key={option.value}
                type="button"
                className={`btn ${themeMode === option.value ? 'is-active' : ''}`}
                aria-pressed={themeMode === option.value}
                onClick={() => setMode(option.value)}
              >
                {option.label}
              </button>
            ))}
          </div>
        </div>

        <label className="mb-4 flex items-center gap-3">
          <input
            type="checkbox"
            className="h-5 w-5"
            checked={followSunset}
            onChange={(event) => setFollowSunset(event.target.checked)}
          />
          <span className="text-sm">日落后自动切深色（18:00 – 次日 07:00）</span>
        </label>

        <RangeField
          label="字号"
          value={fontScale}
          min={FONT_SCALE_MIN}
          max={FONT_SCALE_MAX}
          step={0.05}
          format={(v) => `${Math.round(v * 100)}%`}
          onChange={setFontScale}
        />
        <RangeField
          label="亮度"
          value={brightness}
          min={BRIGHTNESS_MIN}
          max={BRIGHTNESS_MAX}
          step={0.05}
          format={(v) => `${Math.round(v * 100)}%`}
          onChange={setBrightness}
        />
      </section>

      {/* ---------------------------------------------------- 时长与节奏 */}
      <section className="card">
        <h2 className="mb-1 font-semibold">时长与节奏</h2>
        <p className="mb-3 text-xs text-ink-soft">
          这些会同步到服务器。真正的计时以后端为准，改本机时间绕不过去。
        </p>

        <NumberField
          label="每日总时长（分钟）"
          hint="0 表示不限，最多 480"
          min={0}
          max={480}
          value={value('daily_limit_min')}
          onChange={(v) => setField('daily_limit_min', v)}
        />
        <NumberField
          label="单次时长上限（分钟）"
          hint="5–120 分钟，到时锁屏，需要家长 PIN 才能继续"
          min={5}
          max={120}
          value={value('session_limit_min')}
          onChange={(v) => setField('session_limit_min', v)}
        />
        <NumberField
          label="休息提醒间隔（分钟）"
          hint="0 表示不强制；20-20-20：每满这个时长弹一次休息页"
          min={0}
          max={120}
          value={value('rest_interval_min')}
          onChange={(v) => setField('rest_interval_min', v)}
        />

        <label className="mt-3 flex items-center gap-3">
          <input
            type="checkbox"
            className="h-5 w-5"
            checked={value('require_parent_confirm')}
            onChange={(event) => setField('require_parent_confirm', event.target.checked)}
          />
          <span className="text-sm">每日完成需要家长确认（描红、跟读等主观项）</span>
        </label>

        <label className="mt-3 flex items-center gap-3">
          <input
            type="checkbox"
            className="h-5 w-5"
            checked={value('compare_children')}
            onChange={(event) => setField('compare_children', event.target.checked)}
          />
          <span className="text-sm">开启多孩对比（只并列展示，不做排名）</span>
        </label>
      </section>

      {dirty && (
        <div className="sticky bottom-4 flex items-center gap-3 rounded-2xl border border-line bg-raised p-3 shadow-lg">
          <span className="flex-1 text-sm text-ink-soft">有未保存的改动</span>
          <Button onClick={() => setDraft({})}>放弃</Button>
          <Button variant="primary" disabled={mutation.isPending} onClick={() => mutation.mutate(draft)}>
            {mutation.isPending ? '保存中…' : '保存'}
          </Button>
        </div>
      )}
      {mutation.isError && <p className="text-sm text-danger">{readableError(mutation.error)}</p>}

      {/* ---------------------------------------------------- 孩子档案 */}
      <ChildSection />

      {/* ---------------------------------------------------- PIN */}
      <section className="card">
        <h2 className="mb-1 font-semibold">家长 PIN</h2>
        <p className="mb-3 text-xs text-ink-soft">用于锁屏解锁、跳过休息、切换孩子。4–6 位数字。</p>
        <div className="flex gap-2">
          <input
            type="password"
            inputMode="numeric"
            maxLength={6}
            value={pinInput}
            onChange={(event) => setPinInput(event.target.value.replace(/\D/g, ''))}
            className="w-40 rounded-xl border border-line bg-surface px-4 py-3 text-center text-lg tracking-[0.4em] text-ink"
            placeholder="••••"
            aria-label="新的家长 PIN"
          />
          <Button
            variant="primary"
            disabled={pinInput.length < 4 || pinMutation.isPending}
            onClick={() => pinMutation.mutate(pinInput)}
          >
            {pinMutation.isPending ? '设置中…' : '设置 PIN'}
          </Button>
        </div>
        {pinMutation.isError && (
          <p className="mt-2 text-sm text-danger">{readableError(pinMutation.error)}</p>
        )}
      </section>
    </div>
  )
}

// ---------------------------------------------------------------- 小控件

function RangeField({
  label,
  value,
  min,
  max,
  step,
  format,
  onChange,
}: {
  label: string
  value: number
  min: number
  max: number
  step: number
  format: (v: number) => string
  onChange: (v: number) => void
}) {
  return (
    <label className="mb-3 block">
      <span className="mb-1 flex items-center justify-between text-sm">
        <span className="text-ink-soft">{label}</span>
        <span className="text-ink-soft">{format(value)}</span>
      </span>
      <input
        type="range"
        className="w-full"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(event) => onChange(Number(event.target.value))}
      />
    </label>
  )
}

function NumberField({
  label,
  hint,
  min,
  max,
  value,
  onChange,
}: {
  label: string
  hint?: string
  /** 上下限与后端 validate() 一致，避免家长填出 422 才发现 */
  min: number
  max: number
  value: number
  onChange: (v: number) => void
}) {
  return (
    <label className="mb-3 flex items-center gap-3">
      <span className="flex-1">
        <span className="block text-sm">{label}</span>
        {hint && <span className="block text-xs text-ink-soft">{hint}</span>}
      </span>
      <input
        type="number"
        min={min}
        max={max}
        className="w-24 rounded-xl border border-line bg-surface px-3 py-2 text-center text-ink"
        value={value}
        onChange={(event) => {
          const raw = Number(event.target.value) || 0
          onChange(Math.min(max, Math.max(min, raw)))
        }}
      />
    </label>
  )
}

// ---------------------------------------------------------------- 孩子档案

/**
 * 孩子档案：列表 + 新增 + 归档。
 *
 * 为什么放在家长设置里：「选孩子」页在没有任何档案时会引导家长来这里，
 * 如果这里没有入口，新注册的家长就卡在死路上——一个孩子都建不出来，什么都用不了。
 */
function ChildSection() {
  const queryClient = useQueryClient()
  const [nickname, setNickname] = useState('')
  const [avatarId, setAvatarId] = useState(AVATAR_IDS[0])
  const [birthYm, setBirthYm] = useState('')
  const [stageCode, setStageCode] = useState('')
  const [notice, setNotice] = useState('')

  const childrenQuery = useQuery({ queryKey: ['children'], queryFn: listChildren })

  const createMutation = useMutation({
    mutationFn: () => {
      const birth = birthYm.trim()
      return createChild({
        nickname: nickname.trim(),
        avatar_id: avatarId,
        // 空串不能发：后端对可选字段的空串会当作非法值，直接省略更干净
        birth_ym: birth || undefined,
        stage_code: stageCode.trim() || undefined,
      })
    },
    onSuccess: () => {
      setNickname('')
      setBirthYm('')
      setStageCode('')
      setNotice('已添加孩子档案')
      void queryClient.invalidateQueries({ queryKey: ['children'] })
      window.setTimeout(() => setNotice(''), 2_000)
    },
  })

  const archiveMutation = useMutation({
    mutationFn: (id: string) => archiveChild(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['children'] })
    },
  })

  const children = childrenQuery.data ?? []
  const birthInvalid = birthYm.trim() !== '' && !/^\d{6}$/.test(birthYm.trim())
  const canCreate = nickname.trim() !== '' && !birthInvalid && !createMutation.isPending

  return (
    <section className="card">
      <h2 className="mb-1 font-semibold">孩子档案</h2>
      <p className="mb-3 text-xs text-ink-soft">
        归档只做软删除：历史学习记录会保留，随时可以再建一个同名档案。
      </p>

      {childrenQuery.isLoading && <p className="text-sm text-ink-soft">正在加载…</p>}
      {childrenQuery.isError && (
        <p className="mb-3 text-sm text-danger">{readableError(childrenQuery.error)}</p>
      )}

      {children.length > 0 && (
        <ul className="mb-4 flex flex-col divide-y divide-line">
          {children.map((child) => (
            <li key={child.id} className="flex items-center gap-3 py-2">
              <span className="text-2xl" aria-hidden="true">
                {avatarEmoji(child.avatar_id)}
              </span>
              <span className="flex-1">
                <span className="block font-medium">{child.nickname}</span>
                <span className="block text-xs text-ink-soft">
                  {[child.stage_code, child.birth_ym].filter(Boolean).join(' · ') || '未填写阶段'}
                </span>
              </span>
              <Button
                className="!px-3 !py-1.5 text-sm"
                disabled={archiveMutation.isPending}
                onClick={() => {
                  if (window.confirm(`确定归档「${child.nickname}」？历史记录会保留。`)) {
                    archiveMutation.mutate(child.id)
                  }
                }}
              >
                归档
              </Button>
            </li>
          ))}
        </ul>
      )}

      {notice && (
        <p className="mb-3 text-sm" style={{ color: 'var(--c-success)' }} role="status">
          {notice}
        </p>
      )}

      <div className="rounded-xl border border-line bg-surface p-3">
        <h3 className="mb-3 text-sm font-semibold">添加孩子</h3>

        <label className="mb-3 flex items-center gap-3">
          <span className="w-28 shrink-0 text-sm text-ink-soft">昵称</span>
          <input
            className="flex-1 rounded-xl border border-line bg-surface px-3 py-2 text-ink"
            value={nickname}
            onChange={(event) => setNickname(event.target.value)}
            placeholder="如：哥哥"
          />
        </label>

        <div className="mb-3 flex items-start gap-3">
          <span className="w-28 shrink-0 pt-1 text-sm text-ink-soft">头像</span>
          <div className="flex flex-wrap gap-2">
            {AVATAR_IDS.map((id) => (
              <button
                key={id}
                type="button"
                className={`btn !px-3 !py-1 text-2xl ${avatarId === id ? 'is-active' : ''}`}
                aria-label={`选择头像 ${id}`}
                aria-pressed={avatarId === id}
                onClick={() => setAvatarId(id)}
              >
                {avatarEmoji(id)}
              </button>
            ))}
          </div>
        </div>

        <label className="mb-3 flex items-center gap-3">
          <span className="w-28 shrink-0 text-sm text-ink-soft">出生年月</span>
          <input
            className="w-40 rounded-xl border border-line bg-surface px-3 py-2 text-ink"
            value={birthYm}
            onChange={(event) => setBirthYm(event.target.value.replace(/\D/g, '').slice(0, 6))}
            placeholder="202001"
            inputMode="numeric"
          />
          {birthInvalid && <span className="text-xs text-danger">需要 6 位数字，如 202001</span>}
        </label>

        <label className="mb-3 flex items-center gap-3">
          <span className="w-28 shrink-0 text-sm text-ink-soft">学习阶段</span>
          <input
            className="w-40 rounded-xl border border-line bg-surface px-3 py-2 text-ink"
            value={stageCode}
            onChange={(event) => setStageCode(event.target.value.toUpperCase().slice(0, 4))}
            placeholder="如 S1（可留空）"
          />
        </label>

        {createMutation.isError && (
          <p className="mb-2 text-sm text-danger">{readableError(createMutation.error)}</p>
        )}

        <Button variant="primary" disabled={!canCreate} onClick={() => createMutation.mutate()}>
          {createMutation.isPending ? '添加中…' : '添加孩子'}
        </Button>
      </div>
    </section>
  )
}
