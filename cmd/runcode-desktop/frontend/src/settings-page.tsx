// settings-page 是「设置」页，按小节拆分：账号(通行证)、会话、自定义模型、
// 联网工具代理、上下文长度。拆分原则——进 saveSettings() 载荷的字段（会话/上下文）
// 受控于 SettingsPage 本体；各自直接落后端的小节（账号/代理/自定义模型草稿）状态
// 内化，只把共享数据（平台模型、自定义模型列表，二者合成模型候选）回流给父级。
// 从 pages.tsx 搬出重组，行为不变。
import { useEffect, useRef, useState } from 'react'
import { Icon } from '@/ui/icons'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { FIELD_CLS, LABEL_CLS, SelectField } from '@/ui/fields'
import { ModelSelect, type ModelOption } from '@/ui/model-picker'
import {
  customModelDraftForEdit,
  emptyCustomModelDraft,
  customModelProvider,
  customModelProviderLabel,
  toCustomModelSaveRequest,
  type CustomModelDraft,
} from '@/core/custom-models'
import {
  createPassportAccountCoordinator,
  initialPassportAccountSnapshot,
  type PassportAccountCoordinator,
} from '@/core/passport-account'
import {
  saveSettings,
  passportStatus, passportLogin, passportCancelLogin, passportLogout, passportModels, passportTenants,
  setActiveTenant, activeTenant,
  listCustomModels, saveCustomModel, deleteCustomModel,
  webProxy, setWebProxy,
  onEvent, Events,
  type SessionInfo, type StartSessionRequest,
  type PassportModel, type CustomModel,
  errText,
} from '@/core/bridge'

// 统一的小节卡片外壳，保持原有视觉不变。
function Section({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
      {hint ? (
        <div className="flex items-center justify-between">
          <div className="text-[13px] font-semibold text-ink">{title}</div>
          <span className="text-[11.5px] text-faint">{hint}</span>
        </div>
      ) : (
        <div className="text-[13px] font-semibold text-ink">{title}</div>
      )}
      {children}
    </section>
  )
}

// 账号：登录态 + 租户切换。登录/登出/切租户直接落后端；租户的平台模型列表经
// onPlatformModels 回流父级（与自定义模型合成会话/判定模型的候选）。
function AccountSection({ onAccount, onBusy }: {
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
        <div className="flex items-center justify-between rounded-[9px] border border-line2 bg-surface2 px-3 py-2.5">
          <span className="text-[13px]">已登录：<b>{passport.name || passport.userName || passport.userId}</b></span>
          <button type="button" className="text-[12.5px] text-muted hover:text-red" onClick={() => { void passportLogout() }}>登出</button>
        </div>
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
        <label className={LABEL_CLS}>租户(切换后下次新建会话生效)
          <SelectField value={account.tenantId} disabled={account.phase === 'resolving'} onChange={(v) => void onSwitchTenant(v)}>
            {!account.tenantId && account.eligibleTenants.length > 1 && <option value="">请选择末级租户</option>}
            {account.eligibleTenants.map((tenant) => <option key={tenant.id} value={tenant.id}>{tenant.name}（{tenant.id}）</option>)}
          </SelectField>
        </label>
      )}
      {acctMsg && <div className="text-red text-[12.5px]">{acctMsg}</div>}
    </Section>
  )
}

