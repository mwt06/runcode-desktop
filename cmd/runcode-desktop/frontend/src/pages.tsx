// Full-screen pages rendered by the app shell: the skills / agents / settings
// managers and the initial start form. Extracted from App.tsx to keep it focused.
import { useEffect, useRef, useState } from 'react'
import { Icon, Logo } from './icons'
import loginBg from './assets/login-bg.jpg'
import loginMascot from './assets/login-mascot.svg'
import { Markdown } from './markdown'
import { BTN, BTN_PRIMARY, BTN_DANGER } from './ui'
import {
  listSkills, saveSkill, deleteSkill, importSkill,
  listAgents, saveAgent, deleteAgent, importAgent,
  saveSettings, pickWorkspaceFolder,
  listMCPServers, saveMCPServer, deleteMCPServer, setMCPServerEnabled,
  listTools, setToolEnabled, setAgentEnabled, readProjectContext, saveProjectContext, readMemory,
  passportStatus, passportLogin, passportCancelLogin, passportLogout, passportModels, passportTenants,
  listCustomModels, saveCustomModel, deleteCustomModel,
  onEvent, Events,
  type SkillInfo, type SkillList,
  type AgentInfo, type AgentList,
  type MCPServerInfo, type MCPServerInput,
  type ToolInfo, type ProjectContextInfo, type MemoryInfo,
  type SessionInfo, type StartSessionRequest,
  type PassportStatus, type PassportModel, type PassportTenant, type CustomModel,
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

// ScopeSelect 是工具/子代理行尾的启用/停用下拉：启用 / 仅本项目停用 / 全局停用。
// 停用时下拉标红。任一作用域停用即视为停用(下次新建会话生效)。
function ScopeSelect({ disabledUser, disabledProject, onSet }: {
  disabledUser: boolean
  disabledProject: boolean
  onSet: (next: 'on' | 'user' | 'project') => void
}) {
  const value = disabledUser ? 'user' : disabledProject ? 'project' : 'on'
  return (
    <select
      value={value}
      title="启用 / 停用范围"
      onClick={(e) => e.stopPropagation()}
      onChange={(e) => { e.stopPropagation(); onSet(e.target.value as 'on' | 'user' | 'project') }}
      className={`text-[11.5px] rounded-[7px] border px-2 py-1 bg-surface2 outline-none flex-none cursor-pointer ${value === 'on' ? 'border-line2 text-muted' : 'border-red/50 text-red bg-red/5'}`}
    >
      <option value="on">启用</option>
      <option value="project">仅本项目停用</option>
      <option value="user">全局停用(用户级)</option>
    </select>
  )
}

// applyScopeChange 把下拉选择落成两个作用域的开关调用(仅调用发生变化的那个)。
async function applyScopeChange(
  setFn: (name: string, scope: string, enabled: boolean) => Promise<void>,
  name: string, curUser: boolean, curProject: boolean, next: 'on' | 'user' | 'project',
) {
  const target: Record<string, [boolean, boolean]> = { on: [false, false], user: [true, false], project: [false, true] }
  const [tu, tp] = target[next]
  if (curUser !== tu) await setFn(name, 'user', !tu)
  if (curProject !== tp) await setFn(name, 'project', !tp)
}

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
  const onSetScope = (ag: AgentInfo) => async (next: 'on' | 'user' | 'project') => {
    setError('')
    try {
      await applyScopeChange(setAgentEnabled, ag.name, ag.disabledUser, ag.disabledProject, next)
      refresh()
    } catch (e) {
      setError(String(e))
    }
  }

  function edit(ag: AgentInfo) {
    const zh = ag.source === 'builtin' ? AGENT_ZH[ag.name] : undefined
    setSel(ag.name)
    setName(ag.name)
    setOriginalName(ag.name)
    // 内置子代理只读，描述与指令正文显示中文对照(仅展示；模型收到的仍是原始定义)。
    setDescription(zh?.desc ?? ag.description)
    setTools(ag.tools)
    setModel(ag.model)
    setPrompt(zh?.prompt ?? ag.prompt)
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
              list.agents.map((ag) => {
                // 内置子代理用中文名+描述，同时保留真实名(供 @ 引用)；用户/项目
                // 自定义的按原样显示。
                const zh = ag.source === 'builtin' ? AGENT_ZH[ag.name] : undefined
                const off = ag.disabledUser || ag.disabledProject
                return (
                <div
                  key={ag.source + '/' + ag.name}
                  onClick={() => edit(ag)}
                  className={`group p-3 rounded-[11px] border cursor-pointer transition ${sel === ag.name ? 'border-primary bg-primarysoft' : 'border-line2 bg-surface hover:border-primary'} ${off ? 'opacity-55' : ''}`}
                >
                  <div className="flex items-center gap-1.5">
                    <span className="font-semibold text-[13.5px] text-ink truncate min-w-0">{zh?.label ?? ag.name}</span>
                    {zh && <span className="font-mono text-[10.5px] text-faint flex-none truncate max-w-[110px]">{ag.name}</span>}
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
                  <div className="text-[12px] text-muted mt-1 line-clamp-2">{zh?.desc ?? ag.description}</div>
                  <div className="flex items-center justify-between gap-2 mt-2">
                    {off ? <span className="text-[11px] text-red">已停用</span> : <span />}
                    <ScopeSelect disabledUser={ag.disabledUser} disabledProject={ag.disabledProject} onSet={onSetScope(ag)} />
                  </div>
                </div>
                )
              })
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
  useEffect(() => {
    void (async () => { try { setCustomModels((await listCustomModels()) ?? []) } catch { /* ignore */ } })()
  }, [])
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
        // provider/baseURL/apiKey 不在设置里编辑（通行证会话自动接线、自定义模型
        // 各自带连接）；原样透传避免保存设置时改动会话接线。
        provider: initial.provider,
        baseURL: initial.baseURL,
        apiKey: initial.apiKey,
        permissionMode,
        maxTokens: maxTokens.trim() ? parseInt(maxTokens, 10) || 0 : 0,
        maxContextTokens,
        maxHistoryMessages: maxHistoryMessages.trim() ? parseInt(maxHistoryMessages, 10) || 0 : 0,
        harmJudgeModel,
        harmJudgeVotes,
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
          <label className={label}>判定模型（智能模式的安全判定；留空 = 独立默认，与主模型解耦）
            <input className={field} value={harmJudgeModel} onChange={(e) => setHarmJudgeModel(e.target.value)} placeholder="留空 = 默认独立模型（如 claude-haiku-4-5）" />
          </label>
          <label className={label}>判定表决（多次独立判定取多数，更稳但更费 token）
            <select className={field} value={String(harmJudgeVotes)} onChange={(e) => setHarmJudgeVotes(parseInt(e.target.value, 10))}>
              <option value="1">单次（默认）</option>
              <option value="3">3 次取多数</option>
              <option value="5">5 次取多数</option>
            </select>
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
            <input className={field} placeholder="显示名称（如 本地 Ollama）" value={cmName} onChange={(e) => setCmName(e.target.value)} />
            <div className="grid grid-cols-2 gap-2">
              <input className={field} placeholder="模型 ID" value={cmModel} onChange={(e) => setCmModel(e.target.value)} />
              <input className={field} placeholder="Base URL（.../v1）" value={cmBaseURL} onChange={(e) => setCmBaseURL(e.target.value)} />
            </div>
            <input className={field} type="password" placeholder="API 密钥（可空）" value={cmApiKey} onChange={(e) => setCmApiKey(e.target.value)} />
            <button type="button" className={`${BTN} self-start px-5`} disabled={!cmName.trim() || !cmModel.trim()} onClick={async () => {
              const list = await saveCustomModel({ name: cmName.trim(), model: cmModel.trim(), baseURL: cmBaseURL.trim(), apiKey: cmApiKey })
              setCustomModels(list ?? []); setCmName(''); setCmModel(''); setCmBaseURL(''); setCmApiKey('')
            }}>添加自定义模型</button>
          </div>
        </section>

        <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
          <div className="flex items-center justify-between">
            <div className="text-[13px] font-semibold text-ink">上下文长度控制</div>
            <span className="text-[11.5px] text-faint">下次新建会话生效</span>
          </div>
          <label className={label}>最大输出 Tokens<input className={field} type="number" value={maxTokens} onChange={(e) => setMaxTokens(e.target.value)} placeholder="留空则用默认 16384" /></label>
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

function kvToText(m?: Record<string, string>): string {
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
  const field = 'font-sans text-[14px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2.5 outline-none focus:border-primary focus:shadow-[0_0_0_3px_var(--color-primarysoft)]'
  const label = 'flex flex-col gap-1.5 text-[12.5px] text-muted'
  const mono = field + ' font-mono text-[12.5px] leading-relaxed'

  async function refresh() {
    setLoading(true)
    setError('')
    try {
      setServers((await listMCPServers()) ?? [])
    } catch (e) {
      setError(String(e))
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
      setError(String(e))
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
      setError(String(e))
    }
  }
  async function toggle(s: MCPServerInfo) {
    setError('')
    try {
      await setMCPServerEnabled(s.name, !s.enabled)
      await refresh()
    } catch (e) {
      setError(String(e))
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
              <label className={label}>名称<input className={field} value={draft.name} onChange={(e) => set({ name: e.target.value })} placeholder="例如 filesystem" /></label>
              <label className={label}>传输方式
                <select className={field} value={draft.transport} onChange={(e) => set({ transport: e.target.value })}>
                  <option value="stdio">stdio(本地进程)</option>
                  <option value="http">http(远程端点)</option>
                </select>
              </label>
            </div>
            {draft.transport === 'http' ? (
              <>
                <label className={label}>URL<input className={field} value={draft.url} onChange={(e) => set({ url: e.target.value })} placeholder="https://example.com/mcp" /></label>
                <label className={label}>请求头(每行 KEY=VALUE,值可用 ${'{ENV_VAR}'})<textarea className={mono} rows={2} value={draft.headersText} onChange={(e) => set({ headersText: e.target.value })} placeholder="Authorization=Bearer ${TOKEN}" /></label>
              </>
            ) : (
              <>
                <label className={label}>命令<input className={field} value={draft.command} onChange={(e) => set({ command: e.target.value })} placeholder="npx" /></label>
                <label className={label}>参数(每行一个)<textarea className={mono} rows={3} value={draft.argsText} onChange={(e) => set({ argsText: e.target.value })} placeholder={"-y\n@modelcontextprotocol/server-filesystem"} /></label>
                <div className="grid grid-cols-2 gap-3">
                  <label className={label}>环境变量(每行 KEY=VALUE)<textarea className={mono} rows={2} value={draft.envText} onChange={(e) => set({ envText: e.target.value })} placeholder="TOKEN=${MY_TOKEN}" /></label>
                  <label className={label}>工作目录(可选)<input className={field} value={draft.dir} onChange={(e) => set({ dir: e.target.value })} placeholder="留空则用工作区" /></label>
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

// ToolsPage lists the active session's tools grouped by source (core builtins vs
// each MCP server), flagging which run concurrently — a read-only companion to the
// permissions page ("what can this session do, and what runs in parallel").
export function ToolsPage() {
  const [tools, setTools] = useState<ToolInfo[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [toggleError, setToggleError] = useState('')
  const reload = async () => { try { setTools(await listTools()) } catch { /* ignore */ } }
  useEffect(() => {
    void (async () => {
      try {
        setTools(await listTools())
      } finally {
        setLoading(false)
      }
    })()
  }, [])
  const onSetScope = (t: ToolInfo) => async (next: 'on' | 'user' | 'project') => {
    setToggleError('')
    try {
      await applyScopeChange(setToolEnabled, t.name, t.disabledUser, t.disabledProject, next)
      await reload()
    } catch (e) {
      setToggleError(String(e))
    }
  }

  const list = tools ?? []
  const builtin = list.filter((t) => t.source !== 'mcp')
  const byServer: Record<string, ToolInfo[]> = {}
  for (const t of list.filter((t) => t.source === 'mcp')) {
    ;(byServer[t.server ?? '?'] ??= []).push(t)
  }
  const shortName = (t: ToolInfo) => {
    const p = `mcp__${t.server}__`
    return t.source === 'mcp' && t.server && t.name.startsWith(p) ? t.name.slice(p.length) : t.name
  }
  const row = (t: ToolInfo) => {
    // 内置工具用中文名+描述；MCP 工具保持原样(第三方，无中文对照)。
    const zh = t.source !== 'mcp' ? TOOL_ZH[shortName(t)] : undefined
    const off = t.disabledUser || t.disabledProject
    return (
      <div key={t.name} className={`flex items-start gap-3 py-2.5 border-t border-line first:border-t-0 ${off ? 'opacity-50' : ''}`}>
        <span className="flex-none min-w-[132px]" title={t.name}>
          <span className="text-[13px] text-ink font-medium">{zh?.label ?? shortName(t)}</span>
          {zh && <span className="block font-mono text-[11px] text-faint mt-0.5 break-all">{shortName(t)}</span>}
        </span>
        <span className="text-[12.5px] text-muted flex-1 min-w-0 leading-relaxed">{zh?.desc ?? t.description}</span>
        {t.concurrencySafe && <span className="flex-none text-[10.5px] font-medium text-green bg-green/12 rounded-full px-2 py-0.5 mt-0.5">并行</span>}
        {t.toggleable && <ScopeSelect disabledUser={t.disabledUser} disabledProject={t.disabledProject} onSet={onSetScope(t)} />}
      </div>
    )
  }

  return (
    <div className="flex-1 overflow-y-auto px-[22px] py-7">
      <div className="max-w-[720px] mx-auto flex flex-col gap-5">
        <div>
          <h2 className="m-0 text-[20px] font-bold tracking-tight">工具</h2>
          <p className="mt-1 text-muted text-[13px]">标 <span className="text-green font-medium">并行</span> 的工具在同一轮里会并发执行(只读类)。行尾 <span className="text-faint">用户 / 项目</span> 可分别关闭该工具，关闭的不再传给模型，<b>下次新建会话生效</b>。</p>
          {toggleError && <p className="mt-1 text-red text-[12.5px]">{toggleError}</p>}
        </div>
        {loading ? (
          <div className="text-muted text-[13px] py-6 text-center">加载中…</div>
        ) : list.length === 0 ? (
          <div className="bg-surface border border-line2 border-dashed rounded-[14px] px-5 py-10 text-center text-muted text-[14px]">启动一个会话后即可查看可用工具。</div>
        ) : (
          <>
            <section className="bg-surface border border-line2 rounded-[14px] p-5 shadow-xs">
              <div className="flex items-center gap-2 mb-1">
                <span className="text-[13px] font-semibold text-ink">内置工具</span>
                <span className="text-[12px] text-faint">{builtin.length}</span>
              </div>
              <div>{builtin.map(row)}</div>
            </section>
            {Object.keys(byServer).sort().map((server) => (
              <section key={server} className="bg-surface border border-line2 rounded-[14px] p-5 shadow-xs">
                <div className="flex items-center gap-2 mb-1">
                  <span className="text-[10.5px] font-mono uppercase tracking-wide text-primaryink bg-primarysoft rounded px-1.5 py-0.5">MCP</span>
                  <span className="text-[13px] font-semibold text-ink">{server}</span>
                  <span className="text-[12px] text-faint">{byServer[server].length}</span>
                </div>
                <div>{byServer[server].map(row)}</div>
              </section>
            ))}
          </>
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
        setError(String(e))
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
      setError(String(e))
    } finally {
      setSaving(false)
    }
  }

  const hasWorkspace = !!ctx && ctx.path !== ''
  const dirty = !!ctx && content !== ctx.content
  const memSection = (title: string, note: string, raw: string[] | undefined) => {
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

// shortenPath keeps a workspace chip readable: the parent + leaf segment (e.g.
// "runcode_desktop\frontend"), with the full path shown on hover via title.
function shortenPath(p: string): string {
  const parts = p.split(/[\\/]+/).filter(Boolean)
  if (parts.length <= 2) return p
  return '…' + (p.includes('\\') ? '\\' : '/') + parts.slice(-2).join(p.includes('\\') ? '\\' : '/')
}

export function StartForm({ onStart, starting, error, initial }: { onStart: (req: StartSessionRequest) => void; starting: boolean; error: string; initial: StartSessionRequest }) {
  const [cwd, setCwd] = useState(initial.cwd ?? '')
  const [passport, setPassport] = useState<PassportStatus>({ loggedIn: false })
  const [tenants, setTenants] = useState<PassportTenant[]>([])
  const [tenantId, setTenantId] = useState(initial.tenantId ?? '')
  const [platformModels, setPlatformModels] = useState<PassportModel[]>([])
  const [customModels, setCustomModels] = useState<CustomModel[]>([])
  const [loggingIn, setLoggingIn] = useState(false)
  const [passportError, setPassportError] = useState('')
  // modelChoice: 'passport:<id>' | 'custom:<name>' | ''（未选）。
  // 手动配置/高级默认项都移到设置页；这里只在登录 + 选定租户后选择一个模型。
  const [modelChoice, setModelChoice] = useState(initial.provider === 'passport' && initial.model ? `passport:${initial.model}` : '')
  const recent = (initial.recentWorkspaces ?? []).filter((w) => w && w !== cwd)
  const field = 'font-sans text-[14px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2.5 outline-none focus:border-primary focus:shadow-[0_0_0_3px_var(--color-primarysoft)]'
  const label = 'flex flex-col gap-1.5 text-[12.5px] text-muted'
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
      setPassportError(`获取平台模型失败：${String(e)}`)
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
          setPassportError(`获取租户列表失败：${String(e)}`)
          tid = ''
        }
        if (tid) await doLoadModels(tid)
        else setPlatformModels([])
      }
    } catch { /* 登录状态读取失败：保持当前状态 */ }
    try { setCustomModels((await listCustomModels()) ?? []) } catch { /* ignore */ }
  }
  useEffect(() => {
    void refreshPassport()
    return onEvent<PassportStatus>(Events.PassportChanged, (st) => {
      setPassport(st)
      if (!st.loggedIn) { setPlatformModels([]); setTenants([]); setTenantId(''); setModelChoice('') }
      else void refreshPassport()
    })
  }, [])

  // 用户选定末级租户：重拉该租户模型，清掉可能已失效的平台模型选择。
  const selectTenant = async (tid: string) => {
    setTenantId(tid)
    if (modelChoice.startsWith('passport:')) setModelChoice('')
    await doLoadModels(tid)
  }

  const doLogin = async () => {
    setLoggingIn(true); setPassportError('')
    try {
      await passportLogin()
      await refreshPassport()
    } catch (e) {
      setPassportError(String(e))
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
    }
    if (modelChoice.startsWith('passport:')) {
      if (!passport.loggedIn || !tenantId) return null
      return { ...base, provider: 'passport', model: modelChoice.slice('passport:'.length), tenantId }
    }
    if (modelChoice.startsWith('custom:')) {
      const cm = customModels.find((m) => `custom:${m.name}` === modelChoice)
      if (!cm) return null
      return { ...base, provider: 'openai', model: cm.model, baseURL: cm.baseURL, apiKey: cm.apiKey }
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
  const renderTenantNodes = (nodes: TenantNode[], depth: number) =>
    nodes.flatMap((n) => {
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

  // 未登录：整屏登录门——背景 + 吉祥物 + 标语 + 唯一的"统一认证登录"入口。
  // 工作区/模型等表单只在登录成功后出现。
  if (!passport.loggedIn) {
    return (
      <div
        className="relative flex flex-col items-center justify-center flex-1 min-h-0 bg-cover bg-center"
        style={{ backgroundImage: `url(${loginBg})` }}
      >
        <img src={loginMascot} alt="" draggable={false} className="w-[190px] h-auto select-none pointer-events-none" />
        <h1 className="mt-7 mb-12 text-[26px] font-bold tracking-[0.06em]" style={{ color: '#1d55c4' }}>
          智开AI，您的AI办公助手
        </h1>
        <button
          type="button"
          disabled={loggingIn}
          onClick={() => void doLogin()}
          className="w-[300px] py-3.5 rounded-full text-white text-[16px] font-semibold tracking-[0.15em] shadow-[0_10px_24px_rgba(46,107,255,0.35)] transition-transform hover:scale-[1.02] active:scale-[0.99] disabled:opacity-70 disabled:cursor-default"
          style={{ background: 'linear-gradient(90deg, #2050d8 0%, #3f7bff 55%, #55a5ff 100%)' }}
        >
          {loggingIn ? '等待浏览器登录…' : '统一认证登录'}
        </button>
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
    <div className="flex items-center justify-center flex-1 min-h-0 p-6">
      <div className="w-[480px] max-h-full overflow-y-auto bg-surface rounded-[18px] p-8 flex flex-col gap-[13px] shadow-card">
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
          <div className={label}>租户（只能选择末级，选定后可选模型）
            <div className="max-h-[190px] overflow-y-auto rounded-[9px] border border-line2 bg-surface2 p-1.5 flex flex-col gap-0.5">
              {renderTenantNodes(tenantTree, 0)}
            </div>
          </div>
        )}
        <div className={label}>工作区目录
          <div className="flex gap-2">
            <input className={`${field} flex-1 min-w-0`} value={cwd} onChange={(e) => setCwd(e.target.value)} placeholder="C:\path\to\project" />
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
        <label className={label}>模型（选定租户后可选；自定义模型在设置中配置）
          <select
            className={field}
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

