import { type FormEvent, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import { readableError } from '../../api/client'
import { login, register } from '../../api/endpoints/auth'
import { Button } from '../../components/Button'

type Mode = 'login' | 'register'

/** 登录 / 注册（公开注册已关闭，注册需要家长邀请码）。 */
export function LoginPage() {
  const navigate = useNavigate()
  const [mode, setMode] = useState<Mode>('login')
  const [identifier, setIdentifier] = useState('')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const id = identifier.trim()
    if (!id || !password) {
      setError('请填写账号和密码')
      return
    }
    if (mode === 'register' && !displayName.trim()) {
      setError('请填写家长称呼')
      return
    }

    // 一个输入框同时收邮箱与手机号；登录接口只认 account（后端按账号查库），
    // 注册才需要区分 email / phone 分别落库。
    const credential = id.includes('@') ? { email: id } : { phone: id }

    setBusy(true)
    setError('')
    try {
      if (mode === 'login') {
        await login({ account: id, password })
      } else {
        await register({
          ...credential,
          password,
          display_name: displayName.trim(),
          invite_code: inviteCode.trim(),
        })
      }
      navigate('/select-child', { replace: true })
    } catch (err) {
      setError(readableError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="card" onSubmit={(event) => void submit(event)}>
      <h1 className="mb-1 text-2xl font-bold">KidStudy</h1>
      <p className="mb-5 text-sm text-ink-soft">陪孩子每天学一点，家长看得见进度。</p>

      {mode === 'register' && (
        <label className="mb-3 block">
          <span className="mb-1 block text-sm text-ink-soft">家长称呼</span>
          <input
            className="w-full rounded-xl border border-line bg-surface px-4 py-3 text-ink"
            value={displayName}
            onChange={(event) => setDisplayName(event.target.value)}
            placeholder="如：妈妈"
            autoComplete="nickname"
          />
        </label>
      )}

      <label className="mb-3 block">
        <span className="mb-1 block text-sm text-ink-soft">邮箱或手机号</span>
        <input
          className="w-full rounded-xl border border-line bg-surface px-4 py-3 text-ink"
          value={identifier}
          onChange={(event) => setIdentifier(event.target.value)}
          placeholder="parent@example.com"
          autoComplete="username"
        />
      </label>

      <label className="mb-3 block">
        <span className="mb-1 block text-sm text-ink-soft">密码</span>
        <input
          type="password"
          className="w-full rounded-xl border border-line bg-surface px-4 py-3 text-ink"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
        />
      </label>

      {mode === 'register' && (
        <label className="mb-3 block">
          <span className="mb-1 block text-sm text-ink-soft">家长邀请码</span>
          <input
            className="w-full rounded-xl border border-line bg-surface px-4 py-3 text-ink"
            value={inviteCode}
            onChange={(event) => setInviteCode(event.target.value)}
            placeholder="首次部署时由管理员生成"
          />
        </label>
      )}

      {error && (
        <p className="mb-3 rounded-xl border border-line bg-surface px-3 py-2 text-sm text-danger" role="alert">
          {error}
        </p>
      )}

      <Button variant="primary" className="w-full" type="submit" disabled={busy}>
        {busy ? '请稍候…' : mode === 'login' ? '登录' : '注册并登录'}
      </Button>

      <div className="mt-4 flex items-center justify-between text-sm">
        <button
          type="button"
          className="text-brand underline"
          onClick={() => {
            setMode(mode === 'login' ? 'register' : 'login')
            setError('')
          }}
        >
          {mode === 'login' ? '用邀请码注册' : '已有账号，去登录'}
        </button>
        <Link className="text-brand underline" to="/qr-login">
          大屏扫码登录
        </Link>
      </div>
    </form>
  )
}
