// StartForm 是首屏：验证登录态 → (未登录且无直连模型时) 登录门 → 选租户 + 工作区
// + 模型 → 开始会话。权限模式与高级项都在设置页，这里只做"进哪个工作区、用哪个
// 模型"这一件事。
import { useEffect, useRef, useState } from 'react'
import { Icon, Logo } from '@/ui/icons'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { FIELD_CLS, LABEL_CLS } from '@/ui/fields'
import { shortenPath } from '@/core/paths'
import { BRAND } from '@/core/brand'
import { customModelProviderLabel } from '@/core/custom-models'
import {
  canAutoStartPassport,
  createPassportAccountCoordinator,
  initialPassportAccountSnapshot,
  type PassportAccountCoordinator,
} from '@/core/passport-account'
import {
  activeTenant, errText, Events, listCustomModels, onEvent,
  passportLogin, passportLogout, passportModels, passportStatus, passportTenants, passportValidate,
  pickWorkspaceFolder, setActiveTenant,
  type CustomModel, type StartSessionRequest,
} from '@/core/bridge'
import { Splash, SplashSpinner } from './splash'
import { LoginGate } from './login-gate'
import { buildTenantTree, TenantTree } from './tenant-tree'

export function StartForm({ onStart, starting, error, initial }: { onStart: (req: StartSessionRequest) => void; starting: boolean; error: string; initial: Partial<StartSessionRequest> }) {
  const [cwd, setCwd] = useState(initial.cwd ?? '')
  const [account, setAccount] = useState(initialPassportAccountSnapshot)
  const accountCoordinator = useRef<PassportAccountCoordinator | null>(null)
  const passport = account.status
  const tenants = account.tenants
  const tenantId = account.tenantId
  const platformModels = account.models
  const eligibleTenantIds = new Set(account.eligibleTenants.map((tenant) => tenant.id))
  const [customModels, setCustomModels] = useState<CustomModel[]>([])
  const [loggingIn, setLoggingIn] = useState(false)
  const [passportError, setPassportError] = useState('')
  // validating gates the whole form on a one-time startup token check: the
  // persisted token is verified against the server before we decide login vs
  // form, so an expired/revoked token lands on the login screen, not a broken form.
  const [validating, setValidating] = useState(true)
  // modelChoice: 'passport:<id>' | 'custom:<name>' | ''（未选）。
  // 手动配置/高级默认项都移到设置页；这里只在登录 + 选定租户后选择一个模型。
  const [modelChoice, setModelChoice] = useState(initial.provider === 'passport' && initial.model ? `passport:${initial.model}` : '')
  const recent = (initial.recentWorkspaces ?? []).filter((w) => w && w !== cwd)
  const browse = async () => {
    try {
      const dir = await pickWorkspaceFolder()
      if (dir) setCwd(dir)
    } catch { /* user cancelled the native picker */ }
  }

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
      if (next.error) setPassportError(next.error)
      else if (next.phase === 'ready' || next.phase === 'logged-out') setPassportError('')
      if (next.phase === 'error' || next.phase === 'ready' || next.phase === 'logged-out') setValidating(false)
    })
    accountCoordinator.current = coordinator

    // Custom profiles are local and remain usable without a Passport login.
    listCustomModels().then((models) => {
      if (alive) setCustomModels(models ?? [])
    }).catch(() => {})

    // Token validation and tenant reconciliation are one startup gate. A Passport
    // session is not ready until its leaf tenant is persisted and its models loaded.
    passportValidate()
      .then((status) => coordinator.refresh(status))
      .catch((e) => {
        if (alive) {
          setPassportError(`验证登录状态失败：${errText(e)}`)
          setValidating(false)
        }
      })
    const off = onEvent(Events.PassportChanged, (status) => {
      if (!status.loggedIn) setModelChoice('')
      void coordinator.refresh(status)
    })
    return () => {
      alive = false
      off()
      coordinator.dispose()
      accountCoordinator.current = null
    }
  }, [])

  // 用户选择末级租户时，先持久化后端活动租户，再加载该租户模型。协调器会
  // 拒绝父节点/未知 id，并防止较慢的旧请求覆盖新选择。
  const selectTenant = async (tid: string) => {
    if (modelChoice.startsWith('passport:')) setModelChoice('')
    await accountCoordinator.current?.selectTenant(tid)
  }

  // scheme selects the upstream identity source: 'OneOuchnPassport' for 统一认证,
  // '' for the base passport (基座通行证). PassportLogin emits PassportChanged;
  // that event is the sole catalog refresh trigger, avoiding duplicate requests.
  const doLogin = async (scheme: string) => {
    setLoggingIn(true); setPassportError('')
    try {
      await passportLogin(scheme)
    } catch (e) {
      setPassportError(errText(e))
    } finally { setLoggingIn(false) }
  }

  // buildRequest maps the selected model onto the wire request. A passport model
  // needs its id + the selected tenant; a custom model sends only its saved profile
  // name so the backend resolves Base URL/key without exposing them to the renderer.
  // The session's permission mode and
  // advanced knobs come from the saved settings (initial.*) — they are edited on
  // the Settings page, not here. Returns null when nothing valid is selected.
  const buildRequest = (): StartSessionRequest | null => {
    const base = {
      cwd,
      permissionMode: initial.permissionMode || 'interactive',
      thinkingEffort: initial.thinkingEffort ?? '',
      maxContextTokens: initial.maxContextTokens ?? 128000,
      harmJudgeModel: initial.harmJudgeModel ?? '',
      harmJudgeVotes: initial.harmJudgeVotes ?? 1,
      // 起始页不涉及的字段按 wire 零值发送 —— 与旧版直接省略这些键时 Go 端
      // json 反序列化得到的零值完全一致（生成的 StartSessionRequest 为全量必填）。
      customModelName: '',
      tenantId: '',
      baseURL: '',
      apiKey: '',
      authToken: '',
      reasoningScenario: '',
      maxTokens: 0,
      maxHistoryMessages: 0,
      resume: '',
      continue: false,
    }
    if (modelChoice.startsWith('passport:')) {
      const selectedModel = modelChoice.slice('passport:'.length)
      if (account.phase !== 'ready' || !passport.loggedIn || !eligibleTenantIds.has(tenantId)) return null
      if (!platformModels.some((model) => model.id === selectedModel)) return null
      return { ...base, provider: 'passport', model: selectedModel, tenantId }
    }
    if (modelChoice.startsWith('custom:')) {
      const cm = customModels.find((m) => `custom:${m.name}` === modelChoice)
      if (!cm) return null
      return { ...base, provider: cm.provider || 'openai', model: cm.model, customModelName: cm.name }
    }
    return null
  }

  const tenantTree = buildTenantTree(tenants)

  // 自动进入只在首个完整的 Passport ready 快照后评估一次。这样单租户必须
  // 已真正绑定且模型目录已返回，多租户无有效旧选择时也不会在用户手选后误触发。
  const autoStartEvaluated = useRef(false)
  const autoStarted = useRef(false)
  const [autoEntering, setAutoEntering] = useState(false)
  useEffect(() => {
    if (autoStarted.current || autoStartEvaluated.current || account.phase !== 'ready' || !passport.loggedIn) return
    autoStartEvaluated.current = true
    if (starting || error || !canAutoStartPassport(initial, account)) return
    const req = buildRequest()
    if (!req) return
    autoStarted.current = true
    setAutoEntering(true)
    onStart(req)
    // 自动进入只在首个完整 ready 快照后评估一次(autoStartEvaluated 守住)。
    // buildRequest/initial/onStart 每次渲染都是新引用,列进依赖只会让这个"一次性"
    // 判定被反复重跑,而守卫 ref 又会立刻把它挡掉——徒增噪音,不改变行为。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [account, passport.loggedIn, starting, error])

  // 启动校验中：转圈的加载门，验完持久化 token 再决定进登录还是表单。
  if (validating) {
    return (
      <Splash>
        <SplashSpinner />
        <div className="mt-5 text-[14px] tracking-wide animate-pulse" style={{ color: '#2050d8' }}>正在验证登录状态…</div>
      </Splash>
    )
  }

  // 未登录且没有本地自定义模型时显示登录门；已有直连配置不依赖 Passport，
  // 可以继续进入工作区表单并直接启动。
  if (!passport.loggedIn && customModels.length === 0) {
    return (
      <LoginGate
        loggingIn={loggingIn}
        error={passportError}
        customModels={customModels}
        onLogin={(scheme) => void doLogin(scheme)}
        onCustomModelsChanged={setCustomModels}
      />
    )
  }

  // 自动进入中：不闪现完整表单，维持登录门同款背景的过渡页；失败回落表单。
  if (autoEntering && !error) {
    return (
      <Splash>
        <div className="mt-7 text-[15px] text-muted">正在进入工作区 <span className="font-mono">{shortenPath(initial.cwd ?? '')}</span>…</div>
      </Splash>
    )
  }

  return (
    <div className="flex items-start justify-center flex-1 min-h-0 overflow-y-auto px-6 py-10">
      <div className="w-[480px] flex flex-col gap-[13px]">
        <div className="flex items-center gap-3.5 mb-1">
          <span className="w-[48px] h-[48px] rounded-[13px] inline-flex items-center justify-center bg-surface border border-line2 shadow-xs"><Logo size={34} /></span>
          <div>
            <h1 className="m-0 text-[22px] font-bold tracking-tight">{BRAND.name}</h1>
            <p className="mt-[3px] text-muted text-[13px]">{BRAND.tagline}</p>
          </div>
        </div>
        {passport.loggedIn ? (
          <div className="flex items-center justify-between rounded-[9px] border border-line2 bg-surface2 px-3 py-2.5">
            <span className="text-[13px]">已登录：<b>{passport.name || passport.userName || passport.userId}</b></span>
            <button type="button" className="text-[12px] text-muted hover:text-ink" onClick={() => { void passportLogout() }}>登出</button>
          </div>
        ) : (
          <div className="flex items-center justify-between rounded-[9px] border border-line2 bg-surface2 px-3 py-2.5">
            <span className="text-[12.5px] text-muted">当前使用本地自定义模型；登录后还可选择平台模型。</span>
            <button type="button" className="text-[12px] text-primary hover:text-primaryink" onClick={() => void doLogin('OneOuchnPassport')}>登录通行证</button>
          </div>
        )}
        {passport.loggedIn && (
          <div className={LABEL_CLS}>
            <span>
              租户
              {account.eligibleTenants.length > 1 && <span className="font-normal text-muted">（只能选择末级，选定后可选模型）</span>}
            </span>
            {account.phase === 'resolving' && account.eligibleTenants.length === 0 ? (
              <div className="rounded-[9px] border border-line2 bg-surface2 px-3 py-2.5 text-[12.5px] text-muted">正在加载并绑定租户…</div>
            ) : account.eligibleTenants.length === 0 ? (
              <div className="rounded-[9px] border border-line2 bg-surface2 px-3 py-2.5 text-[12.5px] text-muted">
                {account.phase === 'error' ? '租户加载失败，请查看下方提示' : '当前账号没有可用的末级租户'}
              </div>
            ) : account.eligibleTenants.length === 1 ? (
              <div className="flex items-center justify-between rounded-[9px] border border-primary/25 bg-primarysoft px-3 py-2.5">
                <span className="min-w-0 truncate text-[13px] text-ink">
                  {account.eligibleTenants[0].name} <span className="font-mono text-[11px] text-muted">（{account.eligibleTenants[0].id}）</span>
                </span>
                <span className={`ml-3 shrink-0 text-[11.5px] ${tenantId === account.eligibleTenants[0].id ? 'text-primary' : 'text-muted'}`}>
                  {tenantId === account.eligibleTenants[0].id ? '✓ 已绑定' : account.phase === 'resolving' ? '正在绑定…' : '等待绑定'}
                </span>
              </div>
            ) : (
              <div className="max-h-[190px] overflow-y-auto rounded-[9px] border border-line2 bg-surface2 p-1.5 flex flex-col gap-0.5">
                <TenantTree
                  nodes={tenantTree}
                  selectableIds={eligibleTenantIds}
                  selectedId={tenantId}
                  disabled={account.phase === 'resolving' || starting}
                  onSelect={(tid) => void selectTenant(tid)}
                />
              </div>
            )}
          </div>
        )}
        <div className={LABEL_CLS}>工作区目录
          <div className="flex gap-2">
            <input className={`${FIELD_CLS} flex-1 min-w-0`} value={cwd} onChange={(e) => setCwd(e.target.value)} placeholder="C:\path\to\project" />
            <button type="button" className={`${BTN} shrink-0 px-3`} onClick={browse}>浏览…</button>
          </div>
          {recent.length > 0 && (
            <div className="flex flex-col gap-1 mt-0.5">
              <span className="text-[11px] text-muted">最近使用</span>
              <div className="flex flex-wrap gap-1.5">
                {recent.map((w) => (
                  <button
                    key={w}
                    type="button"
                    title={w}
                    onClick={() => setCwd(w)}
                    className="max-w-[220px] inline-flex items-center gap-1 px-2 py-1 rounded-[7px] border border-line2 bg-surface2 text-[11.5px] font-mono text-muted hover:border-primary hover:text-ink transition-colors"
                  >
                    <Icon name="folder" size={12} />
                    <span className="truncate">{shortenPath(w)}</span>
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
        <label className={LABEL_CLS}>模型（选定租户后可选；自定义模型在设置中配置）
          <select
            className={FIELD_CLS}
            value={modelChoice}
            disabled={account.phase === 'resolving' || (!tenantId && customModels.length === 0)}
            onChange={(e) => setModelChoice(e.target.value)}
          >
            <option value="" disabled>{account.phase === 'resolving' ? '正在加载租户和模型…' : tenantId || customModels.length > 0 ? '选择一个模型…' : account.eligibleTenants.length === 0 ? '当前账号没有可用租户' : '请先在上方选择租户'}</option>
            {tenantId && platformModels.length > 0 && (
              <optgroup label="平台模型（通行证）">
                {platformModels.map((m) => <option key={m.id} value={`passport:${m.id}`}>{m.id}（{m.ownedBy}）</option>)}
              </optgroup>
            )}
            {customModels.length > 0 && (
              <optgroup label="自定义模型">
                {customModels.map((m) => <option key={m.name} value={`custom:${m.name}`}>{m.name}（{customModelProviderLabel(m.provider)}）</option>)}
              </optgroup>
            )}
          </select>
        </label>
        {passport.loggedIn && passportError && <div className="text-red text-[12.5px]">{passportError}</div>}
        {error && <div className="text-red text-[13px]">{error}</div>}
        <button className={`${BTN} ${BTN_PRIMARY} py-3 text-[15px] mt-1.5`} disabled={!cwd.trim() || !buildRequest() || starting} onClick={() => {
          const req = buildRequest()
          if (!req) { setPassportError('请选择租户和模型'); return }
          onStart(req)
        }}>
          {starting ? '启动中…' : '开始会话'}
        </button>
      </div>
    </div>
  )
}
