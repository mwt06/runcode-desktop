// 设置页：按小节拆分——账号(通行证)、会话、自定义模型、联网工具代理、上下文长度。
// 拆分原则——进 saveSettings() 载荷的字段（会话/上下文）受控于 SettingsPage 本体；
// 各自直接落后端的小节（账号/代理/自定义模型草稿）状态内化，只把共享数据（平台
// 模型、自定义模型列表，二者合成模型候选）回流给父级。
import { useEffect, useState } from 'react'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { toModelOptions, type ModelOption } from '@/ui/model-picker'
import {
  errText, listCustomModels, saveSettings,
  type CustomModel, type PassportModel, type SessionInfo, type StartSessionRequest,
} from '@/core/bridge'
import { Toggle } from '@/ui/toggle'
import { Section } from './section'
import { AccountSection } from './account'
import { SessionSection } from './session'
import { CustomModelsSection } from './custom-models'
import { ProxySection } from './proxy'
import { ContextSection } from './context'
import { ContextAuditSection } from './context-audit'

export function SettingsPage({ initial, info, busy, onSaved, onSwitchModel }: {
  initial: Partial<StartSessionRequest>
  info: SessionInfo | null
  busy: boolean
  onSaved: (info: SessionInfo) => void
  // Switch the live session's model/connection in place (platform ↔ custom), the same
  // path the composer's in-chat picker uses — no new session, history preserved.
  onSwitchModel: (choice: ModelOption) => void
}) {
  // The session's model is now switched live (see 模型 field below), so it is read
  // straight from the running session, not a form field. Permission mode still prefers
  // the live session; connection settings come from the saved config.
  const [harmJudgeModel, setHarmJudgeModel] = useState(initial.harmJudgeModel ?? '')
  const [harmJudgeVotes, setHarmJudgeVotes] = useState(initial.harmJudgeVotes ?? 1)
  const [permissionMode, setPermissionMode] = useState(info?.permissionMode || initial.permissionMode || 'interactive')
  const [maxTokens, setMaxTokens] = useState(initial.maxTokens ? String(initial.maxTokens) : '')
  const [maxContextTokens, setMaxContextTokens] = useState(initial.maxContextTokens ?? 128000)
  const [maxHistoryMessages, setMaxHistoryMessages] = useState(initial.maxHistoryMessages ? String(initial.maxHistoryMessages) : '')
  // 免登录:关闭(默认)时启动强制走登录页;开启后未登录也可直接进表单用本地自定义模型。
  const [skipLogin, setSkipLogin] = useState(initial.skipLogin ?? false)
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
  // switchOpts drives the live 模型 switcher: platform + custom together, since
  // picking either now switches the connection in place (via SwitchModel). modelOpts
  // stays platform-only for the harm-judge field, which takes a bare model id the
  // engine resolves — a custom profile's display name is not such an id.
  const switchOpts = toModelOptions(platformModels, customModels)
  const modelOpts: ModelOption[] = platformModels.map((m): ModelOption => ({ id: m.id, label: m.id, sub: m.ownedBy, kind: 'platform' }))
  async function save() {
    setSaving(true)
    setSaved(false)
    setError('')
    try {
      const i = await saveSettings({
        cwd: info?.cwd ?? '',
        // The model/connection is switched live via onSwitchModel and persisted
        // backend-side; echo the current live model so saving other settings neither
        // clobbers nor reverts it (SaveSettings sees the same connection).
        model: info?.model ?? '',
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
        skipLogin,
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
          }}
          onBusy={setAccountBusy}
        />
        <Section title="登录" hint="保存后于下次启动/登出时生效">
          <label className="flex items-center justify-between gap-3 cursor-pointer">
            <span className="min-w-0">
              <span className="text-[13px]">免登录</span>
              <span className="block text-[12px] text-muted mt-0.5">关闭（默认）时启动强制登录通行证；开启后未登录也可直接进入并使用本地自定义模型。</span>
            </span>
            <Toggle on={skipLogin} onChange={setSkipLogin} />
          </label>
        </Section>
        <SessionSection
          currentModel={info?.model ?? ''}
          switchOpts={switchOpts}
          onSwitchModel={onSwitchModel}
          busy={busy}
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

        <ContextAuditSection />

        <Section title="工作区">
          <div className="font-mono text-[12.5px] text-muted break-all">{info?.cwd || '—'}</div>
        </Section>

        {error && <div className="text-red text-[13px]">{error}</div>}
        <div className="flex items-center gap-3 pb-2">
          <button className={`${BTN} ${BTN_PRIMARY} px-7 py-2.5`} disabled={saving || accountBusy || (!!activeTenantId && !accountReady)} onClick={save}>{saving ? '保存中…' : accountBusy ? '正在同步租户…' : '保存设置'}</button>
          {saved && <span className="text-green text-[13px]">✓ 已保存</span>}
        </div>
      </div>
    </div>
  )
}
