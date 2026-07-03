// Full-screen pages rendered by the app shell: the skills / agents / settings
// managers and the initial start form. Extracted from App.tsx to keep it focused.
import { useEffect, useState } from 'react'
import { Icon, Logo } from './icons'
import { Markdown } from './markdown'
import { BTN, BTN_PRIMARY, BTN_DANGER } from './ui'
import {
  listSkills, saveSkill, deleteSkill, importSkill,
  listAgents, saveAgent, deleteAgent, importAgent,
  saveSettings,
  type SkillInfo, type SkillList,
  type AgentInfo, type AgentList,
  type SessionInfo, type StartSessionRequest,
} from './bridge'

export function SkillsPage({ onUse }: { onUse: (name: string) => void }) {
  const [list, setList] = useState<SkillList>({ skills: [], problems: [] })
  const [sel, setSel] = useState<string | 'new' | null>(null)
  const [name, setName] = useState('')
  const [originalName, setOriginalName] = useState('')
  const [description, setDescription] = useState('')
  const [body, setBody] = useState('')
  const [scope, setScope] = useState('project')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const field = 'font-sans text-[14px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2.5 outline-none focus:border-primary focus:shadow-[0_0_0_3px_var(--color-primarysoft)]'
  const label = 'flex flex-col gap-1.5 text-[12.5px] text-muted'

  function refresh() {
    listSkills()
      .then((l) => setList(l ?? { skills: [], problems: [] }))
      .catch((e) => setError(String(e)))
  }
  useEffect(() => {
    refresh()
  }, [])

  function edit(sk: SkillInfo) {
    setSel(sk.name)
    setName(sk.name)
    setOriginalName(sk.name)
    setDescription(sk.description)
    setBody(sk.body)
    setScope(sk.source === 'user' ? 'user' : 'project')
    setError('')
  }
  function startNew() {
    setSel('new')
    setName('')
    setOriginalName('')
    setDescription('')
    setBody('')
    setScope('project')
    setError('')
  }
  async function save() {
    setBusy(true)
    setError('')
    try {
      const l = await saveSkill({ originalName, name: name.trim(), description, body, scope })
      setList(l)
      setSel(name.trim())
      setOriginalName(name.trim())
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }
  async function remove() {
    if (originalName === '') return
    setBusy(true)
    try {
      const l = await deleteSkill(originalName, scope)
      setList(l)
      setSel(null)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }
  // Delete straight from a list row (its own scope), without opening the editor.
  async function removeFromList(sk: SkillInfo) {
    setBusy(true)
    setError('')
    try {
      const l = await deleteSkill(sk.name, sk.source)
      setList(l)
      if (originalName === sk.name) setSel(null)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }
  async function importExisting() {
    setBusy(true)
    setError('')
    try {
      const l = await importSkill('project')
      setList(l)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex-1 overflow-y-auto px-[30px] py-[28px]">
      <div className="max-w-[1000px] mx-auto">
        <h2 className="text-[20px] font-bold tracking-tight">技能</h2>
        <p className="mt-1 text-muted text-[13px]">技能是可复用的工作流(SKILL.md)。AI 在相关时会自动调用,或用 <code className="font-mono bg-surface2 border border-line2 px-1 rounded">/</code> 指定。保存后<b>当前会话立即生效</b>。</p>

        {list.problems.length > 0 && (
          <div className="mt-3 bg-redbg border border-[rgba(224,86,74,0.35)] rounded-[10px] px-3.5 py-2.5 text-[12.5px] text-red">
            {list.problems.length} 个技能加载失败:
            {list.problems.map((p, i) => (
              <div key={i} className="font-mono mt-1 truncate" title={`${p.dir}: ${p.reason}`}>{p.dir} — {p.reason}</div>
            ))}
          </div>
        )}

        <div className="mt-5 grid grid-cols-[268px_1fr] gap-5 items-start">
          <div className="flex flex-col gap-2.5">
            <div className="flex gap-2">
              <button className={`${BTN} ${BTN_PRIMARY} flex-1 inline-flex items-center justify-center gap-1.5 whitespace-nowrap !py-2`} onClick={startNew}>
                <Icon name="plus" size={15} /> 新建
              </button>
              <button className={`${BTN} flex-1 inline-flex items-center justify-center gap-1.5 whitespace-nowrap !py-2`} disabled={busy} onClick={importExisting}>
                <Icon name="book" size={15} /> 导入
              </button>
            </div>
            {list.skills.length === 0 ? (
              <div className="text-faint text-[13px] px-1 py-3 text-center border border-dashed border-line2 rounded-[11px]">还没有技能<br />点「新建」或「导入」</div>
            ) : (
              list.skills.map((sk) => (
                <div
                  key={sk.source + '/' + sk.name}
                  onClick={() => edit(sk)}
                  className={`group p-3 rounded-[11px] border cursor-pointer transition ${sel === sk.name ? 'border-primary bg-primarysoft' : 'border-line2 bg-surface hover:border-primary'}`}
                >
                  <div className="flex items-center gap-2">
                    <span className="font-semibold text-[13.5px] text-ink truncate min-w-0">{sk.name}</span>
                    <span className="text-[10px] text-faint border border-line2 rounded px-1.5 py-px flex-none">{sk.source === 'project' ? '项目' : '用户'}</span>
                    <button
                      className="ml-auto flex-none p-1 -m-1 text-faint hover:text-red opacity-0 group-hover:opacity-100 focus:opacity-100 transition disabled:opacity-40"
                      title="删除技能"
                      disabled={busy}
                      onClick={(e) => { e.stopPropagation(); removeFromList(sk) }}
                    >
                      <Icon name="trash" size={14} />
                    </button>
                  </div>
                  <div className="text-[12px] text-muted mt-1 line-clamp-2">{sk.description}</div>
                </div>
              ))
            )}
          </div>

          <div className="bg-surface border border-line2 rounded-[14px] p-5 shadow-xs min-h-[420px]">
            {sel === null ? (
              <div className="text-faint text-[13.5px] h-[380px] flex flex-col items-center justify-center gap-2">
                <Icon name="book" size={26} />
                <div>选择左侧技能查看 / 编辑,或新建、导入一个。</div>
              </div>
            ) : (
              <div className="flex flex-col gap-3.5">
                <label className={label}>名称<input className={field} value={name} onChange={(e) => setName(e.target.value)} placeholder="如 ppt-maker(字母、数字、- 或 _)" /></label>
                <label className={label}>
                  范围
                  {sel === 'new' ? (
                    <select className={field} value={scope} onChange={(e) => setScope(e.target.value)}>
                      <option value="project">项目(仅本工作区 .runcode/skills)</option>
                      <option value="user">用户(全局,所有项目可用)</option>
                    </select>
                  ) : (
                    <div className="text-[13px] text-ink bg-surface2 border border-line2 rounded-[9px] px-3 py-2.5">{scope === 'user' ? '用户(全局)' : '项目(本工作区)'}</div>
                  )}
                </label>
                <label className={label}>描述(一句话,告诉 AI 何时用它)<input className={field} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="如 制作排版规整、风格统一的 PPTX 演示文稿" /></label>
                <label className={label}>
                  指令正文(Markdown,AI 调用技能时读取的完整步骤)
                  <textarea className={`${field} min-h-[300px] font-mono text-[13px] leading-[1.6] resize-y`} value={body} onChange={(e) => setBody(e.target.value)} placeholder={'# 步骤\n1. 先做大纲\n2. 应用设计系统(配色/字体/留白)\n3. ...'} />
                </label>
                {error && <div className="text-[12.5px] text-red">{error}</div>}
                <div className="flex gap-2.5">
                  <button className={`${BTN} ${BTN_PRIMARY}`} disabled={busy || !name.trim()} onClick={save}>{busy ? '保存中…' : '保存'}</button>
                  {sel !== 'new' && <button className={BTN} onClick={() => onUse(originalName)}>在对话中使用</button>}
                  {sel !== 'new' && <button className={`${BTN} ${BTN_DANGER}`} disabled={busy} onClick={remove}>删除</button>}
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

// AgentsPage is the sub-agent manager: browse, create, edit, and delete the
// `.runcode/agents/<name>.md` files (project) or the user-global ones. Built-in
// agents are shown read-only.
export function AgentsPage({ onUse }: { onUse: (name: string) => void }) {
  const [list, setList] = useState<AgentList>({ agents: [], problems: [] })
  const [sel, setSel] = useState<string | 'new' | null>(null)
  const [name, setName] = useState('')
  const [originalName, setOriginalName] = useState('')
  const [description, setDescription] = useState('')
  const [tools, setTools] = useState('')
  const [model, setModel] = useState('')
  const [prompt, setPrompt] = useState('')
  const [scope, setScope] = useState('project')
  const [editable, setEditable] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const field = 'font-sans text-[14px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2.5 outline-none focus:border-primary focus:shadow-[0_0_0_3px_var(--color-primarysoft)] disabled:opacity-60'
  const label = 'flex flex-col gap-1.5 text-[12.5px] text-muted'

  function refresh() {
    listAgents()
      .then((l) => setList(l ?? { agents: [], problems: [] }))
      .catch((e) => setError(String(e)))
  }
  useEffect(() => {
    refresh()
  }, [])

  function edit(ag: AgentInfo) {
    setSel(ag.name)
    setName(ag.name)
    setOriginalName(ag.name)
    setDescription(ag.description)
    setTools(ag.tools)
    setModel(ag.model)
    setPrompt(ag.prompt)
    setScope(ag.source === 'user' ? 'user' : 'project')
    setEditable(ag.editable)
    setError('')
  }
  function startNew() {
    setSel('new')
    setName('')
    setOriginalName('')
    setDescription('')
    setTools('')
    setModel('')
    setPrompt('')
    setScope('project')
    setEditable(true)
    setError('')
  }
  async function save() {
    setBusy(true)
    setError('')
    try {
      const l = await saveAgent({ originalName, name: name.trim(), description, tools, model, prompt, scope })
      setList(l)
      setSel(name.trim())
      setOriginalName(name.trim())
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }
  async function remove() {
    if (originalName === '') return
    setBusy(true)
    try {
      const l = await deleteAgent(originalName, scope)
      setList(l)
      setSel(null)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }
  async function removeFromList(ag: AgentInfo) {
    setBusy(true)
    setError('')
    try {
      const l = await deleteAgent(ag.name, ag.source)
      setList(l)
      if (originalName === ag.name) setSel(null)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }
  async function importExisting() {
    setBusy(true)
    setError('')
    try {
      const l = await importAgent('project')
      setList(l)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }
  const sourceBadge = (s: string) => (s === 'project' ? '项目' : s === 'user' ? '用户' : '内置')

  return (
    <div className="flex-1 overflow-y-auto px-[30px] py-[28px]">
      <div className="max-w-[1000px] mx-auto">
        <h2 className="text-[20px] font-bold tracking-tight">子代理</h2>
        <p className="mt-1 text-muted text-[13px]">子代理是专注的助手人格,主 AI 用 <code className="font-mono bg-surface2 border border-line2 px-1 rounded">Task</code> 委派任务,或用 <code className="font-mono bg-surface2 border border-line2 px-1 rounded">/</code> 指定。保存后<b>当前会话立即生效</b>。内置子代理只读。</p>

        {list.problems.length > 0 && (
          <div className="mt-3 bg-redbg border border-[rgba(224,86,74,0.35)] rounded-[10px] px-3.5 py-2.5 text-[12.5px] text-red">
            {list.problems.length} 个子代理加载失败:
            {list.problems.map((p, i) => (
              <div key={i} className="font-mono mt-1 truncate" title={`${p.path}: ${p.reason}`}>{p.path} — {p.reason}</div>
            ))}
          </div>
        )}

        <div className="mt-5 grid grid-cols-[268px_1fr] gap-5 items-start">
          <div className="flex flex-col gap-2.5">
            <div className="flex gap-2">
              <button className={`${BTN} ${BTN_PRIMARY} flex-1 inline-flex items-center justify-center gap-1.5 whitespace-nowrap !py-2`} onClick={startNew}>
                <Icon name="plus" size={15} /> 新建
              </button>
              <button className={`${BTN} flex-1 inline-flex items-center justify-center gap-1.5 whitespace-nowrap !py-2`} disabled={busy} onClick={importExisting}>
                <Icon name="book" size={15} /> 导入
              </button>
            </div>
            {list.agents.length === 0 ? (
              <div className="text-faint text-[13px] px-1 py-3 text-center border border-dashed border-line2 rounded-[11px]">还没有子代理<br />点「新建」或「导入」</div>
            ) : (
              list.agents.map((ag) => (
                <div
                  key={ag.source + '/' + ag.name}
                  onClick={() => edit(ag)}
                  className={`group p-3 rounded-[11px] border cursor-pointer transition ${sel === ag.name ? 'border-primary bg-primarysoft' : 'border-line2 bg-surface hover:border-primary'}`}
                >
                  <div className="flex items-center gap-2">
                    <span className="font-semibold text-[13.5px] text-ink truncate min-w-0">{ag.name}</span>
                    <span className="text-[10px] text-faint border border-line2 rounded px-1.5 py-px flex-none">{sourceBadge(ag.source)}</span>
                    {ag.editable && (
                      <button
                        className="ml-auto flex-none p-1 -m-1 text-faint hover:text-red opacity-0 group-hover:opacity-100 focus:opacity-100 transition disabled:opacity-40"
                        title="删除子代理"
                        disabled={busy}
                        onClick={(e) => { e.stopPropagation(); removeFromList(ag) }}
                      >
                        <Icon name="trash" size={14} />
                      </button>
                    )}
                  </div>
                  <div className="text-[12px] text-muted mt-1 line-clamp-2">{ag.description}</div>
                </div>
              ))
            )}
          </div>

          <div className="bg-surface border border-line2 rounded-[14px] p-5 shadow-xs min-h-[420px]">
            {sel === null ? (
              <div className="text-faint text-[13.5px] h-[380px] flex flex-col items-center justify-center gap-2">
                <Icon name="bot" size={26} />
                <div>选择左侧子代理查看 / 编辑,或新建、导入一个。</div>
              </div>
            ) : (
              <div className="flex flex-col gap-3.5">
                {!editable && <div className="text-[12.5px] text-muted bg-surface2 border border-line2 rounded-[9px] px-3 py-2">内置子代理,只读。可「在对话中使用」,或在用户/项目级新建同名子代理来覆盖它。</div>}
                <label className={label}>名称<input className={field} value={name} disabled={!editable} onChange={(e) => setName(e.target.value)} placeholder="如 code-reviewer(字母、数字、- 或 _)" /></label>
                <label className={label}>
                  范围
                  {sel === 'new' ? (
                    <select className={field} value={scope} onChange={(e) => setScope(e.target.value)}>
                      <option value="project">项目(仅本工作区 .runcode/agents)</option>
                      <option value="user">用户(全局,所有项目可用)</option>
                    </select>
                  ) : (
                    <div className="text-[13px] text-ink bg-surface2 border border-line2 rounded-[9px] px-3 py-2.5">{scope === 'user' ? '用户(全局)' : scope === 'project' ? '项目(本工作区)' : '内置'}</div>
                  )}
                </label>
                <label className={label}>描述(一句话,告诉 AI 何时委派它)<input className={field} value={description} disabled={!editable} onChange={(e) => setDescription(e.target.value)} placeholder="如 审查代码,找出 bug 与风险" /></label>
                <div className="grid grid-cols-2 gap-3">
                  <label className={label}>工具(可选,逗号分隔;留空=继承全部)<input className={field} value={tools} disabled={!editable} onChange={(e) => setTools(e.target.value)} placeholder="如 Read, Grep, Glob" /></label>
                  <label className={label}>模型(可选,覆盖默认)<input className={field} value={model} disabled={!editable} onChange={(e) => setModel(e.target.value)} placeholder="留空=继承会话模型" /></label>
                </div>
                <label className={label}>
                  指令正文(Markdown,子代理的系统提示)
                  <textarea className={`${field} min-h-[260px] font-mono text-[13px] leading-[1.6] resize-y`} value={prompt} disabled={!editable} onChange={(e) => setPrompt(e.target.value)} placeholder={'你是一名……\n\n职责:\n- ……\n\n完成后用要点汇报结论。'} />
                </label>
                {error && <div className="text-[12.5px] text-red">{error}</div>}
                <div className="flex gap-2.5">
                  {editable && <button className={`${BTN} ${BTN_PRIMARY}`} disabled={busy || !name.trim()} onClick={save}>{busy ? '保存中…' : '保存'}</button>}
                  {sel !== 'new' && <button className={BTN} onClick={() => onUse(originalName)}>在对话中使用</button>}
                  {sel !== 'new' && editable && <button className={`${BTN} ${BTN_DANGER}`} disabled={busy} onClick={remove}>删除</button>}
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

export function SettingsPage({ initial, info, onSaved }: { initial: Partial<StartSessionRequest>; info: SessionInfo | null; onSaved: (info: SessionInfo) => void }) {
  // Model and permission mode prefer the live session (they can change at runtime);
  // connection settings come from the saved config.
  const [model, setModel] = useState(info?.model || initial.model || '')
  const [permissionMode, setPermissionMode] = useState(info?.permissionMode || initial.permissionMode || 'interactive')
  const [provider, setProvider] = useState(initial.provider || 'openai')
  const [baseURL, setBaseURL] = useState(initial.baseURL ?? '')
  const [apiKey, setApiKey] = useState(initial.apiKey ?? '')
  const [maxTokens, setMaxTokens] = useState(initial.maxTokens ? String(initial.maxTokens) : '')
  const [maxContextTokens, setMaxContextTokens] = useState(initial.maxContextTokens ?? 128000)
  const [maxHistoryMessages, setMaxHistoryMessages] = useState(initial.maxHistoryMessages ? String(initial.maxHistoryMessages) : '')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState('')
  const field = 'font-sans text-[14px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2.5 outline-none focus:border-primary focus:shadow-[0_0_0_3px_var(--color-primarysoft)]'
  const label = 'flex flex-col gap-1.5 text-[12.5px] text-muted'

  async function save() {
    setSaving(true)
    setSaved(false)
    setError('')
    try {
      const i = await saveSettings({
        cwd: info?.cwd ?? '',
        model,
        provider,
        baseURL,
        permissionMode,
        apiKey,
        maxTokens: maxTokens.trim() ? parseInt(maxTokens, 10) || 0 : 0,
        maxContextTokens,
        maxHistoryMessages: maxHistoryMessages.trim() ? parseInt(maxHistoryMessages, 10) || 0 : 0,
        // Preserved (edited via the in-conversation picker, not this form) so saving
        // connection settings does not silently reset the reasoning strength.
        thinkingEffort: initial.thinkingEffort ?? '',
      })
      if (i && i.model) onSaved(i)
      setSaved(true)
      setTimeout(() => setSaved(false), 2200)
    } catch (e) {
      setError(String(e))
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

        <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
          <div className="text-[13px] font-semibold text-ink">会话</div>
          <label className={label}>模型<input className={field} value={model} onChange={(e) => setModel(e.target.value)} placeholder="如 qwen3.6-27b" /></label>
          <label className={label}>权限模式
            <select className={field} value={permissionMode} onChange={(e) => setPermissionMode(e.target.value)}>
              <option value="interactive">交互（逐项询问）</option>
              <option value="judge">智能（模型审查命令）</option>
              <option value="safe">安全（拒绝高危）</option>
              <option value="flight">飞行（不审计，全部放行）</option>
            </select>
          </label>
          {permissionMode === 'flight' && (
            <div className="flex items-start gap-2 bg-redbg border border-[rgba(224,86,74,0.35)] rounded-lg px-3 py-2.5 text-[12.5px] text-red">
              <span className="flex-none mt-px"><Icon name="shield" size={15} /></span>
              <span>飞行模式会<b>放行一切操作</b>（含删除文件、sudo 等高危命令），不再询问也不做模型审查。仅在完全信任的环境使用。</span>
            </div>
          )}
        </section>

        <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
          <div className="flex items-center justify-between">
            <div className="text-[13px] font-semibold text-ink">连接</div>
            <span className="text-[11.5px] text-faint">下次新建会话生效</span>
          </div>
          <label className={label}>服务商
            <select className={field} value={provider} onChange={(e) => setProvider(e.target.value)}>
              <option value="openai">openai</option>
              <option value="anthropic">anthropic</option>
            </select>
          </label>
          <label className={label}>Base URL<input className={field} value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder="https://..." /></label>
          <label className={label}>API 密钥<input className={field} type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder="可留空，改用环境变量" /></label>
          <label className={label}>最大输出 Tokens<input className={field} type="number" value={maxTokens} onChange={(e) => setMaxTokens(e.target.value)} placeholder="留空则用默认 16384" /></label>
        </section>

        <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
          <div className="flex items-center justify-between">
            <div className="text-[13px] font-semibold text-ink">上下文长度控制</div>
            <span className="text-[11.5px] text-faint">下次新建会话生效</span>
          </div>
          <label className={label}>上下文预算（超出后自动总结压缩较早对话；磁盘记录保持完整）
            <select className={field} value={String(maxContextTokens)} onChange={(e) => setMaxContextTokens(parseInt(e.target.value, 10))}>
              <option value="0">关闭 · 不自动压缩</option>
              <option value="32000">32K · 省 token</option>
              <option value="128000">128K · 推荐</option>
              <option value="200000">200K · 大窗口</option>
            </select>
          </label>
          <label className={label}>历史消息上限（硬截断，仅保留最近 N 条；留空关闭）
            <input className={field} type="number" value={maxHistoryMessages} onChange={(e) => setMaxHistoryMessages(e.target.value)} placeholder="留空 = 不截断（推荐优先用上面的自动压缩）" />
          </label>
        </section>

        <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-1.5 shadow-xs">
          <div className="text-[13px] font-semibold text-ink">工作区</div>
          <div className="font-mono text-[12.5px] text-muted break-all">{info?.cwd || '—'}</div>
        </section>

        {error && <div className="text-red text-[13px]">{error}</div>}
        <div className="flex items-center gap-3 pb-2">
          <button className={`${BTN} ${BTN_PRIMARY} px-7 py-2.5`} disabled={saving} onClick={save}>{saving ? '保存中…' : '保存设置'}</button>
          {saved && <span className="text-green text-[13px]">✓ 已保存</span>}
        </div>
      </div>
    </div>
  )
}

export function StartForm({ onStart, starting, error, initial }: { onStart: (req: StartSessionRequest) => void; starting: boolean; error: string; initial: StartSessionRequest }) {
  const [cwd, setCwd] = useState(initial.cwd ?? '')
  const [model, setModel] = useState(initial.model ?? '')
  const [provider, setProvider] = useState(initial.provider || 'openai')
  const [baseURL, setBaseURL] = useState(initial.baseURL ?? 'https://tenantapi-ai.ouchn.edu.cn/v1')
  const [permissionMode, setPermissionMode] = useState(initial.permissionMode || 'interactive')
  const [apiKey, setApiKey] = useState(initial.apiKey ?? '')
  const [thinkingEffort, setThinkingEffort] = useState(initial.thinkingEffort ?? '')
  const [maxContextTokens, setMaxContextTokens] = useState(initial.maxContextTokens ?? 128000)
  const field = 'font-sans text-[14px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2.5 outline-none focus:border-primary focus:shadow-[0_0_0_3px_var(--color-primarysoft)]'
  const label = 'flex flex-col gap-1.5 text-[12.5px] text-muted'

  return (
    <div className="flex items-center justify-center flex-1 min-h-0 p-6">
      <div className="w-[480px] bg-surface rounded-[18px] p-8 flex flex-col gap-[13px] shadow-card">
        <div className="flex items-center gap-3.5 mb-1">
          <span className="w-[48px] h-[48px] rounded-[13px] inline-flex items-center justify-center bg-surface border border-line2 shadow-xs"><Logo size={34} /></span>
          <div>
            <h1 className="m-0 text-[22px] font-bold tracking-tight">XRUN</h1>
            <p className="mt-[3px] text-muted text-[13px]">你的 AI 编程伙伴 · 打开一个工作区开始会话</p>
          </div>
        </div>
        <label className={label}>工作区目录<input className={field} value={cwd} onChange={(e) => setCwd(e.target.value)} placeholder="C:\path\to\project" /></label>
        <div className="grid grid-cols-2 gap-3">
          <label className={label}>服务商
            <select className={field} value={provider} onChange={(e) => setProvider(e.target.value)}>
              <option value="openai">openai</option>
              <option value="anthropic">anthropic</option>
            </select>
          </label>
          <label className={label}>权限模式
            <select className={field} value={permissionMode} onChange={(e) => setPermissionMode(e.target.value)}>
              <option value="interactive">交互（逐项询问）</option>
              <option value="judge">智能（模型审查命令）</option>
              <option value="safe">安全（拒绝高危）</option>
              <option value="flight">飞行（不审计，全部放行）</option>
            </select>
          </label>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <label className={label}>模型<input className={field} value={model} onChange={(e) => setModel(e.target.value)} placeholder="留空则用 $ANTHROPIC_MODEL" /></label>
          <label className={label}>思考强度（推理模型可见思考过程）
            <select className={field} value={thinkingEffort} onChange={(e) => setThinkingEffort(e.target.value)}>
              <option value="">不启用</option>
              <option value="low">低 · 快速</option>
              <option value="medium">中 · 均衡</option>
              <option value="high">高 · 深度</option>
            </select>
          </label>
        </div>
        <label className={label}>上下文预算（超出后自动总结压缩较早对话）
          <select className={field} value={String(maxContextTokens)} onChange={(e) => setMaxContextTokens(parseInt(e.target.value, 10))}>
            <option value="0">关闭 · 不自动压缩</option>
            <option value="32000">32K · 省 token</option>
            <option value="128000">128K · 推荐</option>
            <option value="200000">200K · 大窗口</option>
          </select>
        </label>
        <label className={label}>Base URL<input className={field} value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder="https://api.anthropic.com" /></label>
        <label className={label}>API 密钥<input className={field} type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder="可留空，改用环境变量" /></label>
        {error && <div className="text-red text-[13px]">{error}</div>}
        <button className={`${BTN} ${BTN_PRIMARY} py-3 text-[15px] mt-1.5`} disabled={!cwd.trim() || starting} onClick={() => onStart({ cwd, model, provider, baseURL, permissionMode, apiKey, thinkingEffort, maxContextTokens })}>
          {starting ? '启动中…' : '开始会话'}
        </button>
      </div>
    </div>
  )
}