// 会话：模型/权限模式/判定模型/判定表决——全部进 saveSettings() 载荷，受控于父级。
function SessionSection({ model, onModel, permissionMode, onPermissionMode, harmJudgeModel, onHarmJudgeModel, harmJudgeVotes, onHarmJudgeVotes, modelOpts }: {
  model: string
  onModel: (v: string) => void
  permissionMode: string
  onPermissionMode: (v: string) => void
  harmJudgeModel: string
  onHarmJudgeModel: (v: string) => void
  harmJudgeVotes: number
  onHarmJudgeVotes: (v: number) => void
  modelOpts: ModelOption[]
}) {
  return (
    <Section title="会话">
      <div className={LABEL_CLS}>模型
        <ModelSelect value={model} options={modelOpts} onPick={onModel} placeholder="选择或搜索模型…" allowCustom />
      </div>
      <label className={LABEL_CLS}>权限模式
        <SelectField value={permissionMode} onChange={onPermissionMode}>
          <option value="interactive">交互（逐项询问）</option>
          <option value="judge">智能（模型审查命令）</option>
          <option value="safe">安全（拒绝高危）</option>
          <option value="flight">飞行（不审计，全部放行）</option>
        </SelectField>
      </label>
      {permissionMode === 'flight' && (
        <div className="flex items-start gap-2 bg-redbg border border-[rgba(224,86,74,0.35)] rounded-lg px-3 py-2.5 text-[12.5px] text-red">
          <span className="flex-none mt-px"><Icon name="shield" size={15} /></span>
          <span>飞行模式会<b>放行一切操作</b>（含删除文件、sudo 等高危命令），不再询问也不做模型审查。仅在完全信任的环境使用。</span>
        </div>
      )}
      <div className={LABEL_CLS}>判定模型（智能模式的安全判定；留空 = 独立默认，与主模型解耦）
        <ModelSelect value={harmJudgeModel} options={modelOpts} onPick={onHarmJudgeModel} placeholder="留空 = 默认独立模型（如 claude-haiku-4-5）" allowCustom clearLabel="留空 = 默认独立模型" />
      </div>
      <label className={LABEL_CLS}>判定表决（多次独立判定取多数，更稳但更费 token）
        <SelectField value={String(harmJudgeVotes)} onChange={(v) => onHarmJudgeVotes(parseInt(v, 10))}>
          <option value="1">单次（默认）</option>
          <option value="3">3 次取多数</option>
          <option value="5">5 次取多数</option>
        </SelectField>
      </label>
    </Section>
  )
}

