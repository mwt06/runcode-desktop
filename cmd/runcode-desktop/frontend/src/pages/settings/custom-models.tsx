// 自定义模型（直连接入点）：列表由父级持有（要合入模型候选），表单同时承担
// 新增和编辑。编辑时 originalName 定位旧记录，允许改显示名而不留下重复项。
// 起始页(未登录时)也复用本小节，让没有通行证的用户先配一个直连模型。
import { useEffect, useRef, useState, type FocusEvent } from 'react'
import { BTN } from '@/ui/tokens'
import { FIELD_CLS, SelectField } from '@/ui/fields'
import {
  CUSTOM_MODEL_PROVIDERS,
  codexAuthMode,
  customModelBaseURLHint,
  customModelDraftForEdit,
  customModelProvider,
  customModelProviderLabel,
  emptyCustomModelDraft,
  toCustomModelSaveRequest,
  usesChatGPTLogin,
  type CustomModelDraft,
} from '@/core/custom-models'
import { deleteCustomModel, errText, saveCustomModel, type CodexModel, type CustomModel } from '@/core/bridge'
import { imageCapabilityLabel, imageCapabilityValue } from '@/core/vision'
import { Section } from './section'
import { CodexLoginRow } from './codex-login'
import { InlineError } from '@/ui/feedback'
import { createAutoSaveChannel, type AutoSaveChannel } from './autosave-channel'
import { useSettingsSaveRegistry } from './settings-save-provider'

