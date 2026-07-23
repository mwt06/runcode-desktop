// AgentDetail 是子代理的全屏详情/编辑页。内置只读(展示中文对照)。
import { useEffect, useState } from 'react'
import { BTN, BTN_PRIMARY, BTN_DANGER } from '@/ui/tokens'
import { FIELD_CLS, LABEL_CLS } from '@/ui/fields'
import { ModelSelect, type ModelOption } from '@/ui/model-picker'
import { deleteAgent, errText, listTools, saveAgent, sessionModels, type AgentInfo, type ToolInfo } from '@/core/bridge'
import { BUILTIN_AGENTS } from './builtin-agents'
import { ToolMultiSelect } from './tool-multi-select'

export function AgentDetail({ agent, onBack, onChanged, onUse }: {
  agent: AgentInfo | 'new'
  onBack: () => void
  onChanged: () => void
  onUse: (name: string) => void
}) {
  const isNew = agent === 'new'
  const ag = isNew ? null : agent
  const zh = ag && ag.source === 'builtin' ? BUILTIN_AGENTS[ag.name] : undefined
  const [name, setName] = useState(ag?.name ?? '')
  const [description, setDescription] = useState(zh?.desc ?? ag?.description ?? '')
  const [tools, setTools] = useState(ag?.tools ?? '')
  const [model, setModel] = useState(ag?.model ?? '')
  const [prompt, setPrompt] = useState(zh?.prompt ?? ag?.prompt ?? '')
  const [scope, setScope] = useState(isNew ? 'project' : ag?.source === 'user' ? 'user' : 'project')
  const editable = isNew || (ag?.editable ?? false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // 工具/模型的候选清单(进入详情页拉一次)。子代理的 model 只是当前连接上的
  // 模型 ID 覆盖，不能携带另一条自定义连接的 endpoint/密钥，因此这里只列平台模型。
  const [toolOptions, setToolOptions] = useState<ToolInfo[]>([])
  const [modelOptions, setModelOptions] = useState<ModelOption[]>([])
  useEffect(() => {
    listTools().then((l) => setToolOptions(l ?? [])).catch(() => {})
    sessionModels()
      .then((platform) => setModelOptions((platform ?? []).map((m): ModelOption => ({ kind: 'platform', id: m.id, label: m.id, sub: m.ownedBy }))))
      .catch(() => setModelOptions([]))
  }, [])

  async function save() {
    setBusy(true); setError('')
    try {
      await saveAgent({ originalName: ag?.name ?? '', name: name.trim(), description, tools, model, prompt, scope })
      onChanged(); onBack()
    } catch (e) { setError(errText(e)) } finally { setBusy(false) }
  }
  async function remove() {
    if (!ag) return
    setBusy(true); setError('')
    try { await deleteAgent(ag.name, ag.source); onChanged(); onBack() } catch (e) { setError(errText(e)) } finally { setBusy(false) }
  }

  return (
    <div className="flex-1 overflow-y-auto px-[30px] py-6">
      <div className="max-w-[720px] mx-auto flex flex-col gap-3.5">
        <button type="button" className="self-start text-[13px] text-muted hover:text-ink inline-flex items-center gap-1" onClick={onBack}>← 返回列表</button>
        {!editable && <div className="text-[12.5px] text-muted bg-surface2 border border-line2 rounded-[9px] px-3 py-2">内置子代理，只读。可「在对话中使用」，或在用户/项目级新建同名子代理来覆盖它。以下描述与指令正文为中文对照，模型收到的仍是原始定义。</div>}
        <label className={LABEL_CLS}>名称<input className={FIELD_CLS} value={name} disabled={!editable} onChange={(e) => setName(e.target.value)} placeholder="如 code-reviewer(字母、数字、- 或 _)" /></label>
        <label className={LABEL_CLS}>
          范围
          {isNew ? (
            <select className={FIELD_CLS} value={scope} onChange={(e) => setScope(e.target.value)}>
              <option value="project">项目(仅本工作区 .runcode/agents)</option>
              <option value="user">用户(全局，所有项目可用)</option>
            </select>
          ) : (
            <div className="text-[13px] text-ink bg-surface2 border border-line2 rounded-[9px] px-3 py-2.5">{scope === 'user' ? '用户(全局)' : scope === 'project' ? '项目(本工作区)' : '内置'}</div>
          )}
        </label>
        <label className={LABEL_CLS}>描述(一句话，告诉 AI 何时委派它)<input className={FIELD_CLS} value={description} disabled={!editable} onChange={(e) => setDescription(e.target.value)} placeholder="如 审查代码，找出 bug 与风险" /></label>
        <div className="grid grid-cols-2 gap-3">
          <div className={LABEL_CLS}>工具(可选;留空=继承全部)
            <ToolMultiSelect value={tools} options={toolOptions} onChange={setTools} disabled={!editable} />
          </div>
          <div className={LABEL_CLS}>模型(可选，覆盖默认)
            <ModelSelect value={model} options={modelOptions} onPick={setModel} placeholder="留空 = 继承会话模型" allowCustom clearLabel="留空 = 继承会话模型" disabled={!editable} />
          </div>
        </div>
        <label className={LABEL_CLS}>
          指令正文(Markdown，子代理的系统提示)
          <textarea className={`${FIELD_CLS} min-h-[280px] font-mono text-[13px] leading-[1.6] resize-y`} value={prompt} disabled={!editable} onChange={(e) => setPrompt(e.target.value)} />
        </label>
        {error && <div className="text-[12.5px] text-red">{error}</div>}
        <div className="flex gap-2.5">
          {editable && <button className={`${BTN} ${BTN_PRIMARY}`} disabled={busy || !name.trim()} onClick={save}>{busy ? '保存中…' : '保存'}</button>}
          {!isNew && <button className={BTN} onClick={() => onUse(ag!.name)}>在对话中使用</button>}
          {!isNew && editable && <button className={`${BTN} ${BTN_DANGER}`} disabled={busy} onClick={remove}>删除</button>}
        </div>
      </div>
    </div>
  )
}