// 自定义模型（直连接入点）：列表由父级持有（要合入模型候选），表单同时承担
// 新增和编辑。编辑时 originalName 定位旧记录，允许改显示名而不留下重复项。
export function CustomModelsSection({ models, onChanged }: { models: CustomModel[]; onChanged: (list: CustomModel[]) => void }) {
  const [editing, setEditing] = useState<CustomModel | null>(null)
  const [draft, setDraft] = useState<CustomModelDraft>(emptyCustomModelDraft)
  const [cmError, setCmError] = useState('')
  const [cmSaving, setCmSaving] = useState(false)

  const patchDraft = (patch: Partial<CustomModelDraft>) => setDraft((current) => ({ ...current, ...patch }))
  const resetForm = () => {
    setEditing(null)
    setDraft(emptyCustomModelDraft())
    setCmError('')
  }
  const beginEdit = (m: CustomModel) => {
    setEditing(m)
    setDraft(customModelDraftForEdit(m))
    setCmError('')
  }
  const saveModel = async () => {
    setCmSaving(true)
    setCmError('')
    try {
      const list = await saveCustomModel(toCustomModelSaveRequest(draft, editing?.name))
      onChanged(list ?? [])
      resetForm()
    } catch (e) {
      setCmError(errText(e))
    } finally {
      setCmSaving(false)
    }
  }

  return (
    <Section title="自定义模型" hint="直连接入点，开始页可选">
      <p className="text-[12px] text-muted -mt-1.5">除通行证平台模型外，可添加 OpenAI 兼容或 Anthropic 接入点（各自带 Base URL 与密钥）。</p>
      {models.length > 0 && (
        <div className="flex flex-col gap-1.5">
          {models.map((m) => (
            <div key={m.name} className={`flex items-center justify-between rounded-[9px] border bg-surface2 px-3 py-2 text-[12.5px] ${editing?.name === m.name ? 'border-primary' : 'border-line2'}`}>
              <span className="truncate">
                {m.name}
                <span className="text-muted"> · {customModelProviderLabel(m.provider)} · {m.model}</span>
                <span className="text-faint font-mono text-[11px]"> {m.baseURL}</span>
              </span>
              <span className="flex items-center gap-2 flex-none ml-2">
                <button type="button" className="text-muted hover:text-primaryink" onClick={() => beginEdit(m)}>编辑</button>
                <button type="button" className="text-muted hover:text-red" onClick={async () => {
                  setCmError('')
                  try {
                    onChanged((await deleteCustomModel(m.name)) ?? [])
                    if (editing?.name === m.name) resetForm()
                  } catch (e) {
                    setCmError(errText(e))
                  }
                }}>删除</button>
              </span>
            </div>
          ))}
        </div>
      )}
      <div className="flex flex-col gap-2 rounded-[11px] border border-dashed border-line2 p-3">
        <div className="flex items-center justify-between">
          <span className="text-[12px] font-medium text-ink">{editing?.name ? `编辑：${editing?.name}` : '添加模型'}</span>
          {editing?.name && <button type="button" className="text-[12px] text-muted hover:text-ink" onClick={resetForm}>取消编辑</button>}
        </div>
        <div className="grid grid-cols-2 gap-2">
          <input className={FIELD_CLS} placeholder="显示名称（如 本地 Ollama）" value={draft.name} onChange={(e) => patchDraft({ name: e.target.value })} />
          <SelectField value={draft.provider} onChange={(v) => patchDraft({ provider: customModelProvider(v) })}>
            <option value="openai">OpenAI 兼容</option>
            <option value="anthropic">Anthropic</option>
          </SelectField>
        </div>
        <div className="grid grid-cols-2 gap-2">
          <input className={FIELD_CLS} placeholder="模型 ID" value={draft.model} onChange={(e) => patchDraft({ model: e.target.value })} />
          <input className={FIELD_CLS} placeholder={draft.provider === 'anthropic' ? 'Base URL（留空使用默认）' : 'Base URL（.../v1）'} value={draft.baseURL} onChange={(e) => patchDraft({ baseURL: e.target.value })} />
        </div>
        <input
          className={FIELD_CLS}
          type="password"
          disabled={draft.clearAPIKey}
          placeholder={editing ? (editing.hasAPIKey ? 'API 密钥（已保存；留空保留，填写则替换）' : 'API 密钥（未配置；可留空）') : 'API 密钥（可空）'}
          value={draft.apiKey}
          onChange={(e) => patchDraft({ apiKey: e.target.value })}
        />
        {editing?.hasAPIKey && (
          <label className="flex items-center gap-2 text-[12px] text-muted">
            <input
              type="checkbox"
              checked={draft.clearAPIKey}
              onChange={(e) => patchDraft({ clearAPIKey: e.target.checked, ...(e.target.checked ? { apiKey: '' } : {}) })}
            />
            清除已保存的 API 密钥
          </label>
        )}
        {cmError && <div className="text-red text-[12px]">{cmError}</div>}
        <div className="flex items-center gap-2">
          <button type="button" className={`${BTN} px-5`} disabled={cmSaving || !draft.name.trim() || !draft.model.trim()} onClick={() => void saveModel()}>
            {cmSaving ? '保存中…' : editing?.name ? '保存修改' : '添加自定义模型'}
          </button>
          {editing?.name && <button type="button" className={BTN} onClick={resetForm}>取消</button>}
        </div>
      </div>
    </Section>
  )
}

// 联网工具(WebSearch/WebFetch)代理：与模型出口无关，读写都直接落后端，完全自包含。
function ProxySection() {
  const [proxy, setProxy] = useState('')
  const [proxyMsg, setProxyMsg] = useState('')
  useEffect(() => {
    webProxy().then((p) => setProxy(p ?? '')).catch(() => {})
  }, [])
  return (
    <Section title="联网工具代理" hint="仅 WebSearch / WebFetch">
      <p className="text-[12px] text-muted -mt-1.5">
        联网搜索走 DuckDuckGo，直连不通时可在此填代理。<b>只影响联网工具</b>，不改变模型 API 与通行证的出口。留空为直连。
      </p>
      <div className="flex gap-2">
        <input
          className={`${FIELD_CLS} flex-1`}
          placeholder="如 127.0.0.1:7890（可省略 http://，支持 socks5://）"
          value={proxy}
          onChange={(e) => { setProxy(e.target.value); setProxyMsg('') }}
        />
        <button type="button" className={`${BTN} px-5 flex-none`} onClick={async () => {
          setProxyMsg('')
          try {
            const norm = await setWebProxy(proxy)
            setProxy(norm ?? '')
            setProxyMsg(norm ? `已保存：${norm}（新建会话后生效）` : '已清除，联网工具将直连')
          } catch (e) {
            setProxyMsg(errText(e))
          }
        }}>保存</button>
      </div>
      {proxyMsg && <div className="text-[12px] text-muted -mt-1">{proxyMsg}</div>}
      <p className="text-[11.5px] text-faint -mt-1">
        出于安全，联网工具始终拒绝访问内网/回环地址(如 127.0.0.1、192.168.*、169.254.169.254)，配了代理也一样。
      </p>
    </Section>
  )
}