export function CustomModelsSection({ models, onChanged }: { models: CustomModel[]; onChanged: (list: CustomModel[]) => void }) {
  const registry = useSettingsSaveRegistry()
  const [editing, setEditing] = useState<CustomModel | null>(null)
  const [draft, setDraft] = useState<CustomModelDraft>(emptyCustomModelDraft)
  const [cmError, setCmError] = useState('')
  const [cmSaving, setCmSaving] = useState(false)
  const channelRef = useRef<{
    key: string
    channel: AutoSaveChannel<CustomModelDraft>
    unregister: () => void
  } | null>(null)
  const originalNameRef = useRef('')
  const activeKeyRef = useRef('')
  const onChangedRef = useRef(onChanged)
  onChangedRef.current = onChanged
  // 上游给出的可用模型（登录后由 CodexLoginRow 报上来）。ChatGPT 账号能用哪些
  // 模型按订阅档次变化，本地写死一张表只会让人填到一个上游不认的名字。
  const [codexOptions, setCodexOptions] = useState<CodexModel[]>([])

  const patchDraft = (patch: Partial<CustomModelDraft>) => setDraft((current) => {
    const next = { ...current, ...patch }
    if (editing && channelRef.current) channelRef.current.channel.edit(next, 'deferred')
    return next
  })
  // 走 ChatGPT 订阅时端点与凭据都不由用户提供，表单相应收起两栏。
  const chatgptLogin = usesChatGPTLogin(draft)
  const resetForm = () => {
    channelRef.current?.channel.discard()
    channelRef.current?.unregister()
    setEditing(null)
    setDraft(emptyCustomModelDraft())
    setCmError('')
    channelRef.current = null
    activeKeyRef.current = ''
    originalNameRef.current = ''
  }
  const beginEdit = (m: CustomModel) => {
    channelRef.current?.channel.discard()
    channelRef.current?.unregister()
    const initial = customModelDraftForEdit(m)
    const key = `custom-model:${m.name}`
    const channel = createAutoSaveChannel<CustomModelDraft>({
      initial,
      delayMs: 600,
      validate: (value) => !value.name.trim() ? '名称不能为空' : !value.model.trim() ? '模型 ID 不能为空' : null,
      write: async (value) => {
        const list = await saveCustomModel(toCustomModelSaveRequest(value, originalNameRef.current))
        if (activeKeyRef.current !== key) return value
        onChangedRef.current(list ?? [])
        originalNameRef.current = value.name.trim()
        setEditing((current) => current ? { ...current, name: originalNameRef.current } : current)
        return value
      },
    })
    const unregister = registry.register(key, '自定义模型', channel)
    channelRef.current = { key, channel, unregister }
    originalNameRef.current = m.name
    activeKeyRef.current = key
    setEditing(m)
    setDraft(initial)
    setCmError('')
  }
  const addModel = async () => {
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
  useEffect(() => {
    const channel = channelRef.current?.channel
    if (!channel) return
    const sync = () => {
      const snapshot = channel.getSnapshot()
      setCmSaving(snapshot.phase === 'saving')
      setCmError(snapshot.phase === 'error' || snapshot.phase === 'invalid' ? snapshot.error : '')
    }
    sync()
    return channel.subscribe(sync)
  }, [editing?.name])
  const finishEdit = async () => {
    const channel = channelRef.current?.channel
    if (!channel || await channel.flush()) resetForm()
  }
  const handleFormBlur = (event: FocusEvent<HTMLDivElement>) => {
    if (!editing) return
    const next = event.relatedTarget
    if (next instanceof Node && event.currentTarget.contains(next)) return
    if (next === null) {
      const form = event.currentTarget
      queueMicrotask(() => {
        const active = document.activeElement
        if (active instanceof Node && form.contains(active)) return
        void channelRef.current?.channel.flush()
      })
      return
    }
    void channelRef.current?.channel.flush()
  }

  return (
    <Section title="自定义模型" hint="直连接入点，开始页可选">
      <p className="text-[12px] text-muted -mt-1.5">除通行证平台模型外，可添加 OpenAI 兼容、Anthropic 或 Codex 接入点（各自带 Base URL 与密钥）。删除默认识图模型后需重新选择兜底。</p>
      <p className="text-[12px] text-faint -mt-1">
        OpenAI 有两套协议：绝大多数网关走「OpenAI 兼容」（<span className="font-mono">/chat/completions</span>），少数端点和较新的推理模型只提供「OpenAI Responses」（<span className="font-mono">/responses</span>）。若报 404 或提示模型不支持，换另一个试试。
      </p>
      {models.length > 0 && (
        <div className="flex flex-col gap-1.5">
          {models.map((m) => (
            <div key={m.name} className={`flex items-center justify-between rounded-field border bg-surface2 px-3 py-2 text-[13px] ${editing?.name === m.name ? 'border-primary' : 'border-line2'}`}>
              <span className="truncate">
                {m.name} · {imageCapabilityLabel(m.supportsImages)}
                <span className="text-muted"> · {customModelProviderLabel(m.provider)} · {m.model}</span>
                <span className="text-faint font-mono text-[11px]"> {m.baseURL}</span>
              </span>
              <span className="flex items-center gap-2 flex-none ml-2">
                <button type="button" className="text-muted hover:text-primaryink" onClick={() => beginEdit(m)}>编辑</button>
                <button type="button" className="text-muted hover:text-red disabled:opacity-40" disabled={editing?.name === m.name && cmSaving} onClick={async () => {
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
      <div className="custom-model-form flex flex-col gap-2 rounded-btn border border-dashed border-line2 p-3" onBlur={handleFormBlur}>
        <div className="flex items-center justify-between">
          <span className="text-[12px] font-medium text-ink">{editing?.name ? `编辑：${editing?.name}` : '添加模型'}</span>
          {editing?.name && <button type="button" className="text-[12px] text-muted hover:text-ink" disabled={cmSaving} onClick={() => void finishEdit()}>收起编辑</button>}
        </div>
        <div className="grid grid-cols-2 gap-2">
          <input className={FIELD_CLS} placeholder="显示名称（如 本地 Ollama）" value={draft.name} onChange={(e) => patchDraft({ name: e.target.value })} />
          <SelectField value={draft.provider} onChange={(v) => patchDraft({ provider: customModelProvider(v) })}>
            {CUSTOM_MODEL_PROVIDERS.map((p) => <option key={p} value={p}>{customModelProviderLabel(p)}</option>)}
          </SelectField>
        </div>
        <label className="flex flex-col gap-1 text-[12px] text-muted">
          图片能力
          <SelectField value={draft.supportsImages == null ? 'unknown' : String(draft.supportsImages)} onChange={(v) => patchDraft({ supportsImages: imageCapabilityValue(v) })}>
            <option value="unknown">未标注 · 保留原图直传</option>
            <option value="true">支持图片 · 直接交给此模型</option>
            <option value="false">仅文本 · 使用默认识图模型</option>
          </SelectField>
        </label>
        {draft.provider === 'codex'  && (
          <SelectField value={draft.authMode} onChange={(v) => patchDraft({ authMode: codexAuthMode(v) })}>
            <option value="chatgpt">用 ChatGPT 订阅登录</option>
            <option value="apikey">用 API 密钥连第三方 Codex 中转</option>
          </SelectField>
        )}
        {chatgptLogin && <CodexLoginRow onModels={setCodexOptions} />}
        <div className={chatgptLogin ? '' : 'grid grid-cols-2 gap-2'}>
          {chatgptLogin && codexOptions.length > 0 ? (
            <SelectField value={draft.model} onChange={(v) => patchDraft({ model: v })}>
              <option value="">选择模型…</option>
              {codexOptions.map((m) => <option key={m.id} value={m.id}>{m.displayName ? `${m.displayName}（${m.id}）` : m.id}</option>)}
            </SelectField>
          ) : (
            <input className={FIELD_CLS} placeholder={chatgptLogin ? '模型 ID（登录后可从清单选择）' : '模型 ID'} value={draft.model} onChange={(e) => patchDraft({ model: e.target.value })} />
          )}
          {/* 走 ChatGPT 登录时端点固定、凭据来自登录，这两栏留着只会让人以为要填。 */}
          {!chatgptLogin && <input className={FIELD_CLS} placeholder={customModelBaseURLHint(draft.provider)} value={draft.baseURL} onChange={(e) => patchDraft({ baseURL: e.target.value })} />}
        </div>
        {!chatgptLogin && (
          <input
            className={FIELD_CLS}
            type="password"
            disabled={draft.clearAPIKey}
            placeholder={editing ? (editing.hasAPIKey ? 'API 密钥（已保存；留空保留，填写则替换）' : 'API 密钥（未配置；可留空）') : 'API 密钥（可空）'}
            value={draft.apiKey}
            onChange={(e) => patchDraft({ apiKey: e.target.value })}
          />
        )}
        {!chatgptLogin && editing?.hasAPIKey && (
          <label className="flex items-center gap-2 text-[12px] text-muted">
            <input
              type="checkbox"
              checked={draft.clearAPIKey}
              onChange={(e) => patchDraft({ clearAPIKey: e.target.checked, ...(e.target.checked ? { apiKey: '' } : {}) })}
            />
            清除已保存的 API 密钥
          </label>
        )}
        {cmError && <InlineError variant="text">{cmError}</InlineError>}
        {!editing && (
          <div className="flex items-center gap-2">
            <button type="button" className={`${BTN} px-5`} disabled={cmSaving || !draft.name.trim() || !draft.model.trim()} onClick={() => void addModel()}>
              {cmSaving ? '保存中…' : '添加自定义模型'}
            </button>
          </div>
        )}
        {editing && (cmSaving || cmError) && <p className="text-[12px] text-muted">{cmSaving ? '正在自动保存…' : '修改尚未保存'}</p>}
      </div>
    </Section>
  )
}
