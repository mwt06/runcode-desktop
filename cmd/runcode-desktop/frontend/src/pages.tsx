// Full-screen pages rendered by the app shell: the skills / agents / settings
// managers and the initial start form. Extracted from App.tsx to keep it focused.
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Icon, Logo, TOOL_ICON } from './icons'
import loginBg from './assets/login-bg.jpg'
import loginMascot from './assets/login-mascot.svg'
import { Markdown } from './markdown'
import { BTN, BTN_PRIMARY, BTN_DANGER } from './ui'
import { FIELD_CLS, LABEL_CLS, ModelPickerPopover, Popover, SelectField, sourceLabel, type ModelOption } from './components'
import { shortenPath } from './paths'
import {
  listSkills, saveSkill, deleteSkill, importSkill,
  listAgents, saveAgent, deleteAgent, importAgent,
  saveSettings, pickWorkspaceFolder,
  listMCPServers, saveMCPServer, deleteMCPServer, setMCPServerEnabled,
  listTools, setToolEnabled, setAgentEnabled, setSkillEnabled, readProjectContext, saveProjectContext, readMemory,
  passportStatus, passportValidate, passportLogin, passportCancelLogin, passportLogout, passportModels, passportTenants,
  setActiveTenant, activeTenant,
  listCustomModels, saveCustomModel, deleteCustomModel, sessionModels,
  webProxy, setWebProxy,
  onEvent, Events,
  type SkillInfo,
  type AgentInfo,
  type MCPServerInfo, type MCPServerInput,
  type ToolInfo, type ProjectContextInfo, type MemoryInfo,
  type SessionInfo, type StartSessionRequest,
  type PassportStatus, type PassportModel, type PassportTenant, type CustomModel,
  errText,
} from './bridge'

// 内置工具/子代理的中文显示映射。仅用于界面展示——发送给模型的工具定义、
// 子代理名称与描述、以及 prompt 全部保持原样(英文),所以模型的工具调用与
// 委派判断不受影响。MCP/用户自定义的工具与子代理不在此映射内，按原样显示。
const TOOL_ZH: Record<string, { label: string; desc: string }> = {
  Read: { label: '读取文件', desc: '读取文件内容。文本返回带行号的内容；图片(png/jpg/jpeg/gif/webp)直接返回图像。' },
  Write: { label: '写入文件', desc: '创建或覆盖写入一个文件。' },
  Edit: { label: '编辑文件', desc: '在已读取的文件里做精确文本替换。' },
  Delete: { label: '删除', desc: '删除工作区里的文件或目录，默认移入回收站(可恢复)，permanent=true 则不可逆删除。' },
  Glob: { label: '查找文件', desc: '按 glob 通配符查找工作区文件。' },
  Grep: { label: '搜索代码', desc: '用正则搜索工作区文件内容，支持内容/文件名/计数等输出模式、上下文行与多行匹配。' },
  Bash: { label: '运行命令', desc: '在工作区执行非交互 shell 命令(需授权，Windows 用 cmd，其余用 bash)。' },
  BashOutput: { label: '后台命令输出', desc: '读取后台运行命令的新增输出。' },
  KillShell: { label: '终止后台命令', desc: '终止一个后台运行的命令。' },
  TodoWrite: { label: '规划任务', desc: '记录当前任务清单，每次传完整列表并替换上一次。' },
  WebFetch: { label: '抓取网页', desc: '抓取一个网址并按提示词处理其内容。' },
  WebSearch: { label: '联网搜索', desc: '通过搜索引擎联网检索并返回结果(标题、网址、摘要)。' },
  Wait: { label: '等待', desc: '暂停指定秒数再继续——等待构建、部署或后台命令等外部操作稳定后再检查。' },
  GetCurrentTime: { label: '获取当前时间', desc: '返回当前日期与时间(本地与 UTC、所在时区及 RFC3339 时间戳)。' },
  Remember: { label: '记录记忆', desc: '把跨会话有用的事实写入持久记忆(用户级或项目级)，下次会话自动带上。' },
  Analyze: { label: '结构化分析', desc: '为当前思考协议记录结构化分析。' },
  AskUser: { label: '询问用户', desc: '向用户提问并停下等待回复，用于需要用户决策或缺少关键信息时。' },
  open_preview: { label: '预览产物', desc: '在桌面预览面板打开工作区文件(仅桌面版)。' },
  Task: { label: '委派子代理', desc: '把一个自包含的子任务委派给子代理独立执行。' },
  Skill: { label: '加载技能', desc: '加载并执行一个已定义的技能。' },
}
const AGENT_ZH: Record<string, { label: string; desc: string; prompt: string }> = {
  'general-purpose': {
    label: '通用代理',
    desc: '通用型代理，用于研究复杂问题、检索代码库、执行多步骤任务。任务开放式或没把握几步内找到答案时使用。',
    prompt: `你是一个通用型研究与执行子代理，处理主代理委派的开放式、多步骤任务——检索代码库、读取文件、追踪实现原理、执行只读检查，以及完成范围明确的改动。

自主高效地工作：
- 只收集任务需要的上下文，不做多余探索。
- 动手前优先用搜索和读取类工具建立理解。
- 任务含糊时，采用最合理的假设并在结果里说明，而不是默默猜测。

你的最终消息就是主代理收到的全部结果，务必完整具体：说清你发现了什么或做了什么，引用具体文件路径与标识符，并指出任何未解决的问题。`,
  },
  'code-reviewer': {
    label: '代码审查',
    desc: '审查 diff 或一组文件，找出 bug、安全与质量问题，只报高置信度发现并给出具体修法。只读，写完/改完代码后用。',
    prompt: `你是一名严谨的代码审查者。给你一段 diff、一组文件或一个改动描述，你只报出真正重要的问题。

按优先级聚焦：
1. 正确性——逻辑错误、条件写反、差一错误、空指针/未定义、未处理的错误、竞态、边界情况失效。
2. 安全——注入、路径穿越、不安全输入、泄漏密钥、缺失鉴权。
3. 资源与生命周期——泄漏、未关闭的句柄、goroutine/promise 泄漏、无界增长。
4. 可维护性——仅当明显有害时：死代码、误导性命名、重复逻辑、破坏约定。

规则：
- 下判断前先读周边代码；不报你没确认的问题。
- 只报真实、高置信度的问题，不挑无关痛痒的风格。
- 每条给出：文件:行号、哪里错、为何重要、具体修法。
- 没发现严重问题就直说，别编问题。

你只读和搜索，从不编辑。你的最终消息就是主代理收到的完整审查。`,
  },
  'code-explorer': {
    label: '代码探索',
    desc: '追踪某个功能或流程在代码库里如何工作，返回关键文件、类型、调用路径的地图。只读，改陌生代码前用来理解。',
    prompt: `你是代码库探索者。你追踪某个功能、流程或符号实际如何工作，并向主代理返回清晰的地图。

方法：
- 从任务点名的入口开始；跨文件跟踪调用、导入和数据流。
- 找出关键的类型、函数、文件，以及它们如何连接。
- 记下重要的抽象、不变量，以及任何意外或脆弱之处。

精确具体：引用 文件:行号 和确切标识符，而非笼统概述。区分你在代码里验证过的与你推断的。你只读和搜索，从不修改。你的最终消息就是整份调查结果，务必自包含：主代理不会重新读文件就据此行动。`,
  },
  planner: {
    label: '实现规划',
    desc: '研究代码库并产出有序的实现计划(要改哪些文件、构建顺序、风险)，不做任何改动。只读，动手前用来设计非平凡改动。',
    prompt: `你是实现规划者。给定一个目标，你研究现有代码并产出一份具体、有序的计划——你不做任何改动。

产出：
1. 简述所选方案与关键取舍，被否决的备选一句话带过。
2. 要创建或修改的确切文件，每个说明改什么、为什么。
3. 有序的构建步骤（先做什么、谁依赖谁）以及每步如何验证。
4. 风险、边界情况，以及任何需要主代理决策的点。

每一步都扎根真实代码库：引用你所遵循的文件与模式，使计划契合现有约定。你只读和搜索，从不编辑。你的最终消息就是主代理将执行的完整计划。`,
  },
  debugger: {
    label: '调试定位',
    desc: '带证据定位失败根因(崩溃、测试失败、输出错误)并给出最小修法但不落地。出问题且原因不明时用。',
    prompt: `你是调试专家。你定位一次失败的根因——崩溃、测试失败、输出错误或卡死——并带证据报告。

方法：
- 先复现或检查失败；读错误信息、栈回溯和确切的失败代码路径。
- 形成假设，再通过读相关代码确认（必要时跑一个聚焦的只读检查或那个失败的测试）。
- 追到真正的根因，而非表面症状。

报告：根因（文件:行号 及原因）、证明它的证据、以及最小修法——精确描述改动但不落地。若无法确认原因，给出最可能的候选并排序，说明各自如何验证。你的最终消息就是主代理收到的整份诊断。`,
  },
}

// Toggle 是参考设计里的 iOS 拨动开关：蓝色=开，灰色=关。
function Toggle({ on, onChange, disabled }: { on: boolean; onChange: (next: boolean) => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      disabled={disabled}
      onClick={(e) => { e.stopPropagation(); onChange(!on) }}
      className={`relative inline-flex h-[24px] w-[42px] flex-none items-center rounded-full transition-colors ${on ? 'bg-primary' : 'bg-line2'} ${disabled ? 'opacity-40 cursor-default' : 'cursor-pointer'}`}
    >
      <span className={`inline-block h-[18px] w-[18px] transform rounded-full bg-white shadow transition-transform ${on ? 'translate-x-[21px]' : 'translate-x-[3px]'}`} />
    </button>
  )
}