// 上下文长度控制：三个字段都进 saveSettings() 载荷，受控于父级。
function ContextSection({ maxTokens, onMaxTokens, maxContextTokens, onMaxContextTokens, maxHistoryMessages, onMaxHistoryMessages }: {
  maxTokens: string
  onMaxTokens: (v: string) => void
  maxContextTokens: number
  onMaxContextTokens: (v: number) => void
  maxHistoryMessages: string
  onMaxHistoryMessages: (v: string) => void
}) {
  return (
    <Section title="上下文长度控制" hint="下次新建会话生效">
      <label className={LABEL_CLS}>最大输出 Tokens<input className={FIELD_CLS} type="number" value={maxTokens} onChange={(e) => onMaxTokens(e.target.value)} placeholder="留空则用默认 16384" /></label>
      <label className={LABEL_CLS}>上下文预算（超出后自动总结压缩较早对话；磁盘记录保持完整）
        <SelectField value={String(maxContextTokens)} onChange={(v) => onMaxContextTokens(parseInt(v, 10))}>
          <option value="0">关闭 · 不自动压缩</option>
          <option value="32000">32K · 省 token</option>
          <option value="128000">128K · 推荐</option>
          <option value="200000">200K · 大窗口</option>
        </SelectField>
      </label>
      <label className={LABEL_CLS}>历史消息上限（硬截断，仅保留最近 N 条；留空关闭）
        <input className={FIELD_CLS} type="number" value={maxHistoryMessages} onChange={(e) => onMaxHistoryMessages(e.target.value)} placeholder="留空 = 不截断（推荐优先用上面的自动压缩）" />
      </label>
    </Section>
  )
}

