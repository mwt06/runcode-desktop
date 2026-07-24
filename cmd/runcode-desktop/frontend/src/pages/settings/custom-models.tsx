// 自定义模型（直连接入点）：列表由父级持有（要合入模型候选），表单同时承担
// 新增和编辑。编辑时 originalName 定位旧记录，允许改显示名而不留下重复项。
// 起始页(未登录时)也复用本小节，让没有通行证的用户先配一个直连模型。
import { useState } from 'react'
import { BTN } from '@/ui/tokens'
import { FIELD_CLS, SelectField } from '@/ui/fields'
import {
  CUSTOM_MODEL_PROVIDERS,
  customModelBaseURLHint,
  customModelDraftForEdit,
  customModelProvider,
  customModelProviderLabel,
  emptyCustomModelDraft,
  toCustomModelSaveRequest,
  type CustomModelDraft,
} from '@/core/custom-models'
import { deleteCustomModel, errText, saveCustomModel, type CustomModel } from '@/core/bridge'
import { Section } from './section'

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
      <p className="text-[12px] text-faint -mt-1">
        OpenAI 有两套协议：绝大多数网关走「OpenAI 兼容」（<span className="font-mono">/chat/completions</span>），少数端点和较新的推理模型只提供「OpenAI Responses」（<span className="font-mono">/responses</span>）。若报 404 或提示模型不支持，换另一个试试。
      </p>
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
            {CUSTOM_MODEL_PROVIDERS.map((p) => <option key={p} value={p}>{customModelProviderLabel(p)}</option>)}
          </SelectField>
        </div>
        <div className="grid grid-cols-2 gap-2">
          <input className={FIELD_CLS} placeholder="模型 ID" value={draft.model} onChange={(e) => patchDraft({ model: e.target.value })} />
          <input className={FIELD_CLS} placeholder={customModelBaseURLHint(draft.provider)} value={draft.baseURL} onChange={(e) => patchDraft({ baseURL: e.target.value })} />
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