// ToolMultiSelect 是子代理「工具」字段的多选下拉:从工具目录勾选,存回逗号分隔
// 串(与子代理 .md frontmatter 的 wire 格式一致);留空 = 继承全部工具。点选不
// 关闭面板,方便连续勾选;点外部收起。
function ToolMultiSelect({ value, options, onChange, disabled }: {
  value: string
  options: ToolInfo[]
  onChange: (next: string) => void
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const picked = value.split(',').map((s) => s.trim()).filter(Boolean)
  const toggle = (n: string) => {
    const next = picked.includes(n) ? picked.filter((p) => p !== n) : [...picked, n]
    onChange(next.join(', '))
  }
  return (
    <div className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        className={`${FIELD_CLS} w-full flex items-center text-left ${disabled ? '' : 'cursor-pointer hover:border-primary/60'} transition-colors`}
      >
        <span className={`flex-1 truncate ${picked.length ? 'font-mono' : 'text-faint'}`}>{picked.length ? picked.join(', ') : '继承全部工具'}</span>
        <Icon name="chevron-down" size={16} className="text-faint flex-none ml-2" />
      </button>
      <Popover open={open} onClose={() => setOpen(false)} placement="down-full" variant="panel" className="max-h-[300px]">
        <div className="overflow-y-auto py-1">
          <button
            type="button"
            onClick={() => onChange('')}
            className={`w-full text-left px-3.5 py-2 text-[12.5px] hover:bg-surface2 ${picked.length ? 'text-muted' : 'text-primary'}`}
          >
            继承全部工具{picked.length === 0 && ' ✓'}
          </button>
          {options.map((t) => {
            const zh = TOOL_ZH[t.name]
            const on = picked.includes(t.name)
            return (
              <button key={t.name} type="button" onClick={() => toggle(t.name)} className="w-full text-left px-3.5 py-2 flex items-center gap-2 hover:bg-surface2">
                <span className={`w-4 h-4 rounded border flex-none inline-flex items-center justify-center text-[10px] leading-none ${on ? 'bg-primary border-primary text-white' : 'border-line2 bg-surface'}`}>{on ? '✓' : ''}</span>
                <span className="text-[13px] text-ink flex-none">{zh?.label ?? t.name}</span>
                {zh && <span className="font-mono text-[11.5px] text-faint truncate">{t.name}</span>}
              </button>
            )
          })}
          {options.length === 0 && (
            <div className="px-3.5 py-6 text-center text-[12.5px] text-muted">工具目录为空(启动一个会话后可选);留空即继承全部工具</div>
          )}
        </div>
      </Popover>
    </div>
  )
}

// AgentDetail 是子代理的全屏详情/编辑页(替代原来的左右分栏)。内置只读。
function AgentDetail({ agent, onBack, onChanged, onUse }: {
  agent: AgentInfo | 'new'
  onBack: () => void
  onChanged: () => void
  onUse: (name: string) => void
}) {
  const isNew = agent === 'new'
  const ag = isNew ? null : agent
  const zh = ag && ag.source === 'builtin' ? AGENT_ZH[ag.name] : undefined
  const [name, setName] = useState(ag?.name ?? '')
  const [description, setDescription] = useState(zh?.desc ?? ag?.description ?? '')
  const [tools, setTools] = useState(ag?.tools ?? '')
  const [model, setModel] = useState(ag?.model ?? '')
  const [prompt, setPrompt] = useState(zh?.prompt ?? ag?.prompt ?? '')
  const [scope, setScope] = useState(isNew ? 'project' : ag?.source === 'user' ? 'user' : 'project')
  const editable = isNew || (ag?.editable ?? false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // 工具/模型的候选清单(进入详情页拉一次):工具来自会话工具目录,模型与设置页
  // 同源(平台 + 自定义)。拉取失败保持空列表,两个下拉退化为「留空/自定义输入」。
  const [toolOptions, setToolOptions] = useState<ToolInfo[]>([])
  const [modelOptions, setModelOptions] = useState<ModelOption[]>([])
  useEffect(() => {
    listTools().then((l) => setToolOptions(l ?? [])).catch(() => {})
    void Promise.all([sessionModels().catch(() => null), listCustomModels().catch(() => null)]).then(([platform, custom]) =>
      setModelOptions([
        ...(platform ?? []).map((m): ModelOption => ({ kind: 'platform', id: m.id, label: m.id, sub: m.ownedBy })),
        ...(custom ?? []).map((c): ModelOption => ({ kind: 'custom', id: c.model, label: c.name, sub: c.model })),
      ]),
    )
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

// PluginsPage 是统一的能力管理页：标签页切换 工具/子代理/技能/MCP，顶部选停用
// 范围 + 搜索，每行图标+名称+描述+iOS 开关(参考设计)。技能/MCP 复用现有管理。
// SkillDetail 是技能的全屏详情/编辑页。技能均为用户/项目文件，可编辑。
function SkillDetail({ skill, onBack, onChanged, onUse }: {
  skill: SkillInfo | 'new'
  onBack: () => void
  onChanged: () => void
  onUse: (name: string) => void
}) {
  const isNew = skill === 'new'
  const sk = isNew ? null : skill
  const [name, setName] = useState(sk?.name ?? '')
  const [description, setDescription] = useState(sk?.description ?? '')
  const [body, setBody] = useState(sk?.body ?? '')
  const [scope, setScope] = useState(isNew ? 'project' : sk?.source === 'user' ? 'user' : 'project')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function save() {
    setBusy(true); setError('')
    try { await saveSkill({ originalName: sk?.name ?? '', name: name.trim(), description, body, scope }); onChanged(); onBack() } catch (e) { setError(errText(e)) } finally { setBusy(false) }
  }
  async function remove() {
    if (!sk) return
    setBusy(true); setError('')
    try { await deleteSkill(sk.name, sk.source); onChanged(); onBack() } catch (e) { setError(errText(e)) } finally { setBusy(false) }
  }
  return (
    <div className="flex-1 overflow-y-auto px-10 py-7">
      <div className="max-w-[820px] mx-auto flex flex-col gap-4">
        <button type="button" className="self-start text-[13px] text-muted hover:text-ink inline-flex items-center gap-1" onClick={onBack}>← 返回列表</button>
        <label className={LABEL_CLS}>名称<input className={FIELD_CLS} value={name} onChange={(e) => setName(e.target.value)} placeholder="如 ppt-maker(字母、数字、- 或 _)" /></label>
        <label className={LABEL_CLS}>
          范围
          {isNew ? (
            <select className={FIELD_CLS} value={scope} onChange={(e) => setScope(e.target.value)}>
              <option value="project">项目(仅本工作区 .runcode/skills)</option>
              <option value="user">用户(全局，所有项目可用)</option>
            </select>
          ) : (
            <div className="text-[13px] text-ink bg-surface2 border border-line2 rounded-[9px] px-3 py-2.5">{scope === 'user' ? '用户(全局)' : '项目(本工作区)'}</div>
          )}
        </label>
        <label className={LABEL_CLS}>描述(一句话，告诉 AI 何时加载它)<input className={FIELD_CLS} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="如 制作 PPT 演示文稿" /></label>
        <label className={LABEL_CLS}>
          正文(Markdown，技能的完整指令)
          <textarea className={`${FIELD_CLS} min-h-[320px] font-mono text-[13px] leading-[1.6] resize-y`} value={body} onChange={(e) => setBody(e.target.value)} />
        </label>
        {error && <div className="text-[12.5px] text-red">{error}</div>}
        <div className="flex gap-2.5">
          <button className={`${BTN} ${BTN_PRIMARY}`} disabled={busy || !name.trim()} onClick={save}>{busy ? '保存中…' : '保存'}</button>
          {!isNew && <button className={BTN} onClick={() => onUse(sk!.name)}>在对话中使用</button>}
          {!isNew && <button className={`${BTN} ${BTN_DANGER}`} disabled={busy} onClick={remove}>删除</button>}
        </div>
      </div>
    </div>
  )
}

export function PluginsPage({ onUseSkill, onUseAgent }: { onUseSkill: (name: string) => void; onUseAgent: (name: string) => void }) {
  const [tab, setTab] = useState<'tools' | 'agents' | 'skills' | 'mcp'>('tools')
  const [scope, setScope] = useState<'project' | 'user'>('project')
  const [query, setQuery] = useState('')
  const [toolList, setToolList] = useState<ToolInfo[]>([])
  const [agentList, setAgentList] = useState<AgentInfo[]>([])
  const [skillList, setSkillList] = useState<SkillInfo[]>([])
  const [err, setErr] = useState('')
  const [detail, setDetail] = useState<{ k: 'agent'; item: AgentInfo | 'new' } | { k: 'skill'; item: SkillInfo | 'new' } | null>(null)
  // 导入范围显式选择(不复用上面的「停用范围」——那个只管开关的作用域)，
  // 与「新建」在详情页里选范围的做法保持一致。
  const [importMenu, setImportMenu] = useState(false)

  const reloadTools = async () => { try { setToolList((await listTools()) ?? []) } catch { /* ignore */ } }
  const reloadAgents = async () => { try { const l = await listAgents(); setAgentList(l?.agents ?? []) } catch { /* ignore */ } }
  const reloadSkills = async () => { try { const l = await listSkills(); setSkillList(l?.skills ?? []) } catch { /* ignore */ } }
  useEffect(() => { void reloadTools(); void reloadAgents(); void reloadSkills() }, [])

  // 导入当前标签页对应的能力：弹原生选择器(技能选整个文件夹连同相关文件，子代理选 .md)，
  // 校验后写入所选范围。取消选择不报错(后端原样返回当前列表)。
  const doImport = async (s: 'project' | 'user') => {
    setImportMenu(false)
    setErr('')
    try {
      if (tab === 'skills') setSkillList((await importSkill(s))?.skills ?? [])
      else setAgentList((await importAgent(s))?.agents ?? [])
    } catch (e) {
      setErr(errText(e))
    }
  }

  // 详情为全屏单页(替代左右分栏)。
  if (detail?.k === 'agent') return <AgentDetail agent={detail.item} onBack={() => setDetail(null)} onChanged={reloadAgents} onUse={onUseAgent} />
  if (detail?.k === 'skill') return <SkillDetail skill={detail.item} onBack={() => setDetail(null)} onChanged={reloadSkills} onUse={onUseSkill} />

  const scopeOn = (du: boolean, dp: boolean) => (scope === 'project' ? !dp : !du)
  const otherOffLabel = (du: boolean, dp: boolean) => (scope === 'project' ? (du ? '全局已停用' : '') : dp ? '本项目已停用' : '')
  const toggle = (setFn: (n: string, s: string, e: boolean) => Promise<void>, reload: () => Promise<void>) => async (name: string, next: boolean) => {
    setErr('')
    try { await setFn(name, scope, next); await reload() } catch (e) { setErr(errText(e)) }
  }
  const toggleTool = toggle(setToolEnabled, reloadTools)
  const toggleAgent = toggle(setAgentEnabled, reloadAgents)
  const toggleSkill = toggle(setSkillEnabled, reloadSkills)

  const q = query.trim().toLowerCase()
  const hit = (a: string, b: string, name: string) => !q || name.toLowerCase().includes(q) || a.toLowerCase().includes(q) || b.toLowerCase().includes(q)
  // This page manages only user/project-authored capabilities; built-ins are the
  // engine's fixed, read-only set, so they are hidden here (the model still uses
  // them, and the composer's / picker still lists built-in agents to delegate).
  // 全局(用户级)范围下再隐藏项目级条目:用户级停用是按名字记进全局 disabled.json
  // 的,对只存在于本工作区的项目级子代理/技能做「全局停用」既无意义,还会误伤
  // 其它项目的同名条目;切到「本项目」范围即可管理它们。
  const inScope = (source: string) => scope === 'project' || source !== 'project'
  const scopedAgents = agentList.filter((a) => a.source !== 'builtin' && inScope(a.source))
  const scopedSkills = skillList.filter((s) => inScope(s.source))
  const shownTools = toolList.filter((t) => t.toggleable && t.source !== 'builtin').filter((t) => { const z = TOOL_ZH[t.name]; return hit(z?.label ?? '', z?.desc ?? t.description, t.name) })
  const shownAgents = scopedAgents.filter((a) => hit('', a.description, a.name))
  const shownSkills = scopedSkills.filter((s) => hit(s.name, s.description, s.name))

  const tabs: { k: typeof tab; label: string; n?: number }[] = [
    { k: 'tools', label: '工具', n: toolList.filter((t) => t.toggleable && t.source !== 'builtin').length },
    { k: 'agents', label: '子代理', n: scopedAgents.length },
    { k: 'skills', label: '技能', n: scopedSkills.length },
    { k: 'mcp', label: 'MCP' },
  ]
  const showControls = tab !== 'mcp'
  const scopeBtn = (s: 'project' | 'user', text: string) => (
    <button type="button" onClick={() => setScope(s)} className={`px-3.5 py-1 text-[12.5px] transition ${scope === s ? 'bg-primary text-white' : 'text-muted hover:text-ink'}`}>{text}</button>
  )

  // 一张能力卡片：图标 + 名称(+原名/徽章) + 描述 + [使用] + iOS 开关。可点击项进入详情。
  const card = (key: string, icon: string, title: string, raw: string, tag: string, desc: string, on: boolean, otherOff: string, onToggle: (n: boolean) => void, onClick?: () => void, onUse?: () => void) => (
    <div
      key={key}
      onClick={onClick}
      className={`bg-surface border border-line2 rounded-[14px] px-5 py-4 flex items-center gap-4 transition ${onClick ? 'cursor-pointer hover:border-primary hover:shadow-xs' : ''} ${on ? '' : 'opacity-60'}`}
    >
      <span className="w-10 h-10 rounded-[11px] bg-surface2 border border-line2 flex items-center justify-center flex-none text-muted"><Icon name={icon} size={19} /></span>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-[14.5px] text-ink truncate">{title}</span>
          {raw && <span className="font-mono text-[11.5px] text-faint flex-none">{raw}</span>}
          {tag && <span className="text-[10.5px] text-faint border border-line2 rounded px-1.5 py-px flex-none">{tag}</span>}
        </div>
        <div className="text-[12.5px] text-muted mt-1 line-clamp-2 leading-relaxed">{desc}</div>
      </div>
      {otherOff && <span className="text-[11px] text-red/70 flex-none">{otherOff}</span>}
      {onUse && <button type="button" className="text-[12px] text-muted hover:text-primary flex-none" onClick={(e) => { e.stopPropagation(); onUse() }}>使用</button>}
      <Toggle on={on} onChange={onToggle} />
    </div>
  )

  return (
    <div className="flex-1 flex flex-col min-h-0">
      <div className="flex-none px-10 pt-9 pb-5 border-b border-line">
        <div className="max-w-[1080px] mx-auto">
          <h2 className="m-0 text-[22px] font-bold tracking-tight">插件</h2>
          <p className="mt-1.5 text-muted text-[13px]">管理工具、子代理、技能与 MCP · 关闭的不传给模型</p>
          <div className="flex items-center gap-3 mt-5">
            <div className="flex items-center gap-1">
              {tabs.map((t) => (
                <button
                  key={t.k}
                  type="button"
                  onClick={() => setTab(t.k)}
                  className={`px-3.5 py-1.5 rounded-[9px] text-[13.5px] transition ${tab === t.k ? 'bg-surface2 text-ink font-medium' : 'text-muted hover:text-ink'}`}
                >
                  {t.label}{t.n !== undefined && <span className="ml-1.5 text-faint text-[12px]">{t.n}</span>}
                </button>
              ))}
            </div>
            {showControls && (
              <div className="ml-auto relative">
                <span className="absolute left-3 top-1/2 -translate-y-1/2 text-faint pointer-events-none"><Icon name="search" size={14} /></span>
                <input className="w-[240px] font-sans text-[13px] bg-surface2 text-ink border border-line2 rounded-[10px] pl-9 pr-3 py-2 outline-none focus:border-primary" value={query} onChange={(e) => setQuery(e.target.value)} placeholder="搜索" />
              </div>
            )}
            {tab === 'agents' && <button type="button" className={`${BTN} ${BTN_PRIMARY} px-4 py-2 text-[13px]`} onClick={() => setDetail({ k: 'agent', item: 'new' })}>+ 新建</button>}
            {tab === 'skills' && <button type="button" className={`${BTN} ${BTN_PRIMARY} px-4 py-2 text-[13px]`} onClick={() => setDetail({ k: 'skill', item: 'new' })}>+ 新建</button>}
            {(tab === 'skills' || tab === 'agents') && (
              <div className="relative flex-none">
                <button
                  type="button"
                  onClick={() => setImportMenu((v) => !v)}
                  title={tab === 'skills' ? '导入技能文件夹（含 SKILL.md 及相关文件）' : '从已有的 .md 导入子代理'}
                  className={`${BTN} px-4 py-2 text-[13px] inline-flex items-center gap-1.5 whitespace-nowrap`}
                >
                  <Icon name="book" size={15} /> 导入
                  <Icon name="chevron-down" size={12} />
                </button>
                <Popover open={importMenu} onClose={() => setImportMenu(false)} placement="down-right" className="w-[260px]">
                  <div onClick={() => void doImport('project')} className="px-3.5 py-2 cursor-pointer hover:bg-surface2">
                    <div className="text-[13px] text-ink">导入到本项目</div>
                    <div className="text-[11.5px] text-faint mt-0.5">仅当前工作区可用</div>
                  </div>
                  <div onClick={() => void doImport('user')} className="px-3.5 py-2 cursor-pointer hover:bg-surface2">
                    <div className="text-[13px] text-ink">导入到全局(用户级)</div>
                    <div className="text-[11.5px] text-faint mt-0.5">所有项目都可用</div>
                  </div>
                </Popover>
              </div>
            )}
          </div>
          {showControls && (
            <div className="flex items-center gap-3 mt-4 text-[12.5px]">
              <span className="text-muted">停用范围</span>
              <div className="inline-flex rounded-[9px] border border-line2 overflow-hidden">
                {scopeBtn('project', '本项目')}
                {scopeBtn('user', '全局(用户级)')}
              </div>
              <span className="text-faint">下次新建会话生效{scope === 'user' ? ' · 项目级条目请切到「本项目」范围管理' : ''}</span>
            </div>
          )}
          {err && <p className="mt-3 text-red text-[12.5px]">{err}</p>}
        </div>
      </div>

      {tab === 'mcp' ? (
        <MCPPage />
      ) : (
        <div className="flex-1 overflow-y-auto px-10 py-6">
          <div className="max-w-[1080px] mx-auto flex flex-col gap-3">
            {tab === 'tools' && shownTools.map((t) => {
              const z = TOOL_ZH[t.name]
              return card('t/' + t.name, TOOL_ICON[t.name] ?? 'grid', z?.label ?? t.name, z ? t.name : '', t.source === 'mcp' ? (t.server ?? 'MCP') : '', z?.desc ?? t.description, scopeOn(t.disabledUser, t.disabledProject), otherOffLabel(t.disabledUser, t.disabledProject), (n) => toggleTool(t.name, n))
            })}
            {tab === 'agents' && shownAgents.map((a) => {
              const z = a.source === 'builtin' ? AGENT_ZH[a.name] : undefined
              const badge = sourceLabel(a.source)
              // 内置子代理不可查看详情(只读)；用户/项目的点击进入编辑。所有子代理都可「使用」。
              const onClick = a.source === 'builtin' ? undefined : () => setDetail({ k: 'agent', item: a })
              return card('a/' + a.source + '/' + a.name, TOOL_ICON[a.name] ?? 'bot', z?.label ?? a.name, z ? a.name : '', badge, z?.desc ?? a.description, scopeOn(a.disabledUser, a.disabledProject), otherOffLabel(a.disabledUser, a.disabledProject), (n) => toggleAgent(a.name, n), onClick, () => onUseAgent(a.name))
            })}
            {tab === 'skills' && shownSkills.map((s) => {
              const badge = sourceLabel(s.source)
              return card('s/' + s.source + '/' + s.name, 'book', s.name, '', badge, s.description, scopeOn(s.disabledUser, s.disabledProject), otherOffLabel(s.disabledUser, s.disabledProject), (n) => toggleSkill(s.name, n), () => setDetail({ k: 'skill', item: s }))
            })}
            {tab === 'tools' && shownTools.length === 0 && <div className="text-center text-muted text-[13px] py-16">{q ? '没有匹配的工具' : '还没有自定义工具（内置工具已隐藏，模型仍可使用；连接 MCP 服务器可在此管理其工具）'}</div>}
            {tab === 'agents' && shownAgents.length === 0 && <div className="text-center text-muted text-[13px] py-16">{q ? '没有匹配的子代理' : '还没有自定义子代理，点右上「新建」创建（内置子代理已隐藏，仍可在对话中委派）'}</div>}
            {tab === 'skills' && shownSkills.length === 0 && <div className="text-center text-muted text-[13px] py-16">还没有技能，点右上「新建」创建，或「导入」一个已有的技能文件夹（含 SKILL.md 及相关文件）</div>}
          </div>
        </div>
      )}
    </div>
  )
}

// ModelSelect is the settings-page trigger around the shared ModelPickerPopover:
// a FIELD_CLS-styled button showing the current value that opens the searchable
// platform + custom model list. allowCustom / clearLabel pass straight through.
function ModelSelect({ value, options, onPick, placeholder, allowCustom, clearLabel, disabled }: {
  value: string
  options: ModelOption[]
  onPick: (id: string) => void
  placeholder: string
  allowCustom?: boolean
  clearLabel?: string
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        className={`${FIELD_CLS} w-full flex items-center text-left ${disabled ? '' : 'cursor-pointer hover:border-primary/60'} transition-colors`}
      >
        <span className={`flex-1 truncate ${value ? 'font-mono' : 'text-faint'}`}>{value || placeholder}</span>
        <Icon name="chevron-down" size={16} className="text-faint flex-none ml-2" />
      </button>
      <ModelPickerPopover
        open={open}
        onClose={() => setOpen(false)}
        placement="down-full"
        className="max-h-[320px]"
        options={options}
        current={value}
        allowCustom={allowCustom}
        clearLabel={clearLabel}
        onPick={onPick}
      />
    </div>
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
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState('')
  // 自定义模型（直连接入点）在设置里集中管理；开始页模型选择器会列出它们。
  const [customModels, setCustomModels] = useState<CustomModel[]>([])
  const [cmName, setCmName] = useState('')
  const [cmModel, setCmModel] = useState('')
  const [cmBaseURL, setCmBaseURL] = useState('')
  const [cmApiKey, setCmApiKey] = useState('')
  // 联网工具(WebSearch/WebFetch)的代理，与模型出口无关。
  const [proxy, setProxy] = useState('')
  const [proxyMsg, setProxyMsg] = useState('')
  useEffect(() => {
    void (async () => {
      try { setCustomModels((await listCustomModels()) ?? []) } catch { /* ignore */ }
      try { setProxy((await webProxy()) ?? '') } catch { /* ignore */ }
    })()
  }, [])
  // 账号：登录态 + 租户切换。
  const [passport, setPassport] = useState<PassportStatus>({ loggedIn: false })
  const [tenants, setTenants] = useState<PassportTenant[]>([])
  const [tenantId, setTenantId] = useState('')
  const [platformModels, setPlatformModels] = useState<PassportModel[]>([])
  const [loggingIn, setLoggingIn] = useState(false)
  const [acctMsg, setAcctMsg] = useState('')
  const loadPlatformModels = async (tid: string) => {
    if (!tid) { setPlatformModels([]); return }
    try { setPlatformModels((await passportModels(tid)) ?? []) } catch { setPlatformModels([]) }
  }
  const refreshAccount = async () => {
    try {
      const st = await passportStatus()
      setPassport(st)
      if (st.loggedIn) {
        setTenants((await passportTenants()) ?? [])
        const tid = await activeTenant()
        setTenantId(tid)
        await loadPlatformModels(tid)
      } else { setTenants([]); setPlatformModels([]) }
    } catch { /* ignore */ }
  }
  useEffect(() => {
    void refreshAccount()
    return onEvent(Events.PassportChanged, () => void refreshAccount())
  }, [])
  const doAcctLogin = async (scheme: string) => {
    setLoggingIn(true); setAcctMsg('')
    try { await passportLogin(scheme); await refreshAccount() } catch (e) { setAcctMsg(errText(e)) } finally { setLoggingIn(false) }
  }
  const onSwitchTenant = async (tid: string) => {
    setTenantId(tid); setAcctMsg('')
    try { await setActiveTenant(tid); await loadPlatformModels(tid) } catch (e) { setAcctMsg(errText(e)) }
  }
  // The searchable model pickers (会话模型 + 判定模型) share the same candidate list:
  // this tenant's platform models plus the local custom models.
  const modelOpts: ModelOption[] = [
    ...platformModels.map((m): ModelOption => ({ id: m.id, label: m.id, sub: m.ownedBy, kind: 'platform' })),
    ...customModels.map((m): ModelOption => ({ id: m.model, label: m.name, sub: m.model, kind: 'custom' })),
  ]

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
        apiKey: initial.apiKey ?? '',
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
        tenantId: '',
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

        <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
          <div className="text-[13px] font-semibold text-ink">账号(通行证)</div>
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
          {passport.loggedIn && tenants.length > 0 && (
            <label className={LABEL_CLS}>租户(切换后下次新建会话生效)
              <SelectField value={tenantId} onChange={(v) => void onSwitchTenant(v)}>
                {!tenants.some((t) => t.id === tenantId) && <option value={tenantId}>{tenantId || '(令牌自带租户)'}</option>}
                {tenants.map((t) => <option key={t.id} value={t.id}>{t.name}（{t.id}）</option>)}
              </SelectField>
            </label>
          )}
          {acctMsg && <div className="text-red text-[12.5px]">{acctMsg}</div>}
        </section>

        <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
          <div className="text-[13px] font-semibold text-ink">会话</div>
          <div className={LABEL_CLS}>模型
            <ModelSelect value={model} options={modelOpts} onPick={setModel} placeholder="选择或搜索模型…" allowCustom />
          </div>
          <label className={LABEL_CLS}>权限模式
            <SelectField value={permissionMode} onChange={setPermissionMode}>
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
            <ModelSelect value={harmJudgeModel} options={modelOpts} onPick={setHarmJudgeModel} placeholder="留空 = 默认独立模型（如 claude-haiku-4-5）" allowCustom clearLabel="留空 = 默认独立模型" />
          </div>
          <label className={LABEL_CLS}>判定表决（多次独立判定取多数，更稳但更费 token）
            <SelectField value={String(harmJudgeVotes)} onChange={(v) => setHarmJudgeVotes(parseInt(v, 10))}>
              <option value="1">单次（默认）</option>
              <option value="3">3 次取多数</option>
              <option value="5">5 次取多数</option>
            </SelectField>
          </label>
        </section>

        <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
          <div className="flex items-center justify-between">
            <div className="text-[13px] font-semibold text-ink">自定义模型</div>
            <span className="text-[11.5px] text-faint">直连接入点，开始页可选</span>
          </div>
          <p className="text-[12px] text-muted -mt-1.5">除通行证平台模型外，可在此添加自备的 OpenAI 兼容模型（各自带 Base URL 与密钥）。</p>
          {customModels.length > 0 && (
            <div className="flex flex-col gap-1.5">
              {customModels.map((m) => (
                <div key={m.name} className="flex items-center justify-between rounded-[9px] border border-line2 bg-surface2 px-3 py-2 text-[12.5px]">
                  <span className="truncate">{m.name} <span className="text-muted">· {m.model}</span> <span className="text-faint font-mono text-[11px]">{m.baseURL}</span></span>
                  <button type="button" className="text-muted hover:text-red flex-none ml-2" onClick={async () => setCustomModels((await deleteCustomModel(m.name)) ?? [])}>删除</button>
                </div>
              ))}
            </div>
          )}
          <div className="flex flex-col gap-2 rounded-[11px] border border-dashed border-line2 p-3">
            <input className={FIELD_CLS} placeholder="显示名称（如 本地 Ollama）" value={cmName} onChange={(e) => setCmName(e.target.value)} />
            <div className="grid grid-cols-2 gap-2">
              <input className={FIELD_CLS} placeholder="模型 ID" value={cmModel} onChange={(e) => setCmModel(e.target.value)} />
              <input className={FIELD_CLS} placeholder="Base URL（.../v1）" value={cmBaseURL} onChange={(e) => setCmBaseURL(e.target.value)} />
            </div>
            <input className={FIELD_CLS} type="password" placeholder="API 密钥（可空）" value={cmApiKey} onChange={(e) => setCmApiKey(e.target.value)} />
            <button type="button" className={`${BTN} self-start px-5`} disabled={!cmName.trim() || !cmModel.trim()} onClick={async () => {
              const list = await saveCustomModel({ name: cmName.trim(), model: cmModel.trim(), baseURL: cmBaseURL.trim(), apiKey: cmApiKey })
              setCustomModels(list ?? []); setCmName(''); setCmModel(''); setCmBaseURL(''); setCmApiKey('')
            }}>添加自定义模型</button>
          </div>
        </section>

        <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
          <div className="flex items-center justify-between">
            <div className="text-[13px] font-semibold text-ink">联网工具代理</div>
            <span className="text-[11.5px] text-faint">仅 WebSearch / WebFetch</span>
          </div>
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
        </section>

        <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
          <div className="flex items-center justify-between">
            <div className="text-[13px] font-semibold text-ink">上下文长度控制</div>
            <span className="text-[11.5px] text-faint">下次新建会话生效</span>
          </div>
          <label className={LABEL_CLS}>最大输出 Tokens<input className={FIELD_CLS} type="number" value={maxTokens} onChange={(e) => setMaxTokens(e.target.value)} placeholder="留空则用默认 16384" /></label>
          <label className={LABEL_CLS}>上下文预算（超出后自动总结压缩较早对话；磁盘记录保持完整）
            <SelectField value={String(maxContextTokens)} onChange={(v) => setMaxContextTokens(parseInt(v, 10))}>
              <option value="0">关闭 · 不自动压缩</option>
              <option value="32000">32K · 省 token</option>
              <option value="128000">128K · 推荐</option>
              <option value="200000">200K · 大窗口</option>
            </SelectField>
          </label>
          <label className={LABEL_CLS}>历史消息上限（硬截断，仅保留最近 N 条；留空关闭）
            <input className={FIELD_CLS} type="number" value={maxHistoryMessages} onChange={(e) => setMaxHistoryMessages(e.target.value)} placeholder="留空 = 不截断（推荐优先用上面的自动压缩）" />
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

// PERM_CHIP maps a permission outcome to its label + color. 'auto' is an allow the
// smart mode grants without asking (workspace mutations); it reads green with an
// "自动" tag so it's distinct from a plain allow.
const PERM_CHIP: Record<string, { label: string; cls: string; auto?: boolean }> = {
  allow: { label: '允许', cls: 'text-green bg-green/12' },
  ask: { label: '询问', cls: 'text-amber bg-amber/15' },
  judge: { label: '审查', cls: 'text-primary bg-primary/12' },
  deny: { label: '拒绝', cls: 'text-red bg-red/12' },
  auto: { label: '允许', cls: 'text-green bg-green/12', auto: true },
}

function PermChip({ kind }: { kind: string }) {
  const c = PERM_CHIP[kind] ?? PERM_CHIP.deny
  return (
    <span className={`inline-flex items-center justify-center gap-1 text-[12.5px] font-medium px-3 py-[5px] rounded-full whitespace-nowrap ${c.cls}`}>
      {c.label}
      {c.auto && <span className="font-mono text-[9px] tracking-wide opacity-70">自动</span>}
    </span>
  )
}

// level is how much a mode lets through (1 = most guarded … 4 = fully open); tone is
// its risk color. Together they read as a spectrum from safe/green to flight/red.
const PERM_MODES = [
  { key: 'safe', zh: '安全', en: 'safe', level: 1, tone: 'bg-green', essence: '只放行绝对安全的,其余一律拒绝,从不打扰你。' },
  { key: 'interactive', zh: '交互', en: 'interactive', level: 2, tone: 'bg-primary', essence: '读取自动放行,写改 / 命令 / 联网逐项当面问你。' },
  { key: 'judge', zh: '智能', en: 'judge', level: 3, tone: 'bg-amber', essence: '工作区改动直接放行,命令与联网交模型审查。' },
  { key: 'flight', zh: '飞行', en: 'flight', level: 4, tone: 'bg-red', essence: '全部放行,连高危命令也不拦,不审计。' },
]

// PERM_ROWS is the operation × mode matrix, verified against internal/permissions.
// cells are ordered [安全, 交互, 智能, 飞行].
const PERM_ROWS: { op: string; q: string; cells: string[] }[] = [
  { op: '读取', q: 'Read · Grep · Glob', cells: ['allow', 'allow', 'allow', 'allow'] },
  { op: '待办 / 记忆', q: 'TodoWrite · Memory', cells: ['allow', 'allow', 'allow', 'allow'] },
  { op: '写 / 改 · 已读文件', q: 'Write · Edit', cells: ['deny', 'ask', 'auto', 'allow'] },
  { op: '写 / 改 · 没读就覆盖', q: '读态拦截 · 可恢复', cells: ['deny', 'deny', 'auto', 'allow'] },
  { op: '删除 · 工作区内', q: 'Delete', cells: ['deny', 'ask', 'auto', 'allow'] },
  { op: '命令', q: 'Bash', cells: ['deny', 'ask', 'judge', 'allow'] },
  { op: '联网', q: 'WebFetch · WebSearch', cells: ['deny', 'ask', 'judge', 'allow'] },
  { op: 'MCP 外部工具', q: '独立进程', cells: ['deny', 'ask', 'judge', 'allow'] },
  { op: '越出工作区 / 高危命令', q: '提权 · rm -rf · 毁灭性 git', cells: ['deny', 'deny', 'deny', 'allow'] },
]

const PERM_CHOICES: [string, string, boolean][] = [
  ['本次会话', '推荐。本会话内同类命令、本项目文件改动都不再问。', false],
  ['仅此一次', '只放行这一次,下次遇到同样的动作还会再问。', false],
  ['本项目', '对这个项目永久记住这条放行。', false],
  ['拒绝', '拒绝并停止本回合,把控制权交回给你,模型不会重试。', true],
]

const PERM_RULES: [string, string, string][] = [
  ['计划模式', 'text-primaryink bg-primarysoft', '规划阶段任何会改动东西的动作都被拒绝,让 AI 先想清楚——与四种模式叠加。'],
  ['危害审查', 'text-amber bg-amber/15', '智能模式对命令 / 联网 / MCP 先判危害:安全放行,有害弹窗并附原因,评估失败也弹窗,绝不静默放行。'],
  ['并发队列', 'text-green bg-greenbg', '只读类工具并行执行;多个授权请求排队逐个弹出、互不覆盖;批次里任一被拒绝同样停止本回合。'],
]

// AutonomyMeter renders a mode's "how much it lets through" as four segments filled
// to its level — the page's one signature element, and true to the content.
function AutonomyMeter({ level, tone }: { level: number; tone: string }) {
  return (
    <div className="flex gap-1" title={`自主度 ${level} / 4`} aria-label={`自主度 ${level} / 4`}>
      {[1, 2, 3, 4].map((i) => (
        <span key={i} className={`w-4 h-1.5 rounded-full ${i <= level ? tone : 'bg-line2'}`} />
      ))}
    </div>
  )
}

// PermissionsPage: pick a mode (the hero), then scan the operation × mode matrix for
// exactly what each mode does. Clicking a mode switches the live session to it.
export function PermissionsPage({ mode, onPickMode }: { mode?: string; onPickMode: (m: string) => void }) {
  return (
    <div className="flex-1 overflow-y-auto px-[26px] py-9">
      <div className="max-w-[920px] mx-auto flex flex-col gap-9">
        <div>
          <h2 className="m-0 text-[24px] font-bold tracking-tight">权限</h2>
          <p className="mt-3 text-[15px] text-muted leading-[1.8] max-w-[60ch]">
            每次 AI 调用工具都走同一套判定:<b className="text-ink font-semibold">策略先看动作本身</b>,给出「允许 / 询问 / 拒绝」;再由你选的<b className="text-ink font-semibold">模式决定「询问」如何落地</b>。所以读工作区文件永远放行、提权命令永远拒绝——真正被模式改写的,只是那些需要询问的动作。
          </p>
        </div>

        {/* Modes — the hero. The autonomy meter (1–4 bars) reads left→right as a
            spectrum from most guarded (安全) to fully open (飞行). */}
        <section className="flex flex-col gap-4">
          <div className="flex items-baseline gap-3">
            <span className="text-[15px] font-semibold text-ink">选择模式</span>
            <span className="text-[13px] text-faint">点一下即切换当前会话</span>
          </div>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-3.5">
            {PERM_MODES.map((m) => {
              const active = mode === m.key
              return (
                <button
                  key={m.key}
                  onClick={() => onPickMode(m.key)}
                  className={`text-left rounded-[16px] border p-[22px] bg-surface transition-all duration-150 ${active ? 'border-primary shadow-[0_0_0_3px_var(--color-primarysoft)]' : 'border-line2 hover:shadow-[0_5px_18px_rgba(30,35,60,0.08)] hover:-translate-y-0.5'}`}
                >
                  <div className="flex items-center justify-between h-6 mb-4">
                    <AutonomyMeter level={m.level} tone={m.tone} />
                    {active && <span className="text-[11px] font-semibold text-white bg-primary rounded-full px-2.5 py-1 leading-none tracking-wide">当前</span>}
                  </div>
                  <div className="flex items-baseline gap-2">
                    <span className="text-[19px] font-bold tracking-tight text-ink">{m.zh}</span>
                    <span className="font-mono text-[11.5px] text-faint">{m.en}</span>
                  </div>
                  <div className="text-[14px] text-muted mt-2.5 leading-[1.7]">{m.essence}</div>
                </button>
              )
            })}
          </div>
        </section>

        {/* Matrix — the reference. The chosen mode's column is tinted so its column
            reads at a glance. */}
        <section className="flex flex-col gap-4">
          <div className="flex items-baseline justify-between flex-wrap gap-y-2 gap-x-4">
            <span className="text-[15px] font-semibold text-ink">各模式如何处理每类操作</span>
            <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[12.5px] text-muted">
              <span className="inline-flex items-center gap-1.5"><i className="w-2.5 h-2.5 rounded-full bg-green" />允许</span>
              <span className="inline-flex items-center gap-1.5"><i className="w-2.5 h-2.5 rounded-full bg-amber" />询问</span>
              <span className="inline-flex items-center gap-1.5"><i className="w-2.5 h-2.5 rounded-full bg-primary" />智能审查</span>
              <span className="inline-flex items-center gap-1.5"><i className="w-2.5 h-2.5 rounded-full bg-red" />拒绝</span>
            </div>
          </div>
          <div className="overflow-x-auto rounded-[16px] border border-line2 bg-surface">
            <table className="w-full border-collapse text-[14px] min-w-[620px]">
              <thead>
                <tr>
                  <th className="text-left font-medium text-[13px] text-faint px-6 py-4">操作</th>
                  {PERM_MODES.map((m) => {
                    const on = m.key === mode
                    return (
                      <th key={m.key} className={`px-3 py-4 text-center align-bottom ${on ? 'bg-primarysoft/50' : ''}`}>
                        <div className={`text-[14.5px] font-semibold ${on ? 'text-primaryink' : 'text-ink'}`}>{m.zh}</div>
                        <div className="h-[15px] mt-0.5">{on && <span className="text-[10px] font-mono text-primary">当前</span>}</div>
                      </th>
                    )
                  })}
                </tr>
              </thead>
              <tbody>
                {PERM_ROWS.map((r) => (
                  <tr key={r.op} className="border-t border-line">
                    <td className="px-6 py-4">
                      <div className="text-ink text-[14px]">{r.op}</div>
                      <div className="font-mono text-[11.5px] text-faint mt-1">{r.q}</div>
                    </td>
                    {r.cells.map((c, ci) => (
                      <td key={ci} className={`px-3 py-4 text-center ${PERM_MODES[ci].key === mode ? 'bg-primarysoft/40' : ''}`}>
                        <PermChip kind={c} />
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        {/* Footer — quiet: what a prompt offers, plus the cross-cutting rules. */}
        <section className="grid grid-cols-1 md:grid-cols-2 gap-3.5">
          <div className="rounded-[16px] border border-line2 bg-surface p-6">
            <div className="text-[15px] font-semibold text-ink mb-4">被询问时,你能选</div>
            <div className="flex flex-col gap-3">
              {PERM_CHOICES.map(([k, d, stop]) => (
                <div key={k} className="flex gap-3 items-start">
                  <span className={`font-mono text-[12.5px] font-medium rounded-[7px] px-2.5 py-1 min-w-[74px] text-center flex-none border ${stop ? 'text-red border-red/35 bg-red/[0.06]' : 'text-ink bg-surface2 border-line2'}`}>{k}</span>
                  <span className="text-[14px] text-muted leading-[1.7]">{d}</span>
                </div>
              ))}
            </div>
          </div>
          <div className="rounded-[16px] border border-line2 bg-surface p-6">
            <div className="text-[15px] font-semibold text-ink mb-4">还有几条连带规则</div>
            <div className="flex flex-col gap-3.5">
              {PERM_RULES.map(([b, cls, d]) => (
                <div key={b} className="flex gap-3 items-start">
                  <span className={`font-mono text-[12px] font-medium rounded-[6px] px-2 py-1 flex-none mt-0.5 whitespace-nowrap ${cls}`}>{b}</span>
                  <span className="text-[14px] text-muted leading-[1.7]">{d}</span>
                </div>
              ))}
            </div>
          </div>
        </section>
      </div>
    </div>
  )
}

// ---- MCP (Model Context Protocol) server management ---------------------------

type MCPDraft = {
  originalName: string
  name: string
  transport: string
  command: string
  argsText: string
  envText: string
  dir: string
  url: string
  headersText: string
  enabled: boolean
}

function kvToText(m?: Record<string, string> | null): string {
  if (!m) return ''
  return Object.entries(m).map(([k, v]) => `${k}=${v}`).join('\n')
}
function textToKV(t: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of t.split('\n')) {
    const s = line.trim()
    if (!s) continue
    const i = s.indexOf('=')
    if (i <= 0) continue
    out[s.slice(0, i).trim()] = s.slice(i + 1).trim()
  }
  return out
}
function linesToArr(t: string): string[] {
  return t.split('\n').map((l) => l.trim()).filter(Boolean)
}
function draftFrom(s?: MCPServerInfo): MCPDraft {
  return {
    originalName: s?.name ?? '',
    name: s?.name ?? '',
    transport: s?.transport ?? 'stdio',
    command: s?.command ?? '',
    argsText: (s?.args ?? []).join('\n'),
    envText: kvToText(s?.env),
    dir: s?.dir ?? '',
    url: s?.url ?? '',
    headersText: kvToText(s?.headers),
    enabled: s?.enabled ?? true,
  }
}

// MCPPage manages the Model Context Protocol servers stored in the shared
// config.toml (the same file the CLI reads), and shows each server's live
// connection state from the running session.
export function MCPPage() {
  const [servers, setServers] = useState<MCPServerInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState<MCPDraft | null>(null)
  const [saving, setSaving] = useState(false)
  const mono = FIELD_CLS + ' font-mono text-[12.5px] leading-relaxed'

  async function refresh() {
    setLoading(true)
    setError('')
    try {
      setServers((await listMCPServers()) ?? [])
    } catch (e) {
      setError(errText(e))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void refresh()
  }, [])

  async function save() {
    if (!draft) return
    const input: MCPServerInput = {
      originalName: draft.originalName,
      name: draft.name.trim(),
      transport: draft.transport,
      command: draft.command.trim(),
      args: linesToArr(draft.argsText),
      env: textToKV(draft.envText),
      dir: draft.dir.trim(),
      url: draft.url.trim(),
      headers: textToKV(draft.headersText),
      enabled: draft.enabled,
    }
    setSaving(true)
    setError('')
    try {
      await saveMCPServer(input)
      setDraft(null)
      await refresh()
    } catch (e) {
      setError(errText(e))
    } finally {
      setSaving(false)
    }
  }
  async function remove(name: string) {
    setError('')
    try {
      await deleteMCPServer(name)
      await refresh()
    } catch (e) {
      setError(errText(e))
    }
  }
  async function toggle(s: MCPServerInfo) {
    setError('')
    try {
      await setMCPServerEnabled(s.name, !s.enabled)
      await refresh()
    } catch (e) {
      setError(errText(e))
    }
  }

  const set = (patch: Partial<MCPDraft>) => setDraft((d) => (d ? { ...d, ...patch } : d))

  return (
    <div className="flex-1 overflow-y-auto px-[22px] py-7">
      <div className="max-w-[720px] mx-auto flex flex-col gap-5">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 className="m-0 text-[20px] font-bold tracking-tight">MCP 服务器</h2>
            <p className="mt-1 text-muted text-[13px]">连接外部工具服务器(Model Context Protocol)。与命令行共用同一份 <code className="font-mono text-[12px] bg-surface2 px-1 py-0.5 rounded">config.toml</code>,更改在<b className="text-ink font-semibold">下次新建会话</b>时生效。</p>
          </div>
          {!draft && (
            <button className={`${BTN} ${BTN_PRIMARY} flex-none`} onClick={() => setDraft(draftFrom())}>
              <Icon name="plus" size={15} /> 新建
            </button>
          )}
        </div>

        {error && <div className="text-red text-[13px] bg-redbg border border-red/25 rounded-lg px-3 py-2.5 whitespace-pre-wrap break-words">{error}</div>}

        {draft ? (
          <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-3.5 shadow-xs">
            <div className="text-[14px] font-semibold">{draft.originalName ? `编辑 ${draft.originalName}` : '新建 MCP 服务器'}</div>
            <div className="grid grid-cols-2 gap-3">
              <label className={LABEL_CLS}>名称<input className={FIELD_CLS} value={draft.name} onChange={(e) => set({ name: e.target.value })} placeholder="例如 filesystem" /></label>
              <label className={LABEL_CLS}>传输方式
                <select className={FIELD_CLS} value={draft.transport} onChange={(e) => set({ transport: e.target.value })}>
                  <option value="stdio">stdio(本地进程)</option>
                  <option value="http">http(远程端点)</option>
                </select>
              </label>
            </div>
            {draft.transport === 'http' ? (
              <>
                <label className={LABEL_CLS}>URL<input className={FIELD_CLS} value={draft.url} onChange={(e) => set({ url: e.target.value })} placeholder="https://example.com/mcp" /></label>
                <label className={LABEL_CLS}>请求头(每行 KEY=VALUE,值可用 ${'{ENV_VAR}'})<textarea className={mono} rows={2} value={draft.headersText} onChange={(e) => set({ headersText: e.target.value })} placeholder="Authorization=Bearer ${TOKEN}" /></label>
              </>
            ) : (
              <>
                <label className={LABEL_CLS}>命令<input className={FIELD_CLS} value={draft.command} onChange={(e) => set({ command: e.target.value })} placeholder="npx" /></label>
                <label className={LABEL_CLS}>参数(每行一个)<textarea className={mono} rows={3} value={draft.argsText} onChange={(e) => set({ argsText: e.target.value })} placeholder={"-y\n@modelcontextprotocol/server-filesystem"} /></label>
                <div className="grid grid-cols-2 gap-3">
                  <label className={LABEL_CLS}>环境变量(每行 KEY=VALUE)<textarea className={mono} rows={2} value={draft.envText} onChange={(e) => set({ envText: e.target.value })} placeholder="TOKEN=${MY_TOKEN}" /></label>
                  <label className={LABEL_CLS}>工作目录(可选)<input className={FIELD_CLS} value={draft.dir} onChange={(e) => set({ dir: e.target.value })} placeholder="留空则用工作区" /></label>
                </div>
              </>
            )}
            <label className="flex items-center gap-2.5 text-[13px] text-ink cursor-pointer select-none">
              <input type="checkbox" checked={draft.enabled} onChange={(e) => set({ enabled: e.target.checked })} className="w-4 h-4 accent-[var(--color-primary)]" />
              启用(会话启动时连接此服务器)
            </label>
            <div className="text-[12px] text-faint -mt-1">密钥请用 <code className="font-mono bg-surface2 px-1 py-0.5 rounded">${'{ENV_VAR}'}</code> 引用,只把变量名写进配置文件,明文密钥留在环境变量里。</div>
            <div className="flex gap-2.5 mt-1">
              <button className={`${BTN} ${BTN_PRIMARY}`} disabled={!draft.name.trim() || saving} onClick={save}>{saving ? '保存中…' : '保存'}</button>
              <button className={BTN} onClick={() => { setDraft(null); setError('') }}>取消</button>
            </div>
          </section>
        ) : loading ? (
          <div className="text-muted text-[13px] py-6 text-center">加载中…</div>
        ) : servers.length === 0 ? (
          <div className="bg-surface border border-line2 border-dashed rounded-[14px] px-5 py-10 text-center">
            <div className="text-muted text-[14px]">还没有配置 MCP 服务器</div>
            <div className="text-faint text-[12.5px] mt-1">点右上角「新建」接入一个外部工具服务器(如文件系统、浏览器、数据库)。</div>
          </div>
        ) : (
          <div className="flex flex-col gap-2.5">
            {servers.map((s) => (
              <div key={s.name} className="bg-surface border border-line2 rounded-[14px] p-4 shadow-xs flex flex-col gap-2">
                <div className="flex items-center gap-2.5">
                  <span className={`w-2 h-2 rounded-full flex-none ${!s.enabled ? 'bg-faint' : s.connected ? 'bg-green' : 'bg-amber'}`} />
                  <span className="font-semibold text-[14.5px]">{s.name}</span>
                  <span className="font-mono text-[10.5px] uppercase tracking-wide text-muted bg-surface2 border border-line2 rounded px-1.5 py-0.5">{s.transport}</span>
                  <span className="text-[12px] text-muted ml-1">
                    {!s.enabled ? '已停用' : s.connected ? `已连接 · ${s.toolCount} 个工具` : '未连接'}
                  </span>
                  <div className="ml-auto flex items-center gap-1.5">
                    <button className={`${BTN} px-2.5 py-1 text-[12.5px]`} onClick={() => toggle(s)}>{s.enabled ? '停用' : '启用'}</button>
                    <button className={`${BTN} px-2.5 py-1 text-[12.5px]`} onClick={() => setDraft(draftFrom(s))}>编辑</button>
                    <button className={`${BTN} ${BTN_DANGER} px-2.5 py-1 text-[12.5px]`} onClick={() => remove(s.name)}>删除</button>
                  </div>
                </div>
                <div className="font-mono text-[12px] text-muted break-all pl-[18px]">
                  {s.transport === 'http' ? s.url : [s.command, ...(s.args ?? [])].filter(Boolean).join(' ')}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

// MemoryPage shows the two forms of persistent context: the workspace's project
// instructions (CLAUDE.md / RUNCODE.md, editable) and the agent's memory (read-only
// — the model maintains it via its memory tool).
export function MemoryPage() {
  const [ctx, setCtx] = useState<ProjectContextInfo | null>(null)
  const [content, setContent] = useState('')
  const [mem, setMem] = useState<MemoryInfo>({ user: [], project: [] })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    void (async () => {
      try {
        const [c, m] = await Promise.all([readProjectContext(), readMemory()])
        setCtx(c)
        setContent(c?.content ?? '')
        setMem({ user: m?.user ?? [], project: m?.project ?? [] })
      } catch (e) {
        setError(errText(e))
      } finally {
        setLoading(false)
      }
    })()
  }, [])

  async function save() {
    setSaving(true)
    setError('')
    try {
      await saveProjectContext(content)
      setCtx((c) => (c ? { ...c, content, exists: true } : c))
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
    } catch (e) {
      setError(errText(e))
    } finally {
      setSaving(false)
    }
  }

  const hasWorkspace = !!ctx && ctx.path !== ''
  const dirty = !!ctx && content !== ctx.content
  const memSection = (title: string, note: string, raw: string[] | null | undefined) => {
    const entries = raw ?? []
    return (
    <div>
      <div className="flex items-baseline gap-2 mb-1.5">
        <span className="text-[13px] font-semibold text-ink">{title}</span>
        <span className="text-[12px] text-faint">{entries.length}</span>
        <span className="text-[11.5px] text-faint ml-auto">{note}</span>
      </div>
      {entries.length === 0 ? (
        <div className="text-faint text-[12.5px] py-2">暂无</div>
      ) : (
        <ul className="flex flex-col gap-1.5 m-0 p-0 list-none">
          {entries.map((e, i) => (
            <li key={i} className="text-[12.5px] text-ink bg-surface2 border border-line2 rounded-lg px-3 py-2 leading-relaxed break-words">{e}</li>
          ))}
        </ul>
      )}
    </div>
    )
  }

  return (
    <div className="flex-1 overflow-y-auto px-[22px] py-7">
      <div className="max-w-[720px] mx-auto flex flex-col gap-5">
        <div>
          <h2 className="m-0 text-[20px] font-bold tracking-tight">记忆与项目指令</h2>
          <p className="mt-1 text-muted text-[13px]">项目指令随每次对话注入给模型;记忆由模型在跨会话时自行记录。</p>
        </div>

        {error && <div className="text-red text-[13px] bg-redbg border border-red/25 rounded-lg px-3 py-2.5 whitespace-pre-wrap break-words">{error}</div>}

        {loading ? (
          <div className="text-muted text-[13px] py-6 text-center">加载中…</div>
        ) : (
          <>
            <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-3 shadow-xs">
              <div className="flex items-center gap-2">
                <span className="text-[14px] font-semibold text-ink">项目指令</span>
                {ctx && <span className="font-mono text-[11.5px] text-faint bg-surface2 border border-line2 rounded px-1.5 py-0.5">{ctx.name}{!ctx.exists ? ' · 新建' : ''}</span>}
                <div className="ml-auto flex items-center gap-2">
                  {saved && <span className="text-[12px] text-green">已保存</span>}
                  <button className={`${BTN} ${BTN_PRIMARY} px-3 py-1.5 text-[13px]`} disabled={!hasWorkspace || !dirty || saving} onClick={save}>{saving ? '保存中…' : '保存'}</button>
                </div>
              </div>
              {hasWorkspace ? (
                <textarea
                  className="font-mono text-[12.5px] leading-relaxed bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2.5 outline-none focus:border-primary focus:shadow-[0_0_0_3px_var(--color-primarysoft)] min-h-[240px] resize-y"
                  value={content}
                  onChange={(e) => setContent(e.target.value)}
                  placeholder="# 项目说明&#10;&#10;写下让 AI 遵守的项目约定、技术栈、协作原则……每次对话都会带上这些。"
                  spellCheck={false}
                />
              ) : (
                <div className="text-faint text-[13px] py-4 text-center">启动一个会话后可编辑项目指令。</div>
              )}
            </section>

            <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-4 shadow-xs">
              <div className="text-[14px] font-semibold text-ink">记忆</div>
              {memSection('用户记忆', '全局 · 跨项目', mem.user)}
              {memSection('项目记忆', '仅本工作区', mem.project)}
              <div className="text-[11.5px] text-faint">记忆由模型通过记忆工具自动维护;如需修改,可在对话中让它更新。</div>
            </section>
          </>
        )}
      </div>
    </div>
  )
}

export function StartForm({ onStart, starting, error, initial }: { onStart: (req: StartSessionRequest) => void; starting: boolean; error: string; initial: Partial<StartSessionRequest> }) {
  const [cwd, setCwd] = useState(initial.cwd ?? '')
  const [passport, setPassport] = useState<PassportStatus>({ loggedIn: false })
  const [tenants, setTenants] = useState<PassportTenant[]>([])
  const [tenantId, setTenantId] = useState(initial.tenantId ?? '')
  const [platformModels, setPlatformModels] = useState<PassportModel[]>([])
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

  // loadModels 拉取指定租户的平台模型；失败必须显式提示（不静默清空）。
  const loadModels = async (tid: string) => {
    try {
      setPlatformModels((await passportModels(tid)) ?? [])
      setPassportError('')
    } catch (e) {
      setPlatformModels([])
      setPassportError(`获取平台模型失败：${errText(e)}`)
    }
  }

  // leafTenantIds 返回可选的末级租户 id 集合：任何被别的租户当作 parentId 的
  // 节点都不是叶子（只能选最后一级）。parentId 里的 "SuperTenant"/"" 等非真实
  // id 从集合里移除也无害。
  const leafTenantIds = (list: PassportTenant[]) => {
    const leaves = new Set(list.map((t) => t.id))
    for (const t of list) if (t.parentId) leaves.delete(t.parentId)
    return leaves
  }

  // loadModels 拉取指定租户的平台模型；失败必须显式提示（不静默清空）。
  const doLoadModels = loadModels

  // refreshPassport re-syncs login status, the tenant tree, the models for the
  // selected leaf tenant, and the local custom models. If exactly one leaf
  // tenant exists it is auto-selected; otherwise the user must pick a leaf
  // before models load. Platform models require a login; custom models are local.
  const refreshPassport = async () => {
    try {
      const st = await passportStatus()
      setPassport(st)
      if (st.loggedIn) {
        let tid = tenantId
        try {
          const list = (await passportTenants()) ?? []
          setTenants(list)
          const leaves = leafTenantIds(list)
          if (!leaves.has(tid)) tid = leaves.size === 1 ? [...leaves][0] : ''
          setTenantId(tid)
        } catch (e) {
          setTenants([])
          setPassportError(`获取租户列表失败：${errText(e)}`)
          tid = ''
        }
        if (tid) await doLoadModels(tid)
        else setPlatformModels([])
      }
    } catch { /* 登录状态读取失败：保持当前状态 */ }
    try { setCustomModels((await listCustomModels()) ?? []) } catch { /* ignore */ }
  }
  useEffect(() => {
    let alive = true
    // Validate the persisted token against the server first (shows the loading
    // gate); only then reveal login screen or form.
    passportValidate()
      .then((st) => {
        if (!alive) return
        setPassport(st)
        if (st.loggedIn) void refreshPassport()
      })
      .catch(() => { /* keep not-logged-in default */ })
      .finally(() => { if (alive) setValidating(false) })
    const off = onEvent(Events.PassportChanged, (st) => {
      setPassport(st)
      if (!st.loggedIn) { setPlatformModels([]); setTenants([]); setTenantId(''); setModelChoice('') }
      else void refreshPassport()
    })
    return () => { alive = false; off() }
  }, [])

  // 用户选定末级租户：重拉该租户模型，清掉可能已失效的平台模型选择。
  const selectTenant = async (tid: string) => {
    setTenantId(tid)
    if (modelChoice.startsWith('passport:')) setModelChoice('')
    await doLoadModels(tid)
  }

  // scheme selects the upstream identity source: 'OneOuchnPassport' for 统一认证,
  // '' for the base passport (基座通行证).
  const doLogin = async (scheme: string) => {
    setLoggingIn(true); setPassportError('')
    try {
      await passportLogin(scheme)
      await refreshPassport()
    } catch (e) {
      setPassportError(errText(e))
    } finally { setLoggingIn(false) }
  }

  // buildRequest maps the selected model onto the wire request. A passport model
  // needs its id + the selected tenant (backend resolves auth/base URL); a custom
  // model resends its stored baseURL/apiKey. The session's permission mode and
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
      if (!passport.loggedIn || !tenantId) return null
      return { ...base, provider: 'passport', model: modelChoice.slice('passport:'.length), tenantId }
    }
    if (modelChoice.startsWith('custom:')) {
      const cm = customModels.find((m) => `custom:${m.name}` === modelChoice)
      if (!cm) return null
      return { ...base, provider: 'openai', model: cm.model, baseURL: cm.baseURL, apiKey: cm.apiKey ?? '' }
    }
    return null
  }

  // 租户树：AI.Core 的租户带 parentId 层级；渲染成缩进树，只有叶子可点选。
  type TenantNode = { t: PassportTenant; children: TenantNode[] }
  const tenantTree: TenantNode[] = (() => {
    const byId = new Map(tenants.map((t) => [t.id, t]))
    const kids = new Map<string, PassportTenant[]>()
    const roots: PassportTenant[] = []
    for (const t of tenants) {
      const pid = t.parentId
      if (pid && pid !== t.id && byId.has(pid)) {
        const arr = kids.get(pid) ?? []
        arr.push(t)
        kids.set(pid, arr)
      } else roots.push(t)
    }
    const build = (t: PassportTenant): TenantNode => ({ t, children: (kids.get(t.id) ?? []).map(build) })
    return roots.map(build)
  })()
  const renderTenantNodes = (nodes: TenantNode[], depth: number): ReactNode[] =>
    nodes.flatMap((n): ReactNode[] => {
      const pad = { paddingLeft: `${8 + depth * 16}px` }
      const leaf = n.children.length === 0
      const row = leaf ? (
        <button
          key={n.t.id}
          type="button"
          onClick={() => void selectTenant(n.t.id)}
          style={pad}
          className={`flex items-center gap-2 w-full text-left pr-2 py-1.5 rounded-[7px] text-[13px] transition-colors ${tenantId === n.t.id ? 'bg-primarysoft text-primary font-medium' : 'hover:bg-surface2 text-ink'}`}
        >
          <span className={`w-[7px] h-[7px] rounded-full flex-none ${tenantId === n.t.id ? 'bg-primary' : 'bg-line2'}`} />
          <span className="truncate">{n.t.name}</span>
        </button>
      ) : (
        <div key={n.t.id} style={pad} className="pr-2 py-1.5 text-[11.5px] text-faint font-medium">{n.t.name}</div>
      )
      return [row, ...renderTenantNodes(n.children, depth + 1)]
    })

  // 已选过工作区（上次会话持久化了 cwd + 模型）且已登录 → 自动进入，
  // 无需再次选择；只触发一次，启动失败(error)时回落到表单让用户处理。
  const autoStarted = useRef(false)
  const [autoEntering, setAutoEntering] = useState(false)
  useEffect(() => {
    if (autoStarted.current || !passport.loggedIn || starting || error) return
    if (!(initial.cwd ?? '').trim()) return
    // 只有上次保存的就是通行证会话才自动进入；旧的手动配置一律显示表单，
    // 让用户能选择租户 + 平台模型（否则会被旧配置直接带进会话，看不到选择界面）。
    if (initial.provider !== 'passport') return
    const req = buildRequest()
    if (!req || !(req.model ?? '').trim()) return
    autoStarted.current = true
    setAutoEntering(true)
    onStart(req)
  }, [passport.loggedIn])

  // 启动校验中：转圈的加载门，验完持久化 token 再决定进登录还是表单。
  if (validating) {
    return (
      <div
        className="relative flex flex-col items-center justify-center flex-1 min-h-0 bg-cover bg-center"
        style={{ backgroundImage: `url(${loginBg})` }}
      >
        <img src={loginMascot} alt="" draggable={false} className="w-[150px] h-auto select-none pointer-events-none opacity-90" />
        {/* 加载环：浅背景上白色不可见，改用品牌蓝——淡蓝底轨 + 蓝色渐变彗尾旋转。
            彗尾用 conic-gradient 填满圆再用 radial mask 抠成 3px 圆环，兼容 WebView2。 */}
        <div className="mt-8 relative w-11 h-11">
          <div className="absolute inset-0 rounded-full border-[3px]" style={{ borderColor: 'rgba(32,80,216,0.14)' }} />
          <div
            className="absolute inset-0 rounded-full animate-spin"
            style={{
              background:
                'conic-gradient(from 0deg, rgba(32,80,216,0) 0deg, rgba(63,123,255,0.4) 150deg, #3f7bff 300deg, #2050d8 360deg)',
              WebkitMaskImage: 'radial-gradient(farthest-side, transparent calc(100% - 3px), #000 calc(100% - 3px))',
              maskImage: 'radial-gradient(farthest-side, transparent calc(100% - 3px), #000 calc(100% - 3px))',
            }}
          />
        </div>
        <div className="mt-5 text-[14px] tracking-wide animate-pulse" style={{ color: '#2050d8' }}>正在验证登录状态…</div>
      </div>
    )
  }

  // 未登录：整屏登录门——背景 + 吉祥物 + 标语 + 两个登录入口(统一认证 / 基座通行证)。
  // 工作区/模型等表单只在登录成功后出现。
  if (!passport.loggedIn) {
    return (
      <div
        className="relative flex flex-col items-center justify-center flex-1 min-h-0 bg-cover bg-center"
        style={{ backgroundImage: `url(${loginBg})` }}
      >
        <img src={loginMascot} alt="" draggable={false} className="w-[190px] h-auto select-none pointer-events-none" />
        <h1 className="mt-7 mb-10 text-[26px] font-bold tracking-[0.06em]" style={{ color: '#1d55c4' }}>
          智开AI，您的AI办公助手
        </h1>
        <div className="flex items-stretch gap-4">
          <button
            type="button"
            disabled={loggingIn}
            onClick={() => void doLogin('OneOuchnPassport')}
            className="w-[230px] py-3.5 rounded-full text-white text-[16px] font-semibold tracking-[0.12em] shadow-[0_10px_24px_rgba(46,107,255,0.35)] transition-transform hover:scale-[1.02] active:scale-[0.99] disabled:opacity-70 disabled:cursor-default"
            style={{ background: 'linear-gradient(90deg, #2050d8 0%, #3f7bff 55%, #55a5ff 100%)' }}
          >
            {loggingIn ? '等待浏览器登录…' : '统一认证登录'}
          </button>
          <button
            type="button"
            disabled={loggingIn}
            onClick={() => void doLogin('')}
            className="w-[230px] py-3.5 rounded-full text-[15px] font-semibold tracking-[0.12em] border-2 bg-white/80 hover:bg-white transition-colors disabled:opacity-70 disabled:cursor-default"
            style={{ color: '#2050d8', borderColor: '#2050d8' }}
          >
            基座通行证登录
          </button>
        </div>
        {loggingIn && (
          <button type="button" className="mt-4 text-[13px] text-muted hover:text-ink" onClick={() => void passportCancelLogin()}>
            取消登录
          </button>
        )}
        {passportError && <div className="mt-4 max-w-[420px] text-center text-red text-[13px]">{passportError}</div>}
      </div>
    )
  }

  // 自动进入中：不闪现完整表单，维持登录门同款背景的过渡页；失败回落表单。
  if (autoEntering && !error) {
    return (
      <div
        className="relative flex flex-col items-center justify-center flex-1 min-h-0 bg-cover bg-center"
        style={{ backgroundImage: `url(${loginBg})` }}
      >
        <img src={loginMascot} alt="" draggable={false} className="w-[150px] h-auto select-none pointer-events-none" />
        <div className="mt-7 text-[15px] text-muted">正在进入工作区 <span className="font-mono">{shortenPath(initial.cwd ?? '')}</span>…</div>
      </div>
    )
  }

  return (
    <div className="flex items-start justify-center flex-1 min-h-0 overflow-y-auto px-6 py-10">
      <div className="w-[480px] flex flex-col gap-[13px]">
        <div className="flex items-center gap-3.5 mb-1">
          <span className="w-[48px] h-[48px] rounded-[13px] inline-flex items-center justify-center bg-surface border border-line2 shadow-xs"><Logo size={34} /></span>
          <div>
            <h1 className="m-0 text-[22px] font-bold tracking-tight">XRUN</h1>
            <p className="mt-[3px] text-muted text-[13px]">你的 AI 编程伙伴 · 打开一个工作区开始会话</p>
          </div>
        </div>
        <div className="flex items-center justify-between rounded-[9px] border border-line2 bg-surface2 px-3 py-2.5">
          <span className="text-[13px]">已登录：<b>{passport.name || passport.userName || passport.userId}</b></span>
          <button type="button" className="text-[12px] text-muted hover:text-ink" onClick={() => { void passportLogout() }}>登出</button>
        </div>
        {tenants.length > 1 && (
          <div className={LABEL_CLS}>租户（只能选择末级，选定后可选模型）
            <div className="max-h-[190px] overflow-y-auto rounded-[9px] border border-line2 bg-surface2 p-1.5 flex flex-col gap-0.5">
              {renderTenantNodes(tenantTree, 0)}
            </div>
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
            disabled={!tenantId && customModels.length === 0}
            onChange={(e) => setModelChoice(e.target.value)}
          >
            <option value="" disabled>{tenantId ? '选择一个模型…' : '请先在上方选择租户'}</option>
            {tenantId && platformModels.length > 0 && (
              <optgroup label="平台模型（通行证）">
                {platformModels.map((m) => <option key={m.id} value={`passport:${m.id}`}>{m.id}（{m.ownedBy}）</option>)}
              </optgroup>
            )}
            {customModels.length > 0 && (
              <optgroup label="自定义模型">
                {customModels.map((m) => <option key={m.name} value={`custom:${m.name}`}>{m.name}</option>)}
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