export function SettingsPage({ initial, info, onSaved }: { initial: Partial<StartSessionRequest>; info: SessionInfo | null; onSaved: (info: SessionInfo) => void }) {
  // Model and permission mode prefer the live session (they can change at runtime);
  // connection settings come from the saved config.
  const [model, setModel] = useState(info?.model || initial.model || '')
  const [harmJudgeModel, setHarmJudgeModel] = useState(initial.harmJudgeModel ?? '')
  const [harmJudgeVotes, setHarmJudgeVotes] = useState(initial.harmJudgeVotes ?? 1)
  const [permissionMode, setPermissionMode] = useState(info?.permissionMode || initial.permissionMode || 'interactive')
  const [maxTokens, setMaxTokens] = useState(initial.maxTokens ? String(initial.maxTokens) : '')
  const [maxContextTokens, setMaxContextTokens] = useState(initial.maxContextTokens ?? 128000)
  const [maxHistoryMessages, setMaxHistoryMessages] = useState(initial.maxHistoryMessages ? String(initial.maxHistoryMessages) : '')
  const [saving, setSaving] = useState(false)
  const [accountBusy, setAccountBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState('')
  // 共享数据：平台模型（账号小节回流）+ 自定义模型列表（自定义模型小节维护），
  // 合成会话/判定模型选择器共用的候选列表。
  const [platformModels, setPlatformModels] = useState<PassportModel[]>([])
  const [activeTenantId, setActiveTenantId] = useState(initial.tenantId ?? '')
  const [accountReady, setAccountReady] = useState(false)
  const [customModels, setCustomModels] = useState<CustomModel[]>([])
  useEffect(() => {
    listCustomModels().then((l) => setCustomModels(l ?? [])).catch(() => {})
  }, [])
  // This form's model fields are plain IDs on the current connection. A custom
  // profile also owns provider/Base URL/credentials, so treating it as only m.model
  // would route requests through the wrong endpoint. Custom profiles stay in the
  // start page and in-chat SwitchModel picker, both of which switch the connection.
  const modelOpts: ModelOption[] = platformModels.map((m): ModelOption => ({ id: m.id, label: m.id, sub: m.ownedBy, kind: 'platform' }))
  async function save() {
    setSaving(true)
    setSaved(false)
    setError('')
    try {
      const i = await saveSettings({
        cwd: info?.cwd ?? '',
        model,
        // provider/baseURL/apiKey 不在设置里编辑（通行证会话自动接线、自定义模型
        // 各自带连接）；原样透传避免保存设置时改动会话接线。
        provider: initial.provider ?? '',
        baseURL: initial.baseURL ?? '',
        apiKey: '',
        permissionMode,
        maxTokens: maxTokens.trim() ? parseInt(maxTokens, 10) || 0 : 0,
        maxContextTokens,
        maxHistoryMessages: maxHistoryMessages.trim() ? parseInt(maxHistoryMessages, 10) || 0 : 0,
        harmJudgeModel,
        harmJudgeVotes,
        // Preserved (edited via the in-conversation picker, not this form) so saving
        // connection settings does not silently reset the reasoning strength.
        thinkingEffort: initial.thinkingEffort ?? '',
        // 本表单不涉及的字段按 wire 零值发送 —— 与旧版直接省略这些键时 Go 端
        // json 反序列化得到的零值完全一致（生成的 StartSessionRequest 为全量必填）。
        customModelName: initial.customModelName ?? '',
        tenantId: activeTenantId,
        authToken: '',
        reasoningScenario: '',
        resume: '',
        continue: false,
      })
      if (i && i.model) onSaved(i)
      setSaved(true)
      setTimeout(() => setSaved(false), 2200)
    } catch (e) {
      setError(errText(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex-1 overflow-y-auto px-[22px] py-7">
      <div className="max-w-[640px] mx-auto flex flex-col gap-5">
        <div>
          <h2 className="m-0 text-[20px] font-bold tracking-tight">设置</h2>
          <p className="mt-1 text-muted text-[13px]">模型与权限模式即时生效；连接设置在下次新建会话时生效。</p>
        </div>

        <AccountSection
          onAccount={(tenantId, models, ready) => {
            setActiveTenantId(tenantId)
            setPlatformModels(models)
            setAccountReady(ready)
            if (ready) {
              setModel((current) => models.some((candidate) => candidate.id === current) ? current : '')
            }
          }}
          onBusy={setAccountBusy}
        />
        <SessionSection
          model={model}
          onModel={setModel}
          permissionMode={permissionMode}
          onPermissionMode={setPermissionMode}
          harmJudgeModel={harmJudgeModel}
          onHarmJudgeModel={setHarmJudgeModel}
          harmJudgeVotes={harmJudgeVotes}
          onHarmJudgeVotes={setHarmJudgeVotes}
          modelOpts={modelOpts}
        />
        <CustomModelsSection models={customModels} onChanged={setCustomModels} />
        <ProxySection />
        <ContextSection
          maxTokens={maxTokens}
          onMaxTokens={setMaxTokens}
          maxContextTokens={maxContextTokens}
          onMaxContextTokens={setMaxContextTokens}
          maxHistoryMessages={maxHistoryMessages}
          onMaxHistoryMessages={setMaxHistoryMessages}
        />

        <Section title="工作区">
          <div className="font-mono text-[12.5px] text-muted break-all">{info?.cwd || '—'}</div>
        </Section>

        {error && <div className="text-red text-[13px]">{error}</div>}
        <div className="flex items-center gap-3 pb-2">
          <button className={`${BTN} ${BTN_PRIMARY} px-7 py-2.5`} disabled={saving || accountBusy || (!!activeTenantId && (!accountReady || !model))} onClick={save}>{saving ? '保存中…' : accountBusy ? '正在同步租户…' : '保存设置'}</button>
          {saved && <span className="text-green text-[13px]">✓ 已保存</span>}
        </div>
      </div>
    </div>
  )
}
