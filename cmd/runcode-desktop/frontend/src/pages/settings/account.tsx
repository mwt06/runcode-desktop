// 账号：登录态 + 租户切换。登录/登出/切租户直接落后端；租户的平台模型列表经
// onAccount 回流父级（与自定义模型合成会话/判定模型的候选）。
import { useEffect, useRef, useState } from 'react'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { LABEL_CLS, SelectField } from '@/ui/fields'
import {
  createPassportAccountCoordinator,
  initialPassportAccountSnapshot,
  type PassportAccountCoordinator,
} from '@/core/passport-account'
import {
  activeTenant, errText, Events, onEvent,
  passportCancelLogin, passportLogin, passportLogout, passportModels, passportStatus, passportTenants,
  setActiveTenant,
  type PassportModel,
} from '@/core/bridge'
import { Section } from './section'
import { InlineError } from '@/ui/feedback'
import { InsetRow } from '@/ui/layout'

export function AccountSection({ onAccount, onBusy }: {
  onAccount: (tenantId: string, models: PassportModel[], ready: boolean) => void
  onBusy: (busy: boolean) => void
}) {
  const [account, setAccount] = useState(initialPassportAccountSnapshot)
  const coordinatorRef = useRef<PassportAccountCoordinator | null>(null)
  const [loggingIn, setLoggingIn] = useState(false)
  const [acctMsg, setAcctMsg] = useState('')
  const passport = account.status

  useEffect(() => {
    let alive = true
    const coordinator = createPassportAccountCoordinator({
      passportStatus,
      passportTenants,
      activeTenant,
      setActiveTenant,
      passportModels,
      errorText: errText,
    }, (next) => {
      if (!alive) return
      setAccount(next)
      onBusy(next.phase === 'resolving')
      if (next.phase === 'ready') onAccount(next.tenantId, next.models, true)
      else if (next.phase === 'logged-out') onAccount('', [], true)
      else if (next.phase === 'error' && next.tenantId) onAccount(next.tenantId, [], false)
      else onAccount(next.tenantId, [], false)
      setAcctMsg(next.error)
    })
    coordinatorRef.current = coordinator
    onBusy(true)
    void coordinator.refresh()
    const off = onEvent(Events.PassportChanged, (status) => { void coordinator.refresh(status) })
    return () => {
      alive = false
      off()
      coordinator.dispose()
      coordinatorRef.current = null
      onBusy(false)
    }
    // 只挂载时建一次协调器:onAccount/onBusy 由父级内联定义,每次渲染都是新引用,
    // 列进依赖会导致通行证协调器被反复销毁重建(重发 /api/me 与租户请求)。二者都
    // 只是 setState 的包装,不持有需要刷新的状态。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const doAcctLogin = async (scheme: string) => {
    setLoggingIn(true); setAcctMsg('')
    try { await passportLogin(scheme) } catch (e) { setAcctMsg(errText(e)) } finally { setLoggingIn(false) }
  }
  const onSwitchTenant = async (tid: string) => {
    setAcctMsg('')
    await coordinatorRef.current?.selectTenant(tid)
  }
  return (
    <Section title="账号(通行证)">
      {passport.loggedIn ? (
        <InsetRow>
          <span className="text-[13px]">已登录：<b>{passport.name || passport.userName || passport.userId}</b></span>
          <button type="button" className="text-[13px] text-muted hover:text-red" onClick={() => { void passportLogout() }}>登出</button>
        </InsetRow>
      ) : (
        <div className="flex flex-col gap-1.5">
          <button type="button" className={`${BTN} ${BTN_PRIMARY} py-2.5`} disabled={loggingIn} onClick={() => void doAcctLogin('OneOuchnPassport')}>
            {loggingIn ? '等待浏览器登录…' : '统一认证登录'}
          </button>
          <button type="button" className={`${BTN} py-2.5`} disabled={loggingIn} onClick={() => void doAcctLogin('')}>
            基座通行证登录
          </button>
          {loggingIn && <button type="button" className="text-[12px] text-muted" onClick={() => void passportCancelLogin()}>取消</button>}
        </div>
      )}
      {passport.loggedIn && account.eligibleTenants.length > 0 && (
        <label className={LABEL_CLS}>租户(切换后在下方重新选择模型即切到该租户,无需新建会话)
          <SelectField value={account.tenantId} disabled={account.phase === 'resolving'} onChange={(v) => void onSwitchTenant(v)}>
            {!account.tenantId && account.eligibleTenants.length > 1 && <option value="">请选择末级租户</option>}
            {account.eligibleTenants.map((tenant) => <option key={tenant.id} value={tenant.id}>{tenant.name}（{tenant.id}）</option>)}
          </SelectField>
        </label>
      )}
      {acctMsg && <InlineError variant="text">{acctMsg}</InlineError>}
    </Section>
  )
}
