import { useEffect, useRef, useState, useMemo, type ReactNode, type CSSProperties, type PointerEvent } from 'react'
import { Icon, Logo } from './icons'
import { Markdown } from './markdown'
import {
  Events,
  onEvent,
  startSession,
  sendMessage,
  sendMessageWithImages,
  pickImageAttachment,
  interrupt,
  resolvePermission,
  setPermissionMode,
  setModel,
  sessionModels,
  setPlanMode,
  setReasoningScenario,
  setThinkingEffort,
  compact,
  listSessions,
  resumeSession,
  newSession,
  pickWorkspaceFolder,
  switchWorkspace,
  loadConfig,
  saveSettings,
  listFiles,
  listEdits,
  revertEdit,
  listSkills,
  saveSkill,
  deleteSkill,
  importSkill,
  listAgents,
  saveAgent,
  deleteAgent,
  importAgent,
  type SkillInfo,
  type SkillList,
  type AgentInfo,
  type AgentList,
  type SessionInfo,
  type SessionSummary,
  type PassportModel,
  type StartSessionRequest,
  type PermissionRequest,
  type ToolEvent,
  type TurnEnd,
  type PlanSnapshot,
  type PlanItem,
} from './bridge'
import {
  finalizeStreaming,
  finalizeTools,
  mergeTool,
  groupBlocks,
  parsePlan,
  type Block,
  type AgentNested,
  type Group,
} from './chat'
import { BTN, BTN_PRIMARY, BTN_DANGER } from './ui'
import { PluginsPage, SettingsPage, PermissionsPage, MemoryPage, StartForm } from './pages'
import { PreviewPane, FileBrowser } from './preview-panel'
import { ArtifactCard } from './artifact-card'
import { EditedCards } from './edited-card'
import { isPreviewable, toWorkspaceRel, clampPreviewWidth, lastPreviewablePath, extractFilePaths, matchWorkspaceFiles } from './preview'
import { openTab, closeTab, type PreviewTab } from './preview-tabs'

let seq = 0
const nextID = () => `b${++seq}`
const now = () =>
  new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })

const MODE_LABEL: Record<string, string> = { safe: '安全模式', interactive: '交互模式', judge: '智能模式', flight: '飞行模式' }
// "Thinking model" options for the in-conversation picker.
const REASONING: { value: string; label: string }[] = [
  { value: 'off', label: '不启用' },
  { value: 'auto', label: '自动分类（每轮多一次调用）' },
  { value: 'troubleshooting', label: '排障 · 5 Whys' },
  { value: 'proposal', label: '方案 · 金字塔原理' },
  { value: 'architecture', label: '架构 · 第一性原理' },
  { value: 'project_management', label: '项目 · 闭环 + 80/20' },
  { value: 'incident_response', label: '救火 · OODA' },
  { value: 'general', label: '通用 · 10 步清单' },
]
const REASONING_LABEL: Record<string, string> = Object.fromEntries(REASONING.map((r) => [r.value, r.label]))

// 思考模型（reasoning scenario）按钮暂时隐藏；改回 true 即可恢复整块 UI 与逻辑。
const SHOW_THINKING_MODEL = false

// "Thinking strength" options: provider-native reasoning effort (OpenAI
// reasoning_effort / an Anthropic thinking budget). This is the knob that makes a
// reasoning model actually emit the reasoning content shown above each answer.
const THINKING: { value: string; label: string }[] = [
  { value: 'off', label: '不启用' },
  { value: 'low', label: '低 · 快速' },
  { value: 'medium', label: '中 · 均衡' },
  { value: 'high', label: '高 · 深度' },
]
const THINKING_LABEL: Record<string, string> = { low: '低', medium: '中', high: '高' }

type MentionTrigger = '@' | '/' | '#'
// computeMention finds an active mention/command trigger ending at the cursor:
//   '@' (a skill command) and '/' (a sub-agent command) only at the very start;
//   '#' (a file mention) at the start or after whitespace, anywhere in the input.
// In every case there must be no whitespace between the trigger and the cursor.
function computeMention(value: string, cursor: number): { query: string; start: number; trigger: MentionTrigger } | null {
  const upToCursor = value.slice(0, cursor)
  if (value.startsWith('@') && !/\s/.test(upToCursor.slice(1))) {
    return { query: upToCursor.slice(1), start: 0, trigger: '@' }
  }
  if (value.startsWith('/') && !/\s/.test(upToCursor.slice(1))) {
    return { query: upToCursor.slice(1), start: 0, trigger: '/' }
  }
  const hash = upToCursor.lastIndexOf('#')
  if (hash < 0) return null
  if (hash > 0 && !/\s/.test(value[hash - 1])) return null
  const query = upToCursor.slice(hash + 1)
  if (/\s/.test(query)) return null
  return { query, start: hash, trigger: '#' }
}
const VERB: Record<string, string> = {
  Read: '读取文件', Write: '写入文件', Edit: '编辑', Delete: '删除', Glob: '查找文件', Grep: '搜索项目代码',
  Bash: '运行命令', BashOutput: '后台输出', KillShell: '终止命令', WebFetch: '抓取网页',
  TodoWrite: '规划任务', Task: '委派子代理', Skill: '加载技能', Remember: '记录记忆', Analyze: '结构化分析',
  open_preview: '预览',
}
const basename = (p?: string) => (p ? p.replace(/\\/g, '/').split('/').pop() || p : '')
const clip = (s: string, n: number) => (s.length > n ? s.slice(0, n) + '…' : s)
// A tool-type icon for the compact list rows, so a long tool sequence scans at a glance.
const TOOL_ICON: Record<string, string> = {
  Read: 'file', Write: 'pencil', Edit: 'pencil', Delete: 'trash',
  Bash: 'terminal', BashOutput: 'terminal', KillShell: 'terminal',
  Grep: 'search', Glob: 'search', WebFetch: 'globe',
  Task: 'bot', Skill: 'book', Remember: 'sparkles', Analyze: 'sparkles',
  TodoWrite: 'grid', AskUser: 'chat',
  open_preview: 'file',
}
const toolIcon = (name?: string) => TOOL_ICON[name || ''] || 'grid'
// toolInputObj parses a tool call's arguments into an object (live events carry the
// parsed value; resumed sessions carry a JSON string).
function toolInputObj(t: ToolEvent): Record<string, unknown> {
  const raw = (t as ToolEvent & { input?: unknown }).input
  if (raw == null) return {}
  if (typeof raw === 'string') { try { return JSON.parse(raw) } catch { return {} } }
  return typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
}
// toolVerbTarget splits a tool call into its Chinese verb and its most useful target
// (command / pattern / host / file), so a row can style the two differently (verb in
// ink, target in mono).
function toolVerbTarget(t: ToolEvent): { verb: string; target: string } {
  const verb = VERB[t.toolName || ''] || t.toolName || '工具'
  const o = toolInputObj(t)
  let target = ''
  switch (t.toolName) {
    case 'Bash':
      target = clip(String(o.command ?? '').replace(/\s+/g, ' ').trim(), 56)
      break
    case 'Grep':
    case 'Glob':
      target = clip(String(o.pattern ?? ''), 44)
      break
    case 'WebFetch':
      try { target = new URL(String(o.url ?? '')).host } catch { target = clip(String(o.url ?? ''), 44) }
      break
    default:
      target = basename(t.files?.[0]?.path) || basename(String(o.path ?? ''))
  }
  return { verb, target }
}

// toolTargetPath returns the file a Write/Edit acted on (absolute or workspace-
// relative), for wiring a preview affordance. Returns undefined when there is none.
function toolTargetPath(t: ToolEvent): string | undefined {
  const fromFiles = t.files?.[0]?.path
  if (fromFiles) return fromFiles
  const p = toolInputObj(t).path
  return typeof p === 'string' && p ? p : undefined
}

// toolLabel is the flat "verb target +adds -dels" string for places that need plain
// text (resumed-session rebuild, titles).
function toolLabel(t: ToolEvent): string {
  const { verb, target } = toolVerbTarget(t)
  const { add, del } = diffStats(t)
  const diff = add + del > 0 ? `  +${add} -${del}` : ''
  return (target ? `${verb} ${target}` : verb) + diff
}
const diffStats = (t: ToolEvent) => {
  let add = 0, del = 0
  for (const l of t.output ?? []) {
    if (l.stream === 'diff_add') add++
    else if (l.stream === 'diff_del') del++
  }
  return { add, del }
}
const hasDiff = (t: ToolEvent) => {
  const { add, del } = diffStats(t)
  return add + del > 0
}
const hasOutput = (t: ToolEvent) => (t.output?.length ?? 0) > 0

// Translate the executor's failure message into a short Chinese reason.
const DENY_REASON: Record<string, string> = {
  read_required: '需先读取该文件再写入',
  write_exists: '当前文件已存在',
  read_stale: '文件已变化，请重新读取',
  approval_denied: '已拒绝',
  approval_unavailable: '安全模式下不可执行',
  outside_workspace: '路径在工作区之外',
  denylisted: '已被拒止规则阻止',
  policy_denied: '策略不允许',
  invalid_target: '目标无效',
  invalid_input: '输入无效',
  unknown_tool: '未知工具',
  requires_approval: '需要审批',
}
const FAIL: Record<string, string> = {
  'blocked by hook': '被钩子拦截',
  cancelled: '已取消',
  failed: '执行失败',
  'completed with error': '执行失败',
}
function failText(t: ToolEvent): string {
  const m = t.message ?? ''
  if (m.startsWith('denied:')) {
    return DENY_REASON[m.slice('denied:'.length)] ?? '权限被拒绝'
  }
  return FAIL[m] ?? (m || '失败')
}

function lineClass(stream?: string): string {
  const base = 'px-2.5 whitespace-pre'
  switch (stream) {
    case 'stderr':
      return base + ' text-red'
    case 'info':
      return base + ' text-faint'
    case 'match':
      return base + ' text-[#957419]'
    default:
      return base + ' text-muted'
  }
}


// analyzeSteps parses an Analyze tool call's input into the method and its filled
// steps, tolerating the live object form and the JSON-string form (resumed). Steps
// carry a human label when the backend enriched the event; otherwise the key stands
// in.
function analyzeSteps(input: unknown): { method?: string; steps: { key: string; label?: string; content: string }[] } {
  let obj: { method?: string; steps?: { key?: string; label?: string; content?: string }[] } = {}
  if (typeof input === 'string') {
    try {
      obj = JSON.parse(input)
    } catch {
      obj = {}
    }
  } else if (input && typeof input === 'object') {
    obj = input as typeof obj
  }
  const steps = Array.isArray(obj.steps)
    ? obj.steps.map((s) => ({ key: String(s?.key ?? ''), label: s?.label ? String(s.label) : undefined, content: String(s?.content ?? '') }))
    : []
  return { method: obj.method ? String(obj.method) : undefined, steps }
}

// askPayload pulls the question and options out of an AskUser tool call's input,
// tolerating both the live object form and the JSON-string form (resumed).
function askPayload(input: unknown): { question: string; options: string[] } {
  let obj: { question?: string; options?: string[] } = {}
  if (typeof input === 'string') {
    try {
      obj = JSON.parse(input)
    } catch {
      /* leave empty */
    }
  } else if (input && typeof input === 'object') {
    obj = input as { question?: string; options?: string[] }
  }
  return { question: obj.question ?? '', options: Array.isArray(obj.options) ? obj.options : [] }
}

export default function App() {
  const [started, setStarted] = useState(false)
  const [view, setView] = useState<'chat' | 'settings' | 'plugins' | 'permissions' | 'memory'>('chat')
  const [info, setInfo] = useState<SessionInfo | null>(null)
  // 对话内模型选择器：点底部模型名弹出，模糊检索，最多显示 10 个。
  const [modelPickerOpen, setModelPickerOpen] = useState(false)
  const [modelOptions, setModelOptions] = useState<PassportModel[]>([])
  const [modelQuery, setModelQuery] = useState('')
  const openModelPicker = async () => {
    setModelQuery('')
    setModelPickerOpen(true)
    try { setModelOptions((await sessionModels()) ?? []) } catch { setModelOptions([]) }
  }
  const pickModel = async (id: string) => {
    setModelPickerOpen(false)
    try { await setModel(id); setInfo((prev) => (prev ? { ...prev, model: id } : prev)) } catch { /* 切换失败保持原样 */ }
  }
  const infoRef = useRef(info)
  useEffect(() => {
    infoRef.current = info
  }, [info])
  const [blocks, setBlocks] = useState<Block[]>([])
  // harmAllows maps a tool-use id to the harm judge's reason when smart mode
  // auto-allowed that call, so its tool card can be marked and explain why.
  const [harmAllows, setHarmAllows] = useState<Record<string, string>>({})
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  // Concurrent tools (e.g. parallel WebFetch) can each raise a prompt. The backend
  // Approver already queues them by id; the UI queues too and resolves the head
  // first, so a second request never clobbers the one on screen. `pending` is the
  // request currently shown.
  const [permQueue, setPermQueue] = useState<PermissionRequest[]>([])
  const pending = permQueue[0] ?? null
  // plan is the latest TodoWrite snapshot (null until the model records one), shown
  // as the top-center progress pill. planOpen toggles the pill's dropdown (the full
  // task timeline); the pill itself stays visible whenever a plan exists.
  const [plan, setPlan] = useState<PlanSnapshot | null>(null)
  const [planOpen, setPlanOpen] = useState(false)
  const [tokens, setTokens] = useState({ in: 0, out: 0 })
  // ctxTokens is the current context-window occupancy (last turn's final input
  // tokens), shown against the compaction budget so the user sees how full it is.
  // ctxEstimated marks it as a resume-time estimate until the first turn reports
  // the provider's exact count.
  const [ctxTokens, setCtxTokens] = useState(0)
  const [ctxEstimated, setCtxEstimated] = useState(false)
  const [compacting, setCompacting] = useState(false)
  const [elapsed, setElapsed] = useState(0)
  const [starting, setStarting] = useState(false)
  const [startError, setStartError] = useState('')
  const [recents, setRecents] = useState<SessionSummary[]>([])
  const [initialReq, setInitialReq] = useState<StartSessionRequest | null>(null)
  const [files, setFiles] = useState<string[]>([])
  const [tabs, setTabs] = useState<PreviewTab[]>([])
  const [activeTab, setActiveTab] = useState<string | null>(null)
  // revertedEdits tracks which edited-file snapshots the user has undone, by
  // snapshotId, so their "已编辑" card renders grey with 已撤销 instead of the
  // 撤销/审核 actions (both live and after a resume re-attach).
  const [revertedEdits, setRevertedEdits] = useState<Set<string>>(new Set())
  const [previewWidth, setPreviewWidth] = useState<number>(() =>
    clampPreviewWidth(Number(localStorage.getItem('preview.width')), window.innerWidth),
  )
  const [autoOpen, setAutoOpen] = useState<boolean>(() => localStorage.getItem('preview.autoOpen') !== '0')
  const toggleAutoOpen = () => setAutoOpen((v) => { localStorage.setItem('preview.autoOpen', v ? '0' : '1'); return !v })
  const [browseOpen, setBrowseOpen] = useState(false)
  const [chatSkills, setChatSkills] = useState<SkillInfo[]>([])
  const [chatAgents, setChatAgents] = useState<AgentInfo[]>([])
  const [mention, setMention] = useState<{ query: string; start: number; sel: number; trigger: MentionTrigger } | null>(null)
  const [reasonMenu, setReasonMenu] = useState(false)
  const [thinkMenu, setThinkMenu] = useState(false)
  const [addMenu, setAddMenu] = useState(false)
  const [attachments, setAttachments] = useState<string[]>([])
  const scrollRef = useRef<HTMLDivElement>(null)
  const taRef = useRef<HTMLTextAreaElement>(null)
  const selItemRef = useRef<HTMLDivElement>(null)

  const openArtifact = (rel: string) => {
    const r = openTab(tabs, activeTab, { kind: 'file', relPath: rel })
    setTabs(r.tabs)
    setActiveTab(r.active)
    setBrowseOpen(false)
  }
  const openDiffTab = (snapshotId: string, relPath: string) => {
    const r = openTab(tabs, activeTab, { kind: 'diff', snapshotId, relPath })
    setTabs(r.tabs)
    setActiveTab(r.active)
    setBrowseOpen(false)
  }
  // handleUndo reverts one edit snapshot (Write/Edit) and marks its card reverted;
  // a revert can create/delete/restore a file, so the workspace file list is
  // refreshed too.
  const handleUndo = async (snapshotId: string) => {
    try {
      await revertEdit(snapshotId)
      setRevertedEdits((s) => new Set(s).add(snapshotId))
      listFiles().then((f) => setFiles(f ?? [])).catch(() => {})
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'warning', id: nextID(), text: '撤销失败：' + String(e) }])
    }
  }
  const resolveWsFile = useMemo(() => {
    const norm = (s: string) => s.replace(/\\/g, '/').replace(/^\.\//, '').replace(/^\/+/, '')
    const set = files.map(norm)
    return (token: string): string | null => {
      const cn = norm(token)
      if (!cn) return null
      return set.find((f) => f === cn || f.endsWith('/' + cn)) ?? null
    }
  }, [files])
  const closePreviewTab = (key: string) => {
    const r = closeTab(tabs, activeTab, key)
    setTabs(r.tabs)
    setActiveTab(r.active)
  }
  const autoRef = useRef({ autoOpen, blocks, cwd: info?.cwd ?? '' })
  autoRef.current = { autoOpen, blocks, cwd: info?.cwd ?? '' }
  const openPreviewRef = useRef<(path: string) => void>(() => {})
  openPreviewRef.current = (path: string) => openArtifact(toWorkspaceRel(path, info?.cwd ?? ''))
  const turnStartLen = useRef(0)
  const prevBusy = useRef(false)
  useEffect(() => {
    if (!prevBusy.current && busy) {
      // Turn starting: remember where this turn's blocks begin.
      turnStartLen.current = autoRef.current.blocks.length
    } else if (prevBusy.current && !busy) {
      // Turn ended: open the newest previewable file THIS turn wrote (if any).
      const { autoOpen: on, blocks: bs, cwd } = autoRef.current
      if (on) {
        const paths: string[] = []
        for (const b of bs.slice(turnStartLen.current)) {
          if (b.kind !== 'tool') continue
          const t = (b as Extract<Block, { kind: 'tool' }>).tool
          if ((t.toolName === 'Write' || t.toolName === 'Edit') && t.type === 'completed') {
            const p = toolTargetPath(t)
            if (p) paths.push(toWorkspaceRel(p, cwd))
          }
        }
        const rel = lastPreviewablePath(paths)
        if (rel) openArtifact(rel)
      }
    }
    prevBusy.current = busy
  }, [busy])
  const dragW = useRef<{ startX: number; startW: number } | null>(null)
  const onPreviewDragStart = (e: PointerEvent) => {
    dragW.current = { startX: e.clientX, startW: previewWidth }
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  }
  const onPreviewDragMove = (e: PointerEvent) => {
    if (!dragW.current) return
    const dx = dragW.current.startX - e.clientX // dragging the left edge leftward grows the pane
    const w = Math.min(Math.max(dragW.current.startW + dx, 360), Math.floor(window.innerWidth * 0.6))
    setPreviewWidth(w)
  }
  const onPreviewDragEnd = () => {
    if (dragW.current) {
      localStorage.setItem('preview.width', String(previewWidth))
      dragW.current = null
    }
  }

  // Keep the highlighted picker item visible as the selection moves with ↑/↓.
  useEffect(() => {
    selItemRef.current?.scrollIntoView({ block: 'nearest' })
  }, [mention?.sel])

  // Composer pickers: files (#), skills (@), sub-agents (/) — refreshed on open.
  useEffect(() => {
    if (!started) return
    listFiles()
      .then((f) => setFiles(f ?? []))
      .catch(() => setFiles([]))
    listSkills()
      .then((l) => setChatSkills(l?.skills ?? []))
      .catch(() => setChatSkills([]))
    listAgents()
      .then((l) => setChatAgents(l?.agents ?? []))
      .catch(() => setChatAgents([]))
  }, [started, info?.sessionId])

  // Files matching the #-query (basename/prefix ranked) — only for the '#' trigger.
  const fileMatches =
    mention?.trigger === '#'
      ? (() => {
          const q = mention.query.toLowerCase()
          const hits = q ? files.filter((p) => p.toLowerCase().includes(q)) : files
          return [...hits]
            .sort((a, b) => {
              const ba = a.slice(a.lastIndexOf('/') + 1).toLowerCase()
              const bb = b.slice(b.lastIndexOf('/') + 1).toLowerCase()
              const sa = q && ba.startsWith(q) ? 0 : 1
              const sb = q && bb.startsWith(q) ? 0 : 1
              return sa - sb || a.length - b.length
            })
            .slice(0, 50)
        })()
      : []
  // Skills matching the @-query (by name or description) — only for the '@' trigger.
  const skillMatches =
    mention?.trigger === '@'
      ? (() => {
          const q = mention.query.toLowerCase()
          return chatSkills.filter((s) => !q || s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q)).slice(0, 50)
        })()
      : []
  // Sub-agents matching the /-query (by name or description) — only for '/'.
  const agentMatches =
    mention?.trigger === '/'
      ? (() => {
          const q = mention.query.toLowerCase()
          return chatAgents.filter((a) => !q || a.name.toLowerCase().includes(q) || a.description.toLowerCase().includes(q)).slice(0, 50)
        })()
      : []
  const mentionCount =
    mention?.trigger === '@' ? skillMatches.length : mention?.trigger === '/' ? agentMatches.length : fileMatches.length

  function syncMention(value: string, cursor: number) {
    const m = computeMention(value, cursor)
    setMention(m ? { ...m, sel: 0 } : null)
  }

  // pickMention applies the highlighted item for the active trigger.
  function pickMention(index: number) {
    if (!mention) return
    if (mention.trigger === '@') {
      const sk = skillMatches[Math.min(index, skillMatches.length - 1)]
      if (sk) pickSkill(sk.name)
    } else if (mention.trigger === '/') {
      const ag = agentMatches[Math.min(index, agentMatches.length - 1)]
      if (ag) pickAgent(ag.name)
    } else {
      const path = fileMatches[Math.min(index, fileMatches.length - 1)]
      if (path) pickFile(path)
    }
  }

  function pickFile(path: string) {
    if (!mention) return
    const before = input.slice(0, mention.start)
    const after = input.slice(mention.start + 1 + mention.query.length)
    const insert = '#' + path + ' '
    setInput(before + insert + after)
    setMention(null)
    setCaret((before + insert).length)
  }

  function pickSkill(name: string) {
    if (!mention) return
    // The '@' command lives at the input start; replace it with a use-instruction
    // and leave the cursor after it so the user types the task.
    const after = input.slice(mention.start + 1 + mention.query.length)
    const insert = `请使用「${name}」技能完成：`
    setInput(insert + after)
    setMention(null)
    setCaret(insert.length)
  }

  function pickAgent(name: string) {
    if (!mention) return
    // The '/' command lives at the input start; replace it with a delegation
    // instruction so the main agent hands the task to this sub-agent (Task tool).
    const after = input.slice(mention.start + 1 + mention.query.length)
    const insert = `请委派「${name}」子代理完成：`
    setInput(insert + after)
    setMention(null)
    setCaret(insert.length)
  }

  function setCaret(pos: number) {
    requestAnimationFrame(() => {
      const ta = taRef.current
      if (ta) {
        ta.focus()
        ta.setSelectionRange(pos, pos)
      }
    })
  }

  function openFilePicker() {
    const ta = taRef.current
    const sep = input && !/\s$/.test(input) ? ' ' : ''
    const next = input + sep + '#'
    setInput(next)
    setMention({ query: '', start: next.length - 1, sel: 0, trigger: '#' })
    // Refresh the workspace file list on open — a session that listed none at
    // startup (or before its workspace was ready) still gets a current list.
    listFiles()
      .then((f) => setFiles(f ?? []))
      .catch(() => {})
    requestAnimationFrame(() => ta?.focus())
  }
  // The "+" menu opens the skill (@) or sub-agent (/) picker directly, mirroring
  // typing the trigger at the composer start. Both are start-of-input commands that
  // replace the whole input on pick, so each opens a fresh one.
  function openSkillPicker() {
    setInput('@')
    setMention({ query: '', start: 0, sel: 0, trigger: '@' })
    setCaret(1)
  }
  function openAgentPicker() {
    setInput('/')
    setMention({ query: '', start: 0, sel: 0, trigger: '/' })
    setCaret(1)
  }
  // Pick an image from disk and attach it to the next message. The path is kept in
  // `attachments` and the bytes are read backend-side at send time.
  async function pickAttachment() {
    try {
      const p = await pickImageAttachment()
      if (p) setAttachments((a) => (a.includes(p) ? a : [...a, p]))
    } catch {
      /* cancelled or unavailable */
    }
  }

  // Load the persisted start-form values so a restart prefills them.
  useEffect(() => {
    loadConfig()
      .then((r) => setInitialReq(r ?? {}))
      .catch(() => setInitialReq({}))
  }, [])

  async function refreshRecents() {
    try {
      const r = await listSessions()
      setRecents(r ?? [])
    } catch {
      /* 列表加载失败时静默保留旧值 */
    }
  }
  // Refresh the recent list when a session opens and whenever a turn finishes
  // (a turn persists/updates the session file).
  useEffect(() => {
    if (started && !busy) refreshRecents()
  }, [started, busy])

  useEffect(() => {
    const offs = [
      onEvent<{ text: string }>(Events.AssistantDelta, ({ text }) => {
        setBlocks((prev) => {
          const last = prev[prev.length - 1]
          if (last && last.kind === 'assistant' && last.streaming) {
            return [...prev.slice(0, -1), { ...last, text: last.text + text }]
          }
          return [...prev, { kind: 'assistant', id: nextID(), text, streaming: true, ts: now() }]
        })
      }),
      // Reasoning ("thinking") arrives before the answer; fold it into the current
      // streaming assistant block (creating one if the model thinks before speaking)
      // so the UI can show the chain of thought above the answer.
      onEvent<{ text: string }>(Events.AssistantThinking, ({ text }) => {
        setBlocks((prev) => {
          const last = prev[prev.length - 1]
          if (last && last.kind === 'assistant' && last.streaming) {
            return [...prev.slice(0, -1), { ...last, thinking: (last.thinking ?? '') + text }]
          }
          return [...prev, { kind: 'assistant', id: nextID(), text: '', thinking: text, streaming: true, ts: now() }]
        })
      }),
      onEvent<ToolEvent>(Events.ToolEvent, (ev) => {
        // The main agent's TodoWrite plan drives the right-rail progress board, not a
        // stream row, so intercept it here (all of its started/progress/completed
        // events carry toolName). Sub-agent todos (parentToolUseID set) are left to
        // nest inside their Task card, so only the main plan reaches the board.
        if (!ev.parentToolUseID && ev.toolName === 'TodoWrite') {
          const snap = parsePlan(ev)
          if (snap) setPlan(snap)
          return
        }
        if (!ev.parentToolUseID && ev.toolName === 'open_preview') {
          const p = (ev.data as { path?: string } | undefined)?.path
          if (p) openPreviewRef.current(p)
        }
        setBlocks((prev) => {
          // A sub-agent's child event nests under its parent Task card (live), rather
          // than creating a top-level row.
          if (ev.parentToolUseID) {
            const pidx = prev.findIndex((b) => b.kind === 'tool' && b.tool.toolUseID === ev.parentToolUseID)
            if (pidx < 0) return prev // parent Task not found yet — drop (shouldn't happen)
            const parent = prev[pidx] as Extract<Block, { kind: 'tool' }>
            const n: AgentNested = parent.nested ?? { agent: ev.agentName ?? '', text: '', tools: [] }
            const agent = n.agent || ev.agentName || ''
            let updated: AgentNested
            if (ev.type === 'agent_delta') {
              updated = { ...n, agent, text: n.text + (ev.message ?? '') }
            } else if (ev.type === 'agent_usage') {
              updated = { ...n, agent, usage: { inTok: ev.inputTokens ?? 0, outTok: ev.outputTokens ?? 0, durMs: ev.durationMs } }
            } else {
              const tidx = n.tools.findIndex((t) => t.toolUseID && t.toolUseID === ev.toolUseID)
              const tools = tidx >= 0 ? n.tools.map((t, i) => (i === tidx ? mergeTool(t, ev) : t)) : [...n.tools, ev]
              updated = { ...n, agent, tools }
            }
            const out = [...prev]
            out[pidx] = { ...parent, nested: updated }
            return out
          }
          let next = finalizeStreaming(prev)
          const idx = next.findIndex(
            (b) => b.kind === 'tool' && b.tool.toolUseID && b.tool.toolUseID === ev.toolUseID,
          )
          if (idx >= 0) {
            const existing = next[idx] as Extract<Block, { kind: 'tool' }>
            next = [...next]
            next[idx] = { ...existing, tool: mergeTool(existing.tool, ev) }
            return next
          }
          return [...next, { kind: 'tool', id: nextID(), tool: ev }]
        })
      }),
      onEvent<TurnEnd>(Events.TurnEnd, (end) => {
        setBusy(false)
        listFiles().then((f) => setFiles(f ?? [])).catch(() => {})
        // The turn is over; any still-queued prompts were denied on the backend
        // (context cancel / DenyAll), so drop stale modals.
        setPermQueue([])
        setTokens((t) => ({ in: t.in + end.inputTokens, out: t.out + end.outputTokens }))
        if (end.contextTokens) {
          setCtxTokens(end.contextTokens)
          setCtxEstimated(false) // now a provider-measured exact count
        }
        setBlocks((prev) => {
          let next = finalizeTools(finalizeStreaming(prev))
          const hadAssistant = next.some((b) => b.kind === 'assistant')
          if (!hadAssistant && end.text.trim()) {
            next = [...next, { kind: 'assistant', id: nextID(), text: end.text, streaming: false, ts: now() }]
          }
          // The turn halted. For a user-denied tool, show a clear "stopped" notice.
          // For an AskUser question the interactive card already prompts the user,
          // so the notice would be redundant — skip it then.
          const askedLast = [...next].reverse().find((b) => b.kind === 'tool')
          const isAsk = askedLast?.kind === 'tool' && askedLast.tool.toolName === 'AskUser'
          if (end.stopped && !isAsk) {
            next = [...next, { kind: 'notice', id: nextID(), text: '已停止，等待下一步指令' }]
          }
          // The turn produced neither text nor any tool call — an empty completion.
          // Usually the model/endpoint returned nothing (e.g. it doesn't support the
          // attached image, or the reply was cut off). Surface it so silence is not
          // confusing.
          const emptyResponse =
            end.text.trim() === '' && end.toolResultCount === 0 && !end.stopped && !isAsk &&
            !next.some((b) => b.kind === 'assistant' && b.text.trim() !== '')
          if (emptyResponse) {
            const lastUser = [...next].reverse().find((b) => b.kind === 'user') as Extract<Block, { kind: 'user' }> | undefined
            const hadImage = (lastUser?.attachments?.length ?? 0) > 0
            next = [...next, {
              kind: 'notice', id: nextID(),
              text: hadImage
                ? '模型返回了空内容 —— 当前模型/接口可能不支持图片输入。可在「设置」换用支持视觉的模型。'
                : '模型返回了空内容(可能被截断或触发了内容限制)。',
            }]
          }
          // Close the reply with this turn's own token spend (a faint footer). It
          // counts only the parent session's model calls; a delegated sub-agent's
          // tokens are shown on its Task card instead, so the two never double-count.
          if (end.inputTokens > 0 || end.outputTokens > 0) {
            next = [...next, { kind: 'usage', id: nextID(), inTok: end.inputTokens, outTok: end.outputTokens, durMs: end.durationMs }]
          }
          // In plan mode, a completed turn means the model presented its plan — offer
          // how to proceed (execute interactively / via judge, or keep refining).
          if (infoRef.current?.planMode && !end.stopped && !isAsk && !emptyResponse) {
            next = next.filter((b) => b.kind !== 'planchoice')
            next = [...next, { kind: 'planchoice', id: nextID() }]
          }
          return next
        })
      }),
      onEvent<{ error: string }>(Events.TurnError, ({ error }) => {
        setBusy(false)
        setPermQueue([])
        setBlocks((prev) => [...finalizeTools(finalizeStreaming(prev)), { kind: 'error', id: nextID(), text: error }])
      }),
      // Enqueue (don't replace): concurrent tools may each prompt. Dedup by id in
      // case an event is delivered twice.
      onEvent<PermissionRequest>(Events.PermissionRequest, (req) =>
        setPermQueue((q) => (q.some((p) => p.id === req.id) ? q : [...q, req])),
      ),
      onEvent<{ message: string }>(Events.Warning, ({ message }) =>
        setBlocks((prev) => [...prev, { kind: 'warning', id: nextID(), text: message }]),
      ),
      // Judge ("smart") mode auto-allowed a risky action without a prompt, or tripped
      // its per-session breaker. An auto-allow is marked on the very tool card it
      // decided (by tool-use id), with the judge's reason shown when the row expands;
      // a breaker trip is a session-level notice.
      onEvent<{ tool: string; toolUseID: string; reason: string; outcome: string }>(
        Events.HarmAutoAllow,
        (e) => {
          if (e.outcome === 'breaker_tripped') {
            setBlocks((prev) => [...prev, { kind: 'warning', id: nextID(), text: '智能模式已达本会话自动放行上限，后续操作转为逐个确认' }])
            return
          }
          if (e.toolUseID) {
            setHarmAllows((prev) => ({ ...prev, [e.toolUseID]: e.reason || '模型判定为安全，无破坏性操作' }))
          }
        },
      ),
      // A turn's generated title arrived; refresh the sidebar so it shows the name.
      onEvent<{ id: string; title: string }>(Events.SessionRenamed, () => {
        void refreshRecents()
      }),
    ]
    return () => offs.forEach((off) => off && off())
  }, [])

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [blocks, pending])

  useEffect(() => {
    if (!busy) return
    const start = Date.now()
    setElapsed(0)
    const id = setInterval(() => setElapsed(Math.floor((Date.now() - start) / 1000)), 500)
    return () => clearInterval(id)
  }, [busy])

  async function handleStart(req: StartSessionRequest) {
    setStarting(true)
    setStartError('')
    try {
      const i = await startSession(req)
      setInfo(i)
      setStarted(true)
    } catch (e) {
      setStartError(String(e))
    } finally {
      setStarting(false)
    }
  }
  async function send(text: string, attach: string[] = []) {
    if ((!text && attach.length === 0) || busy) return
    const names = attach.map((p) => p.replace(/\\/g, '/').split('/').pop() || p)
    setBlocks((prev) => [...prev, { kind: 'user', id: nextID(), text, ts: now(), attachments: names.length ? names : undefined }])
    setBusy(true)
    try {
      if (attach.length) await sendMessageWithImages(text, attach)
      else await sendMessage(text)
    } catch (e) {
      setBusy(false)
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: String(e) }])
    }
  }
  async function handleSend() {
    const text = input.trim()
    if ((!text && attachments.length === 0) || busy) return
    const attach = attachments
    setInput('')
    setAttachments([])
    await send(text, attach)
  }
  async function togglePlan() {
    if (!info) return
    try {
      const i = await setPlanMode(!info.planMode)
      if (i?.model) setInfo(i)
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: String(e) }])
    }
  }
  async function chooseReasoning(scenario: string) {
    setReasonMenu(false)
    if (!info) return
    try {
      const i = await setReasoningScenario(scenario)
      if (i?.model) setInfo(i)
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: String(e) }])
    }
  }
  async function chooseThinking(effort: string) {
    setThinkMenu(false)
    if (!info) return
    try {
      const i = await setThinkingEffort(effort)
      if (i?.model) setInfo(i)
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: String(e) }])
    }
  }
  // doCompact forces a summary-compaction of the in-memory history now. The on-disk
  // log stays complete; this only shrinks what is resent to the model next turn.
  async function doCompact() {
    if (busy || compacting || !info) return
    setCompacting(true)
    try {
      const r = await compact()
      const msg =
        r.after < r.before
          ? `已压缩上下文：${r.before} → ${r.after} 条消息`
          : '暂无可压缩内容（最近对话已很精简）'
      setBlocks((prev) => [...prev, { kind: 'notice', id: nextID(), text: msg }])
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: String(e) }])
    } finally {
      setCompacting(false)
    }
  }
  // executePlanAs leaves plan mode, switches to the chosen permission mode, and
  // tells the model to carry out the plan.
  async function executePlanAs(mode: string) {
    if (busy) return
    setBlocks((prev) => prev.filter((b) => b.kind !== 'planchoice'))
    try {
      await setPermissionMode(mode)
      const i = await setPlanMode(false)
      if (i?.model) setInfo(i)
      else setInfo((prev) => (prev ? { ...prev, permissionMode: mode, planMode: false } : prev))
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: String(e) }])
      return
    }
    await send('计划已确认，请按上述方案开始执行。')
  }
  // dismissPlanChoice keeps plan mode on so the user can refine the plan or raise
  // a different idea.
  function dismissPlanChoice() {
    setBlocks((prev) => prev.filter((b) => b.kind !== 'planchoice'))
  }
  async function decide(decision: string) {
    const cur = permQueue[0]
    if (!cur) return
    // Advance the queue: drop this request so the next one surfaces, then resolve it.
    setPermQueue((q) => q.filter((p) => p.id !== cur.id))
    try {
      await resolvePermission(cur.id, decision)
    } catch {
      /* 已解决或取消 */
    }
  }
  // Deny every queued request at once — handy when a burst of concurrent tools each
  // raised a prompt and the user wants to reject them all.
  async function denyRest() {
    const rest = permQueue
    setPermQueue([])
    for (const p of rest) {
      try {
        await resolvePermission(p.id, 'deny')
      } catch {
        /* 已解决或取消 */
      }
    }
  }
  async function toggleMode() {
    if (!info) return
    const order = ['safe', 'interactive', 'judge', 'flight']
    const next = order[(order.indexOf(info.permissionMode || 'safe') + 1) % order.length]
    await pickMode(next)
  }
  // pickMode activates a specific permission mode (used by the permissions page's
  // mode cards), keeping the local session info in sync with the backend.
  async function pickMode(mode: string) {
    if (!info || info.permissionMode === mode) return
    try {
      await setPermissionMode(mode)
      setInfo({ ...info, permissionMode: mode })
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: String(e) }])
    }
  }
  // clearPlan resets the progress pill when the conversation context is replaced
  // (new/resumed/switched session).
  function clearPlan() {
    setPlan(null)
    setPlanOpen(false)
  }
  async function newChat() {
    try {
      const i = await newSession()
      setInfo(i)
      setBlocks([])
      clearPlan()
      setTokens({ in: 0, out: 0 })
      setCtxTokens(0)
      setCtxEstimated(false)
      setElapsed(0)
      refreshRecents()
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: String(e) }])
    }
  }
  // Pick a different folder and open a fresh session there (a new workspace).
  async function switchWorkspaceFolder() {
    try {
      const dir = await pickWorkspaceFolder()
      if (!dir) return // cancelled
      const i = await switchWorkspace(dir)
      setInfo(i)
      setBlocks([])
      clearPlan()
      setTokens({ in: 0, out: 0 })
      setCtxTokens(0)
      setCtxEstimated(false)
      setElapsed(0)
      setView('chat')
      refreshRecents()
    } catch (e) {
      setView('chat')
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: String(e) }])
    }
  }

  async function openRecent(id: string) {
    if (id === info?.sessionId) return
    try {
      const r = await resumeSession(id)
      setInfo(r.info)
      setBlocks(
        (r.blocks ?? []).map((b): Block => {
          if (b.kind === 'user') return { kind: 'user', id: nextID(), text: b.text ?? '', ts: '' }
          if (b.kind === 'assistant') return { kind: 'assistant', id: nextID(), text: b.text ?? '', streaming: false, ts: '' }
          // Rebuild a tool execution card from the persisted result. Live-only
          // details (colored diffs, file chips) aren't stored, so the card shows the
          // tool, its target, and the result text.
          const t = b.tool ?? {}
          const out = (t.output ?? '').trim()
          const lines = out ? out.split('\n').map((text) => ({ stream: t.isError ? 'stderr' : 'stdout', text })) : []
          return {
            kind: 'tool',
            id: nextID(),
            tool: {
              type: t.isError ? 'failed' : 'completed',
              toolName: t.toolName,
              toolUseID: t.toolUseId,
              input: t.input,
              message: t.isError ? 'completed with error' : 'completed',
              files: t.path ? [{ path: t.path }] : undefined,
              output: lines,
            },
          }
        }),
      )
      // Re-attach edit metadata to resumed tool steps by tool-use id, so the "已编辑"
      // cards + undo/review re-render (the resume payload itself carries no diffs).
      const edits = (await listEdits()) ?? []
      if (edits.length > 0) {
        const byTUID = new Map(edits.map((e) => [e.toolUseId, e]))
        setBlocks((prev) => prev.map((b) => {
          if (b.kind !== 'tool' || !b.tool.toolUseID) return b
          const e = byTUID.get(b.tool.toolUseID)
          return e ? { ...b, tool: { ...b.tool, data: e } } : b
        }))
        setRevertedEdits(new Set(edits.filter((e) => e.reverted).map((e) => e.snapshotId)))
      } else {
        setRevertedEdits(new Set())
      }
      clearPlan()
      setTokens({ in: 0, out: 0 })
      // Seed the usage bar with the reopened history's estimated occupancy so it
      // isn't 0; the first turn replaces it with the provider's exact count.
      setCtxTokens(r.contextTokens ?? 0)
      setCtxEstimated((r.contextTokens ?? 0) > 0)
      setElapsed(0)
      refreshRecents()
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: String(e) }])
    }
  }

  if (!started) {
    return (
      <div className="flex flex-col h-screen">
        <TitleBar />
        {initialReq ? (
          <StartForm onStart={handleStart} starting={starting} error={startError} initial={initialReq} />
        ) : (
          <div className="flex-1" />
        )}
      </div>
    )
  }

  const groups = groupBlocks(blocks)
  const fileSet = new Set<string>()
  let planAdds = 0
  let planDels = 0
  for (const b of blocks) {
    if (b.kind !== 'tool') continue
    if (hasDiff(b.tool) && b.tool.files?.[0]?.path) fileSet.add(b.tool.files[0].path)
    const { add, del } = diffStats(b.tool)
    planAdds += add
    planDels += del
  }
  return (
    <div className="flex flex-col h-screen">
      <TitleBar />
      <div className="flex flex-1 min-h-0">
        <Sidebar
          recents={recents}
          currentId={info?.sessionId}
          cwd={info?.cwd}
          onSwitchWorkspace={switchWorkspaceFolder}
          view={view}
          onNav={setView}
          onNew={() => {
            setView('chat')
            newChat()
          }}
          onResume={(id) => {
            setView('chat')
            openRecent(id)
          }}
        />

        <main className="flex-1 flex flex-col min-w-0 min-h-0 bg-surface">
        {/* Secondary bar below the full-width TitleBar: status + context meter + preview toggle.
            Also a drag region, since it spans the top of the main pane. */}
        <header className="h-[52px] flex-none flex items-center justify-between pl-[22px] pr-1.5 bg-surface border-b border-line2 select-none" style={DRAG}>
          <span className={`inline-flex items-center gap-1.5 text-[12.5px] ${busy ? 'text-green' : 'text-muted'}`}>
            <span className={`w-[7px] h-[7px] rounded-full ${busy ? 'bg-green blip shadow-[0_0_0_3px_rgba(31,157,99,0.16)]' : 'bg-faint'}`} />
            {busy ? '运行中' : '空闲'}
          </span>
          <div className="flex items-center gap-3">
            <div className="flex items-center gap-[18px] text-muted text-[12.5px]" style={NO_DRAG}>
              <ContextMeter used={ctxTokens} budget={info?.maxContextTokens ?? 0} estimated={ctxEstimated} onCompact={doCompact} compacting={compacting} busy={busy} />
            </div>
            <button
              style={NO_DRAG}
              className="text-muted hover:text-ink text-[12.5px] px-2"
              title="文件预览"
              onClick={() => {
                setBrowseOpen((v) => !v)
                if (browseOpen) return
                // opening: refresh files
                listFiles()
                  .then((f) => setFiles(f ?? []))
                  .catch(() => {})
              }}
            >预览</button>
          </div>
        </header>

        {view === 'settings' ? (
          <SettingsPage initial={initialReq ?? {}} info={info} onSaved={(i) => setInfo(i)} />
        ) : view === 'plugins' ? (
          <PluginsPage
            onUseSkill={(skillName) => {
              setView('chat')
              setInput((prev) => (prev.trim() ? prev + ' ' : '') + `请使用「${skillName}」技能完成：`)
              requestAnimationFrame(() => taRef.current?.focus())
            }}
            onUseAgent={(agentName) => {
              setView('chat')
              setInput((prev) => (prev.trim() ? prev + ' ' : '') + `请委派「${agentName}」子代理完成：`)
              requestAnimationFrame(() => taRef.current?.focus())
            }}
          />
        ) : view === 'permissions' ? (
          <PermissionsPage mode={info?.permissionMode} onPickMode={pickMode} />
        ) : view === 'memory' ? (
          <MemoryPage />
        ) : (
        <>
        {plan && (
          <div className="flex-none relative flex justify-center pt-3 pb-1 z-20">
            {planOpen && <div className="fixed inset-0 z-0" onClick={() => setPlanOpen(false)} />}
            <PlanPill
              plan={plan}
              open={planOpen}
              onToggle={setPlanOpen}
              filesChanged={fileSet.size}
              adds={planAdds}
              dels={planDels}
              running={busy}
            />
          </div>
        )}
        <div className="flex-1 overflow-y-auto bg-surface px-6 pt-3 pb-8" ref={scrollRef}>
          <div className="mx-auto max-w-[1200px] flex flex-col gap-6">
            {blocks.length === 0 && (
              <div className="mt-[16vh] text-center text-faint">
                <span className="inline-flex items-center justify-center w-[52px] h-[52px] rounded-[15px] mb-3.5 bg-surface border border-line2 shadow-xs"><Logo size={34} /></span>
                <p>让 XRUN 在 <code className="font-mono bg-surface border border-line2 px-2 py-0.5 rounded-md text-muted">{shorten(info?.cwd)}</code> 中探索、修改或运行点什么。</p>
              </div>
            )}
            {groups.map((g) =>
              g.kind === 'exec' ? (
                <BotRow key={g.id}><ExecutionCard tools={g.tools} harmAllows={harmAllows} /></BotRow>
              ) : g.kind === 'ask' ? (
                <BotRow key={g.id}><AskCard tool={g.tool} busy={busy} onAnswer={send} /></BotRow>
              ) : g.kind === 'edits' ? (
                <BotRow key={g.id}><EditedCards edits={g.edits} reverted={revertedEdits} onReview={openDiffTab} onUndo={handleUndo} /></BotRow>
              ) : g.kind === 'analyze' ? (
                <BotRow key={g.id}><AnalyzeCard tool={g.tool} /></BotRow>
              ) : g.block.kind === 'planchoice' ? (
                <BotRow key={g.block.id}><PlanChoiceCard busy={busy} onExecute={executePlanAs} onDismiss={dismissPlanChoice} /></BotRow>
              ) : (
                <div key={g.block.id}>
                  <BlockView block={g.block} onOpenFile={openArtifact} resolveFile={resolveWsFile} />
                  {g.block.kind === 'assistant' && (
                    <ReplyArtifacts text={g.block.text} files={files} tabs={tabs} cwd={info?.cwd ?? ''} onOpen={openArtifact} />
                  )}
                </div>
              ),
            )}
            {busy && (
              <BotRow>
                <div className="inline-flex items-center gap-2.5 text-faint py-1">
                  <span className="think-dots"><i /><i /><i /></span>
                  <span className="think-label text-[13px] font-medium tracking-wide">思考中</span>
                </div>
              </BotRow>
            )}
          </div>
        </div>

        {pending && <PermissionModal req={pending} onDecide={decide} remaining={permQueue.length - 1} onDenyRest={denyRest} />}

        <footer className="flex-none relative px-6 pt-3.5 pb-[18px] w-full max-w-[1200px] mx-auto">
          {info?.planMode && (
            <div className="flex items-center gap-2.5 mb-2 bg-primarysoft border border-primary rounded-[12px] px-3.5 py-2.5">
              <span className="text-primaryink flex-none"><Icon name="compass" size={16} /></span>
              <span className="text-[12.5px] text-primaryink flex-1 min-w-0">计划模式：只调研、产出方案，不会修改任何文件。方案给出后可选择如何继续。</span>
            </div>
          )}
          {mention && mention.trigger === '@' && skillMatches.length > 0 && (
            <div className="absolute left-6 right-6 bottom-full mb-1.5 z-10 bg-surface border border-line2 rounded-[12px] shadow-card overflow-hidden max-h-[300px] overflow-y-auto">
              <div className="px-3.5 pt-2 pb-1 text-[11.5px] text-faint">使用技能 · {skillMatches.length}</div>
              {skillMatches.map((sk, i) => (
                <div
                  key={sk.source + '/' + sk.name}
                  ref={mention.sel === i ? selItemRef : undefined}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    pickSkill(sk.name)
                  }}
                  onMouseEnter={() => setMention((m) => (m ? { ...m, sel: i } : m))}
                  className={`flex items-start gap-2.5 px-3.5 py-2 cursor-pointer ${mention.sel === i ? 'bg-primarysoft' : 'hover:bg-surface2'}`}
                >
                  <span className="text-primaryink flex-none mt-px"><Icon name="book" size={15} /></span>
                  <div className="min-w-0">
                    <div className="text-[13px] text-ink flex items-center gap-1.5">
                      {sk.name}
                      <span className="text-[10px] text-faint border border-line2 rounded px-1 py-px">{sk.source === 'user' ? '用户' : '项目'}</span>
                    </div>
                    <div className="text-[11.5px] text-faint truncate">{sk.description}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
          {mention && mention.trigger === '/' && agentMatches.length > 0 && (
            <div className="absolute left-6 right-6 bottom-full mb-1.5 z-10 bg-surface border border-line2 rounded-[12px] shadow-card overflow-hidden max-h-[300px] overflow-y-auto">
              <div className="px-3.5 pt-2 pb-1 text-[11.5px] text-faint">委派子代理 · {agentMatches.length}</div>
              {agentMatches.map((ag, i) => (
                <div
                  key={ag.source + '/' + ag.name}
                  ref={mention.sel === i ? selItemRef : undefined}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    pickAgent(ag.name)
                  }}
                  onMouseEnter={() => setMention((m) => (m ? { ...m, sel: i } : m))}
                  className={`flex items-start gap-2.5 px-3.5 py-2 cursor-pointer ${mention.sel === i ? 'bg-primarysoft' : 'hover:bg-surface2'}`}
                >
                  <span className="text-primaryink flex-none mt-px"><Icon name="bot" size={15} /></span>
                  <div className="min-w-0">
                    <div className="text-[13px] text-ink flex items-center gap-1.5">
                      {ag.name}
                      <span className="text-[10px] text-faint border border-line2 rounded px-1 py-px">{ag.source === 'user' ? '用户' : ag.source === 'project' ? '项目' : '内置'}</span>
                    </div>
                    <div className="text-[11.5px] text-faint truncate">{ag.description}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
          {mention && mention.trigger === '#' && (fileMatches.length > 0 || mention.query === '') && (
            <div className="absolute left-6 right-6 bottom-full mb-1.5 z-10 bg-surface border border-line2 rounded-[12px] shadow-card overflow-hidden max-h-[300px] overflow-y-auto">
              <div className="px-3.5 pt-2 pb-1 text-[11.5px] text-faint">引用文件 · {fileMatches.length}{fileMatches.length >= 50 ? '+' : ''}</div>
              {fileMatches.length === 0 && (
                <div className="px-3.5 py-2.5 text-[12.5px] text-faint">该工作区没有可引用的文件</div>
              )}
              {fileMatches.map((p, i) => {
                const slash = p.lastIndexOf('/')
                const name = slash >= 0 ? p.slice(slash + 1) : p
                const dir = slash >= 0 ? p.slice(0, slash) : ''
                return (
                  <div
                    key={p}
                    ref={mention.sel === i ? selItemRef : undefined}
                    onMouseDown={(e) => {
                      e.preventDefault()
                      pickFile(p)
                    }}
                    onMouseEnter={() => setMention((m) => (m ? { ...m, sel: i } : m))}
                    className={`flex items-center gap-2.5 px-3.5 py-1.5 cursor-pointer ${mention.sel === i ? 'bg-primarysoft' : 'hover:bg-surface2'}`}
                  >
                    <span className="w-6 h-6 rounded-[6px] flex-none bg-inset text-faint font-bold text-[8px] font-mono inline-flex items-center justify-center">{ext(p)}</span>
                    <span className="text-[13px] text-ink flex-none">{name}</span>
                    {dir && <span className="text-[11.5px] text-faint font-mono truncate min-w-0">{dir}</span>}
                  </div>
                )
              })}
            </div>
          )}
          {attachments.length > 0 && (
            <div className="flex flex-wrap gap-2 mb-1.5">
              {attachments.map((p, i) => {
                const name = p.replace(/\\/g, '/').split('/').pop() || p
                return (
                  <span key={p + i} className="inline-flex items-center gap-1.5 bg-surface2 border border-line2 rounded-[9px] pl-2.5 pr-1 py-1 text-[12px] text-ink max-w-[220px]">
                    <Icon name="file" size={13} />
                    <span className="truncate">{name}</span>
                    <button className="flex-none text-faint hover:text-red px-1 leading-none" title="移除附件" onClick={() => setAttachments((a) => a.filter((x) => x !== p))}>✕</button>
                  </span>
                )
              })}
            </div>
          )}
          <textarea
            ref={taRef}
            className="block w-full resize-none min-h-[46px] max-h-[200px] bg-surface text-ink border border-line2 border-b-0 rounded-t-[14px] px-4 py-3.5 outline-none placeholder:text-faint"
            value={input}
            placeholder="继续对话，@ 技能，/ 子代理，# 文件，按 Enter 发送"
            onChange={(e) => {
              setInput(e.target.value)
              syncMention(e.target.value, e.target.selectionStart ?? e.target.value.length)
            }}
            onClick={(e) => syncMention(input, e.currentTarget.selectionStart ?? input.length)}
            onKeyUp={(e) => {
              if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) {
                syncMention(input, e.currentTarget.selectionStart ?? input.length)
              }
            }}
            onKeyDown={(e) => {
              if (mention && mentionCount > 0) {
                if (e.key === 'ArrowDown') {
                  e.preventDefault()
                  setMention((m) => (m ? { ...m, sel: Math.min(m.sel + 1, mentionCount - 1) } : m))
                  return
                }
                if (e.key === 'ArrowUp') {
                  e.preventDefault()
                  setMention((m) => (m ? { ...m, sel: Math.max(m.sel - 1, 0) } : m))
                  return
                }
                if (e.key === 'Enter' || e.key === 'Tab') {
                  e.preventDefault()
                  pickMention(mention.sel)
                  return
                }
                if (e.key === 'Escape') {
                  e.preventDefault()
                  setMention(null)
                  return
                }
              }
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                handleSend()
              }
            }}
          />
          <div className="flex items-center justify-between bg-surface border border-line2 border-t-0 rounded-b-[14px] px-3 py-[9px] shadow-card">
            <div className="flex gap-1.5">
              <div className="relative">
                <button
                  onClick={() => setAddMenu((v) => !v)}
                  title="添加：技能 / 智能体 / 图片"
                  className="border-none bg-transparent text-muted text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 hover:bg-surface2 hover:text-ink"
                >
                  <Icon name="plus" size={16} />
                </button>
                {addMenu && (
                  <>
                    <div className="fixed inset-0 z-10" onClick={() => setAddMenu(false)} />
                    <div className="absolute bottom-full left-0 mb-1.5 z-20 w-[180px] bg-surface border border-line2 rounded-[11px] shadow-card overflow-hidden py-1">
                      <div onClick={() => { setAddMenu(false); openSkillPicker() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="book" size={15} /> 技能</div>
                      <div onClick={() => { setAddMenu(false); openAgentPicker() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="bot" size={15} /> 智能体</div>
                      <div onClick={() => { setAddMenu(false); openFilePicker() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="hash" size={15} /> 文件</div>
                      <div onClick={() => { setAddMenu(false); pickAttachment() }} className="px-3 py-[7px] text-[13px] cursor-pointer text-ink hover:bg-surface2 flex items-center gap-2"><Icon name="paperclip" size={15} /> 图片附件</div>
                    </div>
                  </>
                )}
              </div>
              <GhostBtn onClick={toggleMode} title="点击切换权限模式"><Icon name="shield" size={16} /> {MODE_LABEL[info?.permissionMode ?? ''] ?? '安全模式'}</GhostBtn>
              <button
                onClick={togglePlan}
                title="计划模式：只调研、产出方案，不做任何修改"
                className={`border text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 transition ${info?.planMode ? 'border-primary text-primaryink bg-primarysoft font-medium' : 'border-transparent bg-transparent text-muted hover:bg-surface2 hover:text-ink'}`}
              >
                <Icon name="compass" size={16} /> 计划模式
              </button>
              {SHOW_THINKING_MODEL && (
              <div className="relative">
                <button
                  onClick={() => setReasonMenu((v) => !v)}
                  title="思考模型：为本轮注入一套思维方法论"
                  className={`border text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 transition ${(info?.reasoningScenario ?? 'off') !== 'off' ? 'border-primary text-primaryink bg-primarysoft font-medium' : 'border-transparent bg-transparent text-muted hover:bg-surface2 hover:text-ink'}`}
                >
                  <Icon name="sparkles" size={16} /> {(info?.reasoningScenario ?? 'off') === 'off' ? '思考模型' : (REASONING_LABEL[info!.reasoningScenario!] ?? info!.reasoningScenario)}
                  <Icon name="chevron-down" size={12} />
                </button>
                {reasonMenu && (
                  <>
                    <div className="fixed inset-0 z-10" onClick={() => setReasonMenu(false)} />
                    <div className="absolute bottom-full left-0 mb-1.5 z-20 w-[224px] bg-surface border border-line2 rounded-[11px] shadow-card overflow-hidden py-1">
                      {REASONING.map((r) => (
                        <div
                          key={r.value}
                          onClick={() => chooseReasoning(r.value)}
                          className={`px-3 py-[7px] text-[13px] cursor-pointer ${(info?.reasoningScenario ?? 'off') === r.value ? 'bg-primarysoft text-primaryink font-medium' : 'text-ink hover:bg-surface2'}`}
                        >
                          {r.label}
                        </div>
                      ))}
                    </div>
                  </>
                )}
              </div>
              )}
              <div className="relative">
                <button
                  onClick={() => setThinkMenu((v) => !v)}
                  title="思考强度：让推理模型输出思考过程（reasoning_effort）"
                  className={`border text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 transition ${(info?.thinkingEffort ?? 'off') !== 'off' ? 'border-primary text-primaryink bg-primarysoft font-medium' : 'border-transparent bg-transparent text-muted hover:bg-surface2 hover:text-ink'}`}
                >
                  <Icon name="sparkles" size={16} /> {(info?.thinkingEffort ?? 'off') === 'off' ? '思考强度' : `思考 · ${THINKING_LABEL[info!.thinkingEffort!] ?? info!.thinkingEffort}`}
                  <Icon name="chevron-down" size={12} />
                </button>
                {thinkMenu && (
                  <>
                    <div className="fixed inset-0 z-10" onClick={() => setThinkMenu(false)} />
                    <div className="absolute bottom-full left-0 mb-1.5 z-20 w-[200px] bg-surface border border-line2 rounded-[11px] shadow-card overflow-hidden py-1">
                      {THINKING.map((t) => (
                        <div
                          key={t.value}
                          onClick={() => chooseThinking(t.value)}
                          className={`px-3 py-[7px] text-[13px] cursor-pointer ${(info?.thinkingEffort ?? 'off') === t.value ? 'bg-primarysoft text-primaryink font-medium' : 'text-ink hover:bg-surface2'}`}
                        >
                          {t.label}
                        </div>
                      ))}
                    </div>
                  </>
                )}
              </div>
            </div>
            <div className="flex items-center gap-3">
              <div className="relative">
                <button
                  type="button"
                  onClick={() => (modelPickerOpen ? setModelPickerOpen(false) : void openModelPicker())}
                  className="font-mono text-[12px] text-muted bg-surface2 border border-line px-[11px] py-[5px] rounded-lg inline-flex items-center gap-1.5 hover:border-primary hover:text-ink transition"
                  title="点击切换模型"
                >
                  模型 · {info?.model}
                  <Icon name="chevron-down" size={12} />
                </button>
                {modelPickerOpen && (
                  <>
                    <div className="fixed inset-0 z-10" onClick={() => setModelPickerOpen(false)} />
                    <div className="absolute bottom-full right-0 mb-2 w-[320px] max-h-[380px] bg-surface border border-line2 rounded-[13px] shadow-[0_18px_50px_rgba(30,35,60,0.22)] z-20 flex flex-col overflow-hidden">
                      <div className="p-2.5 border-b border-line">
                        <input
                          autoFocus
                          value={modelQuery}
                          onChange={(e) => setModelQuery(e.target.value)}
                          placeholder="搜索模型…"
                          className="w-full font-sans text-[13px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2 outline-none focus:border-primary"
                        />
                      </div>
                      <div className="overflow-y-auto py-1">
                        {(() => {
                          const q = modelQuery.trim().toLowerCase()
                          const matches = modelOptions.filter((m) => !q || m.id.toLowerCase().includes(q) || (m.ownedBy ?? '').toLowerCase().includes(q)).slice(0, 10)
                          if (modelOptions.length === 0) return <div className="px-3.5 py-6 text-center text-[12.5px] text-muted">无可选模型(仅通行证会话可切换)</div>
                          if (matches.length === 0) return <div className="px-3.5 py-6 text-center text-[12.5px] text-muted">没有匹配的模型</div>
                          return matches.map((m) => (
                            <button
                              key={m.id}
                              type="button"
                              onClick={() => void pickModel(m.id)}
                              className={`w-full text-left px-3.5 py-2 flex items-center gap-2 hover:bg-surface2 transition ${m.id === info?.model ? 'text-primary' : 'text-ink'}`}
                            >
                              <span className="font-mono text-[12.5px] truncate flex-1">{m.id}</span>
                              {m.ownedBy && <span className="text-[11px] text-faint flex-none">{m.ownedBy}</span>}
                              {m.id === info?.model && <span className="text-primary text-[13px] flex-none">✓</span>}
                            </button>
                          ))
                        })()}
                      </div>
                    </div>
                  </>
                )}
              </div>
              {busy ? (
                <button className="w-10 h-10 border-none rounded-[11px] flex-none bg-red text-white inline-flex items-center justify-center cursor-pointer shadow-[0_5px_14px_rgba(224,86,74,0.3)] hover:brightness-105" onClick={() => interrupt()} title="停止"><Icon name="stop" size={16} /></button>
              ) : (
                <button className="w-10 h-10 border-none rounded-[11px] flex-none bg-primary text-white inline-flex items-center justify-center cursor-pointer shadow-[0_5px_14px_rgba(91,108,240,0.32)] hover:brightness-105 disabled:opacity-40 disabled:shadow-none disabled:cursor-default" onClick={handleSend} disabled={!input.trim() && attachments.length === 0} title="发送"><Icon name="send" size={17} /></button>
              )}
            </div>
          </div>
        </footer>
        </>
        )}
        </main>
        {view === 'chat' && (tabs.length > 0 || browseOpen) && (
          <>
            <div
              className="w-[6px] flex-none cursor-col-resize bg-surface2 hover:bg-line2 active:bg-primary/40 transition-colors"
              onPointerDown={onPreviewDragStart}
              onPointerMove={onPreviewDragMove}
              onPointerUp={onPreviewDragEnd}
            />
            <aside style={{ width: previewWidth, maxWidth: '60%' }} className="flex-none flex flex-col min-h-0 bg-surface">
              {tabs.length > 0 ? (
                <PreviewPane
                  tabs={tabs}
                  active={activeTab}
                  baseURL={info?.previewBaseURL ?? ''}
                  onSelect={setActiveTab}
                  onCloseTab={closePreviewTab}
                  onClose={() => { setTabs([]); setActiveTab(null) }}
                />
              ) : (
                <div className="flex flex-col h-full min-h-0">
                  <div className="flex-none flex items-center h-[44px] px-3 border-b border-line2">
                    <span className="text-[13px] font-medium text-ink flex-1">文件预览</span>
                    <button className="text-muted hover:text-ink px-1.5" title="关闭" onClick={() => setBrowseOpen(false)}>✕</button>
                  </div>
                  <div className="flex-1 min-h-0">
                    <FileBrowser files={files} onPick={(p) => { if (isPreviewable(p)) openArtifact(toWorkspaceRel(p, info?.cwd ?? '')) }} autoOpen={autoOpen} onToggleAutoOpen={toggleAutoOpen} />
                  </div>
                </div>
              )}
            </aside>
          </>
        )}
      </div>
    </div>
  )
}

// CheckMark is the small tick used by the completed marker and the done footer.
function CheckMark({ size = 9, className }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={3.2} strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden>
      <path d="M5 12l4.5 4.5L19 7" />
    </svg>
  )
}

// Spinner is a small ring that rotates while a step is in flight (used by the
// progress pill and running tool rows).
function Spinner({ size = 14 }: { size?: number }) {
  return (
    <span
      className="spin-ring inline-block flex-none rounded-full border-2 border-[var(--color-primarysoft)] border-t-[var(--color-primary)]"
      style={{ width: size, height: size }}
    />
  )
}

// PlanPill is the top-center progress board: a compact pill showing the current
// step, files changed, and the running diff totals. Clicking it drops down the full
// task timeline (the same spine list). It stays visible whenever a plan exists.
function PlanPill({
  plan,
  open,
  onToggle,
  filesChanged,
  adds,
  dels,
  running,
}: {
  plan: PlanSnapshot
  open: boolean
  onToggle: (open: boolean) => void
  filesChanged: number
  adds: number
  dels: number
  running: boolean
}) {
  const allDone = plan.total > 0 && plan.done >= plan.total
  const activeIndex = plan.items.findIndex((it) => it.status === 'in_progress')
  // "第 N / M 步": the running step if one is active, otherwise how many are done.
  const step = activeIndex >= 0 ? activeIndex + 1 : plan.done
  const frontier = activeIndex >= 0 ? activeIndex : plan.done
  const live = running || activeIndex >= 0
  return (
    <>
      <button
        onClick={() => onToggle(!open)}
        className={`relative z-10 inline-flex items-center gap-2.5 pl-3 pr-2.5 py-2 rounded-full bg-surface shadow-card text-[12.5px] cursor-pointer transition border ${open ? 'border-primary' : 'border-line2 hover:border-primary'}`}
      >
        {allDone ? (
          <CheckMark size={13} className="text-green flex-none" />
        ) : live ? (
          <Spinner size={13} />
        ) : (
          <span className="w-2 h-2 rounded-full bg-primary flex-none" />
        )}
        <span className="text-ink whitespace-nowrap">
          第 <b className="font-semibold tabular-nums">{step}</b> <span className="text-faint">/ {plan.total}</span> 步
        </span>
        {filesChanged > 0 && (
          <>
            <span className="w-px h-3.5 bg-line2 flex-none" />
            <span className="text-muted whitespace-nowrap">{filesChanged} 个文件已更改</span>
            {(adds > 0 || dels > 0) && (
              <span className="font-mono tabular-nums text-[12px] whitespace-nowrap">
                <span className="text-green">+{adds}</span> <span className={dels > 0 ? 'text-red' : 'text-faint'}>−{dels}</span>
              </span>
            )}
          </>
        )}
        <Icon name="chevron-down" size={14} className={`flex-none text-faint transition ${open ? 'rotate-180' : ''}`} />
      </button>
      {open && (
        <div className="absolute top-full left-1/2 -translate-x-1/2 mt-1.5 w-[380px] max-w-[calc(100vw-40px)] z-10">
          <div className="bg-surface border border-line2 rounded-[14px] shadow-card overflow-hidden anim-rise">
            <div className="px-4 py-3 max-h-[52vh] overflow-y-auto">
              {plan.items.map((it, i) => (
                <PlanRow key={i} item={it} last={i === plan.items.length - 1} filled={i < frontier} />
              ))}
            </div>
          </div>
        </div>
      )}
    </>
  )
}

// PlanRow is one step on the timeline: a status marker in the left gutter, a spine
// segment dropping from it to the next marker (primary when `filled`, faint
// otherwise), and the label. The active step prefers its present-continuous
// activeForm and reads in primary; completed steps dim; pending steps stay muted.
function PlanRow({ item, last, filled }: { item: PlanItem; last: boolean; filled: boolean }) {
  const inProgress = item.status === 'in_progress'
  const done = item.status === 'completed'
  const label = inProgress && item.activeForm?.trim() ? item.activeForm : item.content
  const textClass = done ? 'text-faint' : inProgress ? 'text-primaryink font-medium' : 'text-muted'
  return (
    <div className="flex gap-3">
      <div className="w-[13px] flex-none flex flex-col items-center">
        <PlanMark status={item.status} />
        {!last && <span className={`w-[2px] flex-1 min-h-[14px] my-1 rounded-full ${filled ? 'bg-primary' : 'bg-line2'}`} />}
      </div>
      <span className={`min-w-0 break-words text-[13px] leading-[1.5] ${last ? 'pb-1' : 'pb-4'} ${textClass}`}>{label}</span>
    </div>
  )
}

// PlanMark is the per-step glyph: a filled primary check (completed), a primary core
// dot inside a pulsing halo (in progress), or a hollow faint dot (pending).
function PlanMark({ status }: { status: string }) {
  if (status === 'completed') {
    return (
      <span className="w-[13px] h-[13px] flex-none rounded-full bg-primary inline-flex items-center justify-center text-white">
        <CheckMark size={8} />
      </span>
    )
  }
  if (status === 'in_progress') {
    return (
      <span className="relative w-[13px] h-[13px] flex-none inline-flex items-center justify-center">
        <span className="absolute w-[13px] h-[13px] rounded-full plan-pulse" />
        <span className="relative w-[9px] h-[9px] rounded-full bg-primary ring-2 ring-surface" />
      </span>
    )
  }
  return <span className="w-[13px] h-[13px] flex-none rounded-full border-[1.6px] border-line2 bg-surface" />
}

// BotRow wraps an assistant-side entry (message, tool card, notice). The assistant
// output is left-aligned and flows naturally, while user messages are right-aligned flat blocks.
function BotRow({ children }: { children: ReactNode }) {
  return <div className="anim-rise min-w-0">{children}</div>
}

function GhostBtn({ children, onClick, title }: { children: ReactNode; onClick?: () => void; title?: string }) {
  return (
    <button className="border-none bg-transparent text-muted text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 hover:bg-surface2 hover:text-ink" onClick={onClick} title={title}>
      {children}
    </button>
  )
}

// ReplyArtifacts renders the regex-matched workspace files mentioned in an assistant
// reply as clickable cards. Memoized so it only recomputes when the reply text or the
// workspace file list changes — not on every streaming re-render.
function ReplyArtifacts({ text, files, tabs, cwd, onOpen }: { text: string; files: string[]; tabs: PreviewTab[]; cwd: string; onOpen: (relPath: string) => void }) {
  const paths = useMemo(() => matchWorkspaceFiles(extractFilePaths(text), files), [text, files])
  if (paths.length === 0) return null
  return (
    <div className="flex flex-col gap-1.5 mt-1.5">
      {paths.map((p) => (
        <ArtifactCard key={p} relPath={p} add={0} del={0} onOpen={onOpen} autoOpened={tabs.some((t) => t.kind === 'file' && t.relPath === p)} />
      ))}
    </div>
  )
}

// The OS title bar is hidden (Frameless), so a region must be marked draggable
// and the window controls provided in-app. DRAG marks a drag region; NO_DRAG opts
// interactive children out of dragging.
const DRAG = { ['--wails-draggable' as string]: 'drag' } as CSSProperties
const NO_DRAG = { ['--wails-draggable' as string]: 'no-drag' } as CSSProperties

// TitleBar is the full-width top row (the frameless-window drag region): the XRUN
// wordmark on the left, an empty drag middle, and the window controls at the right.
function TitleBar() {
  return (
    <div className="h-[38px] flex-none flex items-center pl-3.5 bg-surface border-b border-line2 select-none" style={DRAG}>
      <span className="flex items-center gap-2 font-semibold text-[13.5px] tracking-tight">
        <span className="w-[20px] h-[20px] inline-flex items-center justify-center"><Logo size={18} /></span>
        XRUN
      </span>
      <div className="flex-1" />
      <WindowControls />
    </div>
  )
}

// WindowControls is the minimize / maximize / close cluster, placed at the far
// right of whichever bar hosts it.
function WindowControls() {
  const rt = () => window.runtime
  return (
    <div className="flex items-center" style={NO_DRAG}>
      <button className="w-11 h-[34px] inline-flex items-center justify-center text-muted rounded-md hover:bg-surface2" title="最小化" onClick={() => rt().WindowMinimise()}>
        <Icon name="win-min" size={15} />
      </button>
      <button className="w-11 h-[34px] inline-flex items-center justify-center text-muted rounded-md hover:bg-surface2" title="最大化" onClick={() => rt().WindowToggleMaximise()}>
        <Icon name="win-max" size={13} />
      </button>
      <button className="w-11 h-[34px] inline-flex items-center justify-center text-muted rounded-md hover:bg-red hover:text-white" title="关闭" onClick={() => rt().Quit()}>
        <Icon name="win-close" size={15} />
      </button>
    </div>
  )
}

function Sidebar({
  recents,
  currentId,
  cwd,
  onSwitchWorkspace,
  view,
  onNav,
  onNew,
  onResume,
}: {
  recents: SessionSummary[]
  currentId?: string
  cwd?: string
  onSwitchWorkspace: () => void
  view: 'chat' | 'settings' | 'plugins' | 'permissions' | 'memory'
  onNav: (v: 'chat' | 'settings' | 'plugins' | 'permissions' | 'memory') => void
  onNew: () => void
  onResume: (id: string) => void
}) {
  const wsName = cwd ? cwd.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || cwd : '—'
  const nav = [
    { label: '对话', name: 'chat', view: 'chat' as const },
    { label: '插件', name: 'grid', view: 'plugins' as const },
    { label: '记忆', name: 'file', view: 'memory' as const },
    { label: '权限', name: 'shield', view: 'permissions' as const },
    { label: '设置', name: 'settings', view: 'settings' as const },
  ]
  return (
    <aside className="w-[268px] flex-none bg-surface border-r border-line2 flex flex-col p-4">
      <button className="w-full border-none bg-primary text-white font-semibold text-sm py-3 rounded-[11px] cursor-pointer inline-flex items-center justify-center gap-2 shadow-[0_5px_14px_rgba(91,108,240,0.3)] hover:brightness-105 transition" onClick={onNew}>
        <Icon name="plus" size={16} /> 新建对话
      </button>
      <nav className="mt-[18px] flex flex-col gap-0.5">
        {nav.map((n) => (
          <div
            key={n.label}
            onClick={() => onNav(n.view)}
            className={`flex items-center gap-[11px] px-[11px] py-[9px] rounded-[9px] text-sm cursor-pointer select-none ${
              view === n.view ? 'bg-primarysoft text-primaryink font-semibold' : 'text-muted hover:bg-surface2 hover:text-ink'
            }`}
          >
            <Icon name={n.name} size={18} />
            {n.label}
          </div>
        ))}
      </nav>
      <div className="mt-[22px] flex-1 overflow-y-auto -mr-1 pr-1">
        <div className="text-[11.5px] text-faint px-[11px] pb-2 tracking-wide">最近对话</div>
        {recents.length === 0 ? (
          <div className="text-faint text-[13px] px-[11px] py-1">暂无对话</div>
        ) : (
          recents.map((s) => {
            const active = s.id === currentId
            return (
              <div
                key={s.id}
                onClick={() => onResume(s.id)}
                title={s.title}
                className={`flex items-center gap-2.5 px-[11px] py-[9px] rounded-[9px] cursor-pointer text-[13.5px] mb-0.5 ${
                  active ? 'text-ink bg-surface2 shadow-[inset_2px_0_0_var(--color-primary)]' : 'text-muted hover:bg-surface2'
                }`}
              >
                <Icon name="file" size={15} />
                <span className="flex-1 min-w-0 truncate">{s.title}</span>
                <span className="text-faint text-[11px] flex-none">{s.when}</span>
              </div>
            )
          })
        )}
      </div>
      <button
        onClick={onSwitchWorkspace}
        title={`当前工作区:${cwd || '—'}\n点击切换到其它目录`}
        className="mt-3 flex-none flex items-center gap-2 px-[11px] py-2.5 rounded-[10px] border border-line2 bg-surface hover:border-primary hover:bg-surface2 text-muted hover:text-ink transition"
      >
        <Icon name="folder" size={16} />
        <span className="flex-1 min-w-0 truncate text-left font-mono text-[12.5px]">{wsName}</span>
        <span className="text-faint text-[11px] flex-none">切换</span>
      </button>
    </aside>
  )
}

// AskCard renders an AskUser tool call as an interactive question: the user picks
// a suggested option or types a custom reply, which is sent as the next message.
function AskCard({ tool, busy, onAnswer }: { tool: ToolEvent; busy: boolean; onAnswer: (text: string) => void }) {
  const { question, options } = askPayload((tool as ToolEvent & { input?: unknown }).input)
  const [custom, setCustom] = useState('')
  const [answered, setAnswered] = useState<string | null>(null)
  function answer(text: string) {
    const t = text.trim()
    if (!t || answered || busy) return
    setAnswered(t)
    onAnswer(t)
  }
  return (
    <div className="anim-rise">
      <div className="min-w-0 flex-1 bg-surface border border-primary rounded-[14px] shadow-xs p-4">
        <div className="flex items-center gap-2 mb-2 text-primaryink">
          <Icon name="chat" size={16} />
          <span className="text-[12.5px] font-semibold">需要你的确认</span>
        </div>
        <div className="text-[14px] text-ink whitespace-pre-wrap mb-3">{question || '（无问题内容）'}</div>
        {answered ? (
          <div className="text-[13px] text-muted">已回复：<span className="text-ink">{answered}</span></div>
        ) : (
          <>
            {options.length > 0 && (
              <div className="flex flex-col gap-1.5 mb-2.5">
                {options.map((opt, i) => (
                  <button
                    key={i}
                    onClick={() => answer(opt)}
                    disabled={busy}
                    className="text-left text-[13.5px] text-ink bg-surface2 border border-line2 rounded-[10px] px-3.5 py-2 cursor-pointer hover:border-primary hover:bg-primarysoft disabled:opacity-40"
                  >
                    {opt}
                  </button>
                ))}
              </div>
            )}
            <div className="flex gap-2">
              <input
                value={custom}
                onChange={(e) => setCustom(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') answer(custom)
                }}
                placeholder="或输入你的回答…"
                className="flex-1 text-[13.5px] bg-surface2 text-ink border border-line2 rounded-[10px] px-3 py-2 outline-none focus:border-primary"
              />
              <button
                onClick={() => answer(custom)}
                disabled={!custom.trim() || busy}
                className="text-[13px] font-semibold text-white bg-primary px-3.5 rounded-[10px] cursor-pointer hover:brightness-105 disabled:opacity-40 disabled:cursor-default"
              >
                发送
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// PlanChoiceCard appears after a plan-mode turn: the user picks how to carry out
// the plan (interactive or judge permission mode) or keeps refining it.
function PlanChoiceCard({ busy, onExecute, onDismiss }: { busy: boolean; onExecute: (mode: string) => void; onDismiss: () => void }) {
  return (
    <div className="anim-rise">
      <div className="min-w-0 flex-1 bg-surface border border-primary rounded-[14px] shadow-xs p-4">
        <div className="flex items-center gap-2 mb-1 text-primaryink">
          <Icon name="compass" size={16} />
          <span className="text-[12.5px] font-semibold">方案已就绪，如何继续？</span>
        </div>
        <div className="text-[12.5px] text-muted mb-3">选择执行模式后将退出计划模式并开始执行；也可先不执行、继续补充想法。</div>
        <div className="flex flex-wrap gap-2">
          <button
            onClick={() => onExecute('interactive')}
            disabled={busy}
            className="text-[13px] font-semibold text-white bg-primary px-3.5 py-2 rounded-[10px] cursor-pointer hover:brightness-105 disabled:opacity-40 disabled:cursor-default"
          >
            交互模式执行
          </button>
          <button
            onClick={() => onExecute('judge')}
            disabled={busy}
            className="text-[13px] font-semibold text-primaryink bg-primarysoft border border-primary px-3.5 py-2 rounded-[10px] cursor-pointer hover:brightness-105 disabled:opacity-40 disabled:cursor-default"
          >
            智能模式执行
          </button>
          <button
            onClick={onDismiss}
            disabled={busy}
            className="text-[13px] text-muted bg-surface2 border border-line2 px-3.5 py-2 rounded-[10px] cursor-pointer hover:text-ink disabled:opacity-40 disabled:cursor-default"
          >
            先不执行 / 继续补充
          </button>
        </div>
      </div>
    </div>
  )
}

// ExecutionCard renders a run of tool calls as a compact, scannable list: one line
// per tool (type icon + verb/target + status), each click-to-expand for its detail.
// A running tool auto-expands so its streaming output stays visible.
// ExecutionCard is the agent's activity panel: a full-width card (no avatar) whose
// rows are the tool calls in one run. Each row shows the tool's icon chip, its verb +
// mono target, a diff badge for edits, and a live/done/failed status; clicking a row
// drills into its parameters and output.
function ExecutionCard({ tools, harmAllows }: { tools: ToolEvent[]; harmAllows?: Record<string, string> }) {
  const [sel, setSel] = useState<number | null>(null)
  const runningIdx = tools.findIndex((t) => t.type !== 'completed' && t.type !== 'failed')
  const activeIdx = sel != null && sel < tools.length ? sel : runningIdx

  return (
    <div className="anim-rise">
      {tools.length > 1 && (
        <div className="flex items-center gap-1.5 px-1 pb-1.5 text-[12px] text-faint">
          <Icon name="terminal" size={13} className="flex-none" />
          <span className="font-medium">执行过程</span>
          <span className="font-mono">· {tools.length} 步</span>
          {runningIdx >= 0 && <span className="w-[6px] h-[6px] rounded-full bg-primary blip ml-0.5" />}
        </div>
      )}
      {tools.map((t, i) => {
        const st = t.type === 'failed' ? 'failed' : t.type === 'completed' ? 'done' : 'running'
        const active = activeIdx === i
        const { verb, target } = toolVerbTarget(t)
        const { add, del } = diffStats(t)
        const showDiff = add + del > 0
        const allowReason = harmAllows && t.toolUseID ? harmAllows[t.toolUseID] : undefined
        const iconColor = st === 'failed' ? 'text-red' : st === 'running' ? 'text-primary' : 'text-faint'
        const rowBg = active ? 'bg-surface2' : 'hover:bg-surface2'
        return (
          <div key={i}>
            <div
              onClick={() => setSel(sel === i ? null : i)}
              className={`flex items-center gap-2.5 px-2 py-1.5 rounded-lg cursor-pointer select-none ${rowBg}`}
            >
              <span className={`flex-none ${iconColor}`}><Icon name={toolIcon(t.toolName)} size={15} /></span>
              <span className="flex-1 min-w-0 truncate text-[13.5px] text-[#3f4653]">
                {verb}
                {target && <span className="font-mono text-faint"> {target}</span>}
              </span>
              {showDiff && (
                <span className="font-mono text-[11.5px] tabular-nums flex-none">
                  <span className="text-green">+{add}</span> <span className={del > 0 ? 'text-red' : 'text-faint'}>−{del}</span>
                </span>
              )}
              {allowReason && (
                <span title="智能模式已自动放行，展开查看原因" className="flex-none inline-flex items-center gap-1 text-[10.5px] text-primaryink bg-primarysoft rounded px-1.5 py-0.5">
                  <Icon name="shield" size={11} /> 智能放行
                </span>
              )}
              {st === 'failed' ? (
                <span className="text-[11px] text-red bg-redbg rounded-md px-1.5 py-0.5 flex-none">{failText(t)}</span>
              ) : st === 'running' ? (
                <Spinner size={14} />
              ) : (
                <span className="text-green flex-none"><CheckMark size={14} /></span>
              )}
              <Icon name="chevron-down" size={13} className={`flex-none text-faint transition ${active ? 'rotate-180' : ''}`} />
            </div>
            {active && (
              <div className="px-2 pb-3 pt-0.5">
                <ToolDetail tool={t} />
                {allowReason && (
                  <div className="mt-2 flex items-start gap-1.5 text-[12px] text-muted bg-primarysoft rounded-lg px-2.5 py-2">
                    <span className="flex-none mt-px text-primaryink"><Icon name="shield" size={13} /></span>
                    <span><b className="text-primaryink font-medium">智能放行</b>：{allowReason}</span>
                  </div>
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

// AgentTaskCard renders a Task delegation as a live, observable nested view: the
// sub-agent's streamed text plus its own tool calls (each drillable). It is
// expanded while the sub-agent runs and collapses to a summary when it finishes.
function AgentTaskCard({ tool, nested }: { tool: ToolEvent; nested?: AgentNested }) {
  const running = tool.type !== 'completed' && tool.type !== 'failed'
  const failed = tool.type === 'failed'
  const [open, setOpen] = useState(false)
  const [selTool, setSelTool] = useState<number | null>(null)
  const expanded = running || open
  const meta = (() => {
    const raw = (tool as ToolEvent & { input?: unknown }).input
    const o: { subagent_type?: string; description?: string } =
      typeof raw === 'string' ? (() => { try { return JSON.parse(raw) } catch { return {} } })() : ((raw as object) ?? {})
    return { sub: o.subagent_type || nested?.agent || '子代理', desc: o.description || '' }
  })()
  const tools = nested?.tools ?? []
  // Auto-expand the running child tool (like the top-level exec card) so its
  // streaming output shows live; an explicit click takes over.
  const runningChild = tools.findIndex((ct) => ct.type !== 'completed' && ct.type !== 'failed')
  const activeChild = selTool != null && selTool < tools.length ? selTool : runningChild
  return (
    <div className="anim-rise flex flex-col gap-2">
      <div className={`flex items-center gap-2 text-[12.5px] ${running ? '' : 'cursor-pointer'}`} onClick={() => !running && setOpen((o) => !o)}>
        <span className="inline-flex items-center text-primary flex-none"><Icon name="bot" size={14} /></span>
        <span className="font-medium text-ink">委派子代理 · {meta.sub}</span>
        <span className="inline-flex items-center gap-1.5 text-faint font-mono flex-none">
          <span className={`w-[6px] h-[6px] rounded-full ${running ? 'bg-primary blip' : failed ? 'bg-red' : 'bg-green'}`} />
          {running ? '运行中…' : failed ? '失败' : `${tools.length} 步`}
        </span>
        {nested?.usage && (nested.usage.inTok > 0 || nested.usage.outTok > 0) && (
          <span className="text-[11px] text-faint font-mono tabular-nums flex-none" title="子代理自身用量与运行时间(与主回复分开计)">
            ↑{fmtTokens(nested.usage.inTok)} ↓{fmtTokens(nested.usage.outTok)}{nested.usage.durMs ? ` · ${fmtDuration(nested.usage.durMs)}` : ''}
          </span>
        )}
        {!running && (
          <button className="ml-1 flex-none text-faint hover:text-ink inline-flex items-center gap-1 cursor-pointer" onClick={(e) => { e.stopPropagation(); setOpen((o) => !o) }}>
            {open ? '收起' : '展开'}
            <Icon name="chevron-down" size={13} className={open ? 'rotate-180 transition' : 'transition'} />
          </button>
        )}
      </div>
      {meta.desc && <div className="text-[12.5px] text-faint break-words">{meta.desc}</div>}
      {expanded && (
        <div className="flex flex-col gap-2">
          {tools.length > 0 && (
            <div className="flex flex-col gap-0.5">
              {tools.map((ct, i) => {
                const st = ct.type === 'failed' ? 'failed' : ct.type === 'completed' ? 'done' : 'running'
                const active = activeChild === i
                const iconColor = st === 'failed' ? 'text-red' : st === 'running' ? 'text-primary' : 'text-faint'
                return (
                  <div key={i}>
                    <div onClick={() => setSelTool(active ? null : i)} className={`flex items-center gap-2 text-[13px] px-2 py-1.5 rounded-lg cursor-pointer select-none ${active ? 'bg-surface2' : 'hover:bg-surface2'}`}>
                      <span className={`flex-none ${iconColor}`}><Icon name={toolIcon(ct.toolName)} size={14} /></span>
                      <span className="flex-1 min-w-0 truncate text-[#3f4653]">{toolLabel(ct)}</span>
                      {st === 'running' ? <Spinner size={13} /> : st === 'failed' ? <span className="text-red text-[12px] flex-none">✕</span> : <span className="text-green flex-none"><CheckMark size={13} /></span>}
                      <Icon name="chevron-down" size={12} className={`flex-none text-faint transition ${active ? 'rotate-180' : ''}`} />
                    </div>
                    {active && <div className="px-2 pb-2 pt-0.5"><ToolDetail tool={ct} /></div>}
                  </div>
                )
              })}
            </div>
          )}
          {nested?.text ? (
            <div className="text-[13.5px] text-[#3f4653] leading-[1.7] break-words">
              <Markdown>{nested.text}</Markdown>
              {running && <span className="caret">▍</span>}
            </div>
          ) : running ? (
            <div className="text-faint text-[12.5px]">子代理思考中…</div>
          ) : null}
        </div>
      )}
    </div>
  )
}

// formatInput renders a tool call's raw arguments as pretty JSON. Live events
// carry the parsed value; resumed sessions carry a JSON string — both are handled.
function formatInput(input: unknown): string {
  if (input == null || input === '') return ''
  if (typeof input === 'string') {
    try {
      return JSON.stringify(JSON.parse(input), null, 2)
    } catch {
      return input
    }
  }
  try {
    return JSON.stringify(input, null, 2)
  } catch {
    return String(input)
  }
}

// useStickToBottom keeps a scroll container pinned to its newest content while it
// streams, unless the user has scrolled up to read — then it leaves them be. dep
// changes (growing text/output) trigger the re-pin.
function useStickToBottom(dep: unknown) {
  const ref = useRef<HTMLDivElement>(null)
  const stick = useRef(true)
  useEffect(() => {
    const el = ref.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [dep])
  const onScroll = () => {
    const el = ref.current
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48
  }
  return { ref, onScroll }
}

// kvRows renders a compact key→value table for a tool's structured input.
function kvRows(rows: ([string, unknown] | false | null | undefined)[]) {
  const pairs = rows.filter(Boolean) as [string, unknown][]
  return (
    <div className="flex flex-col gap-1.5 bg-surface2 border border-line rounded-[8px] py-2 px-2.5">
      {pairs.map(([k, v], i) => (
        <div key={i} className="flex gap-2.5 text-[12.5px] min-w-0">
          <span className="text-faint flex-none w-[48px]">{k}</span>
          <span className="font-mono text-ink break-all min-w-0">{String(v ?? '')}</span>
        </div>
      ))}
    </div>
  )
}

// RawJson shows a tool's raw arguments as pretty JSON, collapsed by default when
// long so a big payload does not flood the card.
function RawJson({ value }: { value: unknown }) {
  const text = formatInput(value)
  const [open, setOpen] = useState(text.length <= 400)
  if (!text || text === '{}') return <div className="text-faint text-[12.5px] bg-surface2 border border-line rounded-[8px] py-2 px-2.5">（无入参）</div>
  return (
    <div>
      {text.length > 400 && (
        <button onClick={() => setOpen((v) => !v)} className="text-[12px] text-muted hover:text-ink inline-flex items-center gap-1 mb-1.5">
          <Icon name="chevron-down" size={13} className={open ? 'rotate-180 transition' : 'transition'} />
          {open ? '收起原始入参' : `展开原始入参 · ${text.length} 字符`}
        </button>
      )}
      {open && (
        <pre className="m-0 font-mono text-[12px] leading-[1.5] bg-surface2 border border-line rounded-[8px] py-2 px-2.5 max-h-[200px] overflow-auto whitespace-pre-wrap break-all">
          {text.length > 16000 ? text.slice(0, 16000) + '\n… 已截断' : text}
        </pre>
      )}
    </div>
  )
}

// ToolInputView renders a tool's input in a clean, tool-specific shape — a command
// block, a key/value table, etc. — falling back to collapsible raw JSON for tools
// without a tailored view.
function ToolInputView({ tool }: { tool: ToolEvent }) {
  const o = toolInputObj(tool)
  const s = (k: string) => (o[k] != null ? String(o[k]) : '')
  switch (tool.toolName) {
    case 'Bash':
      if (s('command')) return (
        <div>
          <pre className="m-0 font-mono text-[12.5px] leading-[1.5] bg-surface2 border border-line rounded-[8px] py-2 px-2.5 max-h-[160px] overflow-auto whitespace-pre-wrap break-all text-ink">{s('command')}</pre>
          {(o.timeout || o.run_in_background) ? <div className="text-[11.5px] text-faint mt-1">{o.timeout ? `超时 ${s('timeout')}ms` : ''}{o.run_in_background ? (o.timeout ? ' · ' : '') + '后台运行' : ''}</div> : null}
        </div>
      )
      break
    case 'Read':
      if (s('path')) return kvRows([['路径', s('path')], (o.offset || o.limit) && ['范围', `第 ${o.offset ?? 0} 行起${o.limit ? `，≤ ${s('limit')} 行` : ''}`], o.pages && ['页', s('pages')]])
      break
    case 'Grep':
      if (s('pattern')) return kvRows([['模式', s('pattern')], o.path && ['路径', s('path')], o.glob && ['glob', s('glob')], o.type && ['类型', s('type')], o.output_mode && ['输出', s('output_mode')]])
      break
    case 'Glob':
      if (s('pattern')) return kvRows([['模式', s('pattern')], o.path && ['路径', s('path')]])
      break
    case 'Write':
    case 'Edit':
    case 'Delete':
      if (s('path')) return kvRows([['路径', s('path')], tool.toolName === 'Delete' && o.permanent && ['方式', '永久删除']])
      break
    case 'WebFetch':
      if (s('url')) return kvRows([['URL', s('url')]])
      break
  }
  return <RawJson value={o} />
}

// ToolDetail shows one tool call's input arguments and its return content
// (matched files, command/diff output, or a result message).
function ToolDetail({ tool }: { tool: ToolEvent }) {
  const matched = (tool.files ?? []).filter((f) => f.kind === 'matched')
  const out = tool.output ?? []
  const outScroll = useStickToBottom(out.length)
  const img = tool.image
  const imgSrc = img ? (img.url || (img.data ? `data:${img.mediaType || 'image/png'};base64,${img.data}` : '')) : ''
  return (
    <div className="min-w-0 flex flex-col gap-2.5">
      <div>
        <div className="text-[11px] text-faint mb-1 tracking-wide">参数</div>
        <ToolInputView tool={tool} />
      </div>

      <div>
        <div className="text-[11px] text-faint mb-1 tracking-wide">
          输出{imgSrc ? ' · 图片' : matched.length > 0 ? ` · ${matched.length}${tool.filesTotal && tool.filesTotal > matched.length ? `/${tool.filesTotal}` : ''} 个匹配` : out.length > 0 ? ` · ${out.length} 行` : ''}
        </div>
        {imgSrc ? (
        <div className="bg-surface2 border border-line rounded-[8px] p-2 inline-block max-w-full">
          <img src={imgSrc} alt="" className="max-h-[340px] max-w-full rounded-[5px] block" />
        </div>
      ) : matched.length > 0 ? (
        <ul className="list-none m-0 p-0 flex flex-col gap-1 bg-surface2 border border-line rounded-[8px] py-2 px-2.5 max-h-[360px] overflow-auto">
          {matched.slice(0, 400).map((f, i) => (
            <li key={i} className="font-mono text-[12px] text-ink truncate" title={f.path}>{f.path}</li>
          ))}
        </ul>
      ) : out.length > 0 ? (
        <pre ref={outScroll.ref} onScroll={outScroll.onScroll} className="m-0 font-mono text-[12px] leading-[1.55] bg-surface2 border border-line rounded-[8px] py-2 max-h-[360px] overflow-auto">
          {out.slice(0, 400).map((l, i) => (
            <div key={i} className={(l.stream || '').startsWith('diff') ? `cl ${l.stream}` : lineClass(l.stream)}>{l.text}</div>
          ))}
          {tool.outputTruncated && <div className="px-2.5 text-faint">… 输出已截断</div>}
        </pre>
      ) : (
        <div className="text-faint text-[12.5px] bg-surface2 border border-line rounded-[8px] py-2 px-2.5">（无返回内容）</div>
      )}
      </div>
    </div>
  )
}

// ToolPreview renders the tool cards with mock data so the styling can be reviewed
// without a live session (mounted via ?preview=tools). Not used in normal flow.
export function ToolPreview() {
  const bash: ToolEvent = {
    type: 'completed', toolName: 'Bash', toolUseID: 'b1',
    input: { command: 'go test ./internal/repl/ -run TestStream -count=1', timeout: 120000 },
    output: [
      { stream: 'stdout', text: 'ok  \tgithub.com/wt68/runcode/internal/repl\t2.81s' },
      { stream: 'info', text: '退出码 0 · 2.83s' },
    ],
  }
  const edit: ToolEvent = {
    type: 'completed', toolName: 'Edit', toolUseID: 'e1',
    input: { path: 'internal/repl/session.go' },
    files: [{ path: 'internal/repl/session.go', kind: 'read' }],
    output: [
      { stream: 'diff_context', text: '  func (s *Session) buildRequest() {' },
      { stream: 'diff_context', text: '    promptOpts.Tools = tools' },
      { stream: 'diff_del', text: '-   promptOpts.Skills = s.skills' },
      { stream: 'diff_add', text: '+   promptOpts.Skills = s.currentSkillsCatalog()' },
      { stream: 'diff_add', text: '+   promptOpts.Agents = s.currentAgentsCatalog()' },
      { stream: 'diff_context', text: '    system, _ := prompt.Build(promptOpts)' },
    ],
  }
  const grep: ToolEvent = {
    type: 'completed', toolName: 'Grep', toolUseID: 'g1',
    input: { pattern: 'StreamDelta', path: 'internal', glob: '*.go', output_mode: 'files_with_matches' },
    files: [
      { path: 'internal/repl/session.go', kind: 'matched' },
      { path: 'internal/subagent/launcher.go', kind: 'matched' },
      { path: 'internal/engine/build.go', kind: 'matched' },
    ],
    filesTotal: 7,
  }
  const running: ToolEvent = {
    type: 'progress', toolName: 'Bash', toolUseID: 'r1',
    input: { command: 'npm run build' },
    output: [
      { stream: 'stdout', text: 'vite v6.4.3 building for production...' },
      { stream: 'stdout', text: '✓ 812 modules transformed' },
    ],
  }
  const readImg: ToolEvent = {
    type: 'completed', toolName: 'Read', toolUseID: 'r2',
    input: { path: 'docs/design.png' }, files: [{ path: 'docs/design.png', kind: 'read' }],
    output: [{ stream: 'stdout', text: '[image: design.png]' }],
    image: { mediaType: 'image/svg+xml', url: 'data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIzNjAiIGhlaWdodD0iMjAwIj48cmVjdCB3aWR0aD0iMzYwIiBoZWlnaHQ9IjIwMCIgZmlsbD0iIzBGNzY2RSIvPjxyZWN0IHg9IjI0IiB5PSIyNiIgd2lkdGg9IjExMCIgaGVpZ2h0PSIxMiIgcng9IjQiIGZpbGw9IiNGNTlFMEIiLz48dGV4dCB4PSIyNCIgeT0iMTE1IiBmb250LWZhbWlseT0ic2Fucy1zZXJpZiIgZm9udC1zaXplPSIyNCIgZmlsbD0iI2ZmZmZmZiI+ZGVzaWduLnBuZzwvdGV4dD48dGV4dCB4PSIyNCIgeT0iMTUwIiBmb250LWZhbWlseT0ic2Fucy1zZXJpZiIgZm9udC1zaXplPSIxNSIgZmlsbD0iIzk5RjZFNCI+dGh1bWJuYWlsIHByZXZpZXc8L3RleHQ+PC9zdmc+' },
  }
  return (
    <div className="min-h-screen p-8">
      <div className="max-w-[940px] mx-auto flex flex-col gap-6">
        <h2 className="text-[18px] font-bold tracking-tight">工具卡样式预览</h2>
        <div className="text-[13px] text-muted">折叠态紧凑列表(每行:图标 + 动词 + 目标 + 状态),点行展开详情;运行中的自动展开:</div>
        <ExecutionCard tools={[bash, edit, grep, readImg]} />
        <ExecutionCard tools={[running]} />
        <div className="text-[13px] text-muted">图片 Read 展开(缩略图):</div>
        <div className="bg-surface border border-line2 rounded-[14px] p-4"><ToolDetail tool={readImg} /></div>
      </div>
    </div>
  )
}

// ThinkingPreview renders assistant blocks carrying reasoning ("thinking") in each
// state, so the thinking panel's rendering can be verified without a live model
// (mounted via ?preview=thinking). Not used in normal flow.
export function ThinkingPreview() {
  const reason =
    '用户问 9.11 和 9.9 哪个大。先把它们对齐小数位:9.11 = 9.110,9.9 = 9.900。' +
    '比较小数部分 0.110 与 0.900,显然 0.900 更大。所以 9.9 > 9.11。'
  const blocks: Block[] = [
    { kind: 'assistant', id: 't1', text: '', thinking: reason, streaming: true, ts: '' },
    { kind: 'assistant', id: 't2', text: '**9.9 更大**。对齐小数位后 9.900 > 9.110。', thinking: reason, streaming: true, ts: '' },
    { kind: 'assistant', id: 't3', text: '**9.9 更大**。对齐小数位后 9.900 > 9.110。', thinking: reason, streaming: false, ts: '11:42' },
  ]
  const labels = ['① 仅思考中(自动展开)', '② 思考完 + 答案流式(自动折叠)', '③ 完成态(可点开)']
  const noop = () => {}
  return (
    <div className="min-h-screen p-8">
      <div className="w-full max-w-[1280px] mx-auto flex flex-col gap-6">
        <h2 className="text-[18px] font-bold tracking-tight">上下文用量条(不同占用)</h2>
        <div className="flex flex-col gap-3 bg-surface border border-line2 rounded-[12px] p-4">
          {[10000, 96000, 120000, 130000].map((u) => (
            <div key={u} className="flex items-center gap-4 text-[12.5px] text-muted">
              <ContextMeter used={u} budget={128000} onCompact={noop} compacting={false} busy={false} />
            </div>
          ))}
          <div className="flex items-center gap-4 text-[12.5px] text-muted">
            <ContextMeter used={44000} budget={0} onCompact={noop} compacting={false} busy={false} />
          </div>
          <div className="flex items-center gap-4 text-[12.5px] text-muted">
            <ContextMeter used={18000} budget={200000} estimated onCompact={noop} compacting={false} busy={false} />
          </div>
        </div>
        <h2 className="text-[18px] font-bold tracking-tight">结构化思考卡片</h2>
        <AnalyzeCard
          tool={{
            type: 'completed',
            toolName: 'Analyze',
            toolUseID: 'a1',
            input: {
              method: '5 Whys + 假设验证 + 奥卡姆剃刀',
              steps: [
                { key: 'symptom', label: '现象与范围', content: '登录后偶发 401,约 5% 请求,集中在移动端弱网环境。' },
                { key: 'hypotheses', label: '可能假设', content: '1) token 刷新竞态;2) 客户端时钟偏移导致过期误判;3) 网关缓存了旧 token。' },
                { key: 'validation', label: '验证方式', content: '抓包对比 401 前后的 token;核对服务端/客户端时间;灰度关闭网关缓存。' },
                { key: 'root_cause', label: '最可能根因', content: '并发刷新时旧 token 覆盖了新 token(竞态)。' },
                { key: 'fix', label: '修复方案', content: '刷新加互斥锁 + 单飞(single-flight);失败仅重试一次。' },
                { key: 'verification', label: '验证方法', content: '压测并发刷新场景;监控 401 率回落到 0 且无回归。' },
              ],
            },
          }}
        />
        <div className="text-[13px] text-muted">流式中(部分步骤已填、后续为空 + 分析中…):</div>
        <AnalyzeCard
          tool={{
            type: 'progress',
            toolName: 'Analyze',
            toolUseID: 'a2',
            input: {
              method: '5 Whys + 假设验证 + 奥卡姆剃刀',
              steps: [
                { key: 'symptom', label: '现象与范围', content: '登录后偶发 401,约 5% 请求,集中在移动端弱网环境。' },
                { key: 'hypotheses', label: '可能假设', content: '1) token 刷新竞态;2) 客户端时钟偏移导致过期误判;3) 网关缓存了旧 to' },
                { key: 'validation', label: '验证方式', content: '' },
                { key: 'root_cause', label: '最可能根因', content: '' },
                { key: 'fix', label: '修复方案', content: '' },
                { key: 'verification', label: '验证方法', content: '' },
              ],
            },
          }}
        />
        <h2 className="text-[18px] font-bold tracking-tight">思考面板样式预览</h2>
        {blocks.map((b, i) => (
          <div key={b.id} className="flex flex-col gap-2">
            <div className="text-[13px] text-muted">{labels[i]}</div>
            <BlockView block={b} />
          </div>
        ))}
      </div>
    </div>
  )
}

// ThinkingPanel shows the model's streamed reasoning ("chain of thought") in a
// collapsible panel above the answer. It auto-expands while the model is actively
// thinking (before any answer text) and auto-collapses once the answer begins; a
// manual toggle takes over from that point.
function ThinkingPanel({ text, streaming }: { text: string; streaming: boolean }) {
  const [open, setOpen] = useState(streaming)
  const userToggled = useRef(false)
  useEffect(() => {
    if (!userToggled.current) setOpen(streaming)
  }, [streaming])
  const scroll = useStickToBottom(open && streaming ? text.length : null)
  // Compact, dimmed disclosure (Claude-style): a single quiet line that opens into
  // the reasoning as indented, muted text with a left rule — not a boxed panel.
  return (
    <div>
      <button
        onClick={() => {
          userToggled.current = true
          setOpen((v) => !v)
        }}
        className="inline-flex items-center gap-1.5 text-[12.5px] text-faint hover:text-muted transition cursor-pointer select-none"
      >
        <span className={`flex-none${streaming ? ' animate-pulse' : ''}`}>
          <Icon name="sparkles" size={13} />
        </span>
        <span>{streaming ? '正在思考…' : '思考过程'}</span>
        <Icon name="chevron-down" size={12} className={`flex-none transition-transform${open ? ' rotate-180' : ''}`} />
      </button>
      {open && (
        <div
          ref={streaming ? scroll.ref : undefined}
          onScroll={streaming ? scroll.onScroll : undefined}
          className={`mt-1.5 text-[13px] leading-[1.7] text-faint whitespace-pre-wrap break-words${streaming ? ' max-h-[34vh] overflow-y-auto' : ''}`}
        >
          {text}
          {streaming && <span className="caret">▍</span>}
        </div>
      )}
    </div>
  )
}

// AnalyzeCard visualizes the model's structured-thinking protocol (the Analyze
// tool): the method as a badge and each protocol step as a numbered timeline entry
// with its label and the model's task-specific content. Collapsible; shows a live
// "分析中…" hint until the analysis completes.
function AnalyzeCard({ tool }: { tool: ToolEvent }) {
  const { method, steps } = analyzeSteps(tool.input)
  const running = tool.type === 'started' || tool.type === 'progress'
  const [open, setOpen] = useState(true)
  return (
    <div className="anim-rise">
      <div className="flex-1 min-w-0 bg-surface border border-line2 rounded-[14px] shadow-xs overflow-hidden">
        <button
          onClick={() => setOpen((v) => !v)}
          className="w-full flex items-center gap-2 px-4 py-2.5 text-left hover:bg-surface2 transition cursor-pointer select-none"
        >
          <span className={`text-primary flex-none${running ? ' animate-pulse' : ''}`}><Icon name="sparkles" size={16} /></span>
          <span className="font-semibold text-[13.5px] text-ink flex-none">结构化思考</span>
          {method && (
            <span className="text-[11.5px] text-primaryink bg-primarysoft rounded-full px-2 py-0.5 truncate">{method}</span>
          )}
          {running && <span className="text-[12px] text-faint flex-none">分析中…</span>}
          <span className={`ml-auto flex-none text-faint transition-transform${open ? ' rotate-180' : ''}`}><Icon name="chevron-down" size={14} /></span>
        </button>
        {open &&
          (steps.length > 0 ? (
            <ol className="px-4 pb-3.5 pt-0.5 m-0 list-none">
              {steps.map((s, i) => (
                <li key={s.key || i} className="relative pl-8 pb-3.5 last:pb-0">
                  {i < steps.length - 1 && <span className="absolute left-[11px] top-[23px] bottom-0 w-px bg-line2" />}
                  <span className="absolute left-0 top-0 w-[23px] h-[23px] rounded-full bg-primarysoft text-primaryink text-[11.5px] font-bold inline-flex items-center justify-center">{i + 1}</span>
                  <div className="text-[12.5px] font-semibold text-ink pt-[3px]">{s.label || s.key}</div>
                  <div className="text-[13px] text-muted leading-[1.65] whitespace-pre-wrap break-words mt-1">{s.content || '—'}</div>
                </li>
              ))}
            </ol>
          ) : (
            <div className="px-4 pb-3 pt-1 text-[12.5px] text-faint">（无分析内容）</div>
          ))}
      </div>
    </div>
  )
}

// ContextMeter shows how full the model's context window is — the last turn's
// input tokens against the compaction budget — with a bar that turns amber as it
// nears the 80% auto-compaction threshold, plus a manual "压缩" button. With no
// budget set (auto-compaction off) it just shows the raw occupancy.
function ContextMeter({
  used,
  budget,
  estimated,
  onCompact,
  compacting,
  busy,
}: {
  used: number
  budget: number
  estimated?: boolean
  onCompact: () => void
  compacting: boolean
  busy: boolean
}) {
  const k = (n: number) => (n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1) + 'k' : String(n))
  const pct = budget > 0 ? Math.min(100, Math.round((used / budget) * 100)) : 0
  const near = budget > 0 && pct >= 80
  const bar = pct >= 100 ? 'bg-red' : near ? 'bg-[#e0954a]' : 'bg-primary'
  // A leading "≈" marks a resume-time estimate, until the first turn reports the
  // provider's exact count.
  const approx = estimated ? '≈' : ''
  return (
    <div className="inline-flex items-center gap-2" style={NO_DRAG}>
      <span
        className="inline-flex items-center gap-1.5"
        title={
          (estimated ? '（估算值，发送一条消息后即为精确值）\n' : '') +
          (budget > 0
            ? `上下文占用 ${used.toLocaleString()} / ${budget.toLocaleString()} tokens · 达 80% 自动总结压缩`
            : `上下文占用 ${used.toLocaleString()} tokens · 未设预算，自动压缩关闭`)
        }
      >
        <span>上下文</span>
        {budget > 0 ? (
          <>
            <span className="w-[62px] h-[6px] rounded-full bg-surface2 border border-line2 overflow-hidden inline-block align-middle">
              <span className={`block h-full ${bar} transition-[width]`} style={{ width: pct + '%' }} />
            </span>
            <b className={`font-semibold tabular-nums ${near ? 'text-[#b26a1f]' : 'text-ink'}`}>{approx}{pct}%</b>
            <span className="text-faint tabular-nums">{approx}{k(used)}/{k(budget)}</span>
          </>
        ) : (
          <span className="text-ink font-semibold tabular-nums">{approx}{k(used)} <span className="text-faint font-normal">· 未限</span></span>
        )}
      </span>
      <button
        onClick={onCompact}
        disabled={busy || compacting}
        title="压缩上下文：把较早的对话总结成摘要、保留最近几轮原文（磁盘记录保持完整）"
        className="inline-flex items-center gap-1 px-2 py-1 rounded-lg border border-line2 text-[12px] text-muted hover:text-ink hover:bg-surface2 transition cursor-pointer disabled:opacity-40 disabled:cursor-default"
      >
        <Icon name="compress" size={13} /> {compacting ? '压缩中…' : '压缩'}
      </button>
    </div>
  )
}

// fmtTokens renders a token count compactly: 340 → "340", 1234 → "1.2k", 23000 → "23k".
function fmtTokens(n: number): string {
  if (n >= 10000) return Math.round(n / 1000) + 'k'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k'
  return String(n)
}

// fmtDuration renders elapsed ms compactly: 850 → "0.9s", 3200 → "3.2s", 75000 → "1m15s".
function fmtDuration(ms?: number): string {
  if (!ms || ms < 0) return ''
  const s = ms / 1000
  if (s < 60) return (s < 10 ? s.toFixed(1) : Math.round(s).toString()) + 's'
  const m = Math.floor(s / 60)
  return `${m}m${Math.round(s % 60)}s`
}

function BlockView({ block, onOpenFile, resolveFile }: { block: Block; onOpenFile?: (relPath: string) => void; resolveFile?: (token: string) => string | null }) {
  // While an assistant answer streams, keep it in a fixed-height window pinned to
  // the newest text (consistent with the tool/agent cards); release to full height
  // when done so the finished answer reads normally in the page flow.
  const aScroll = useStickToBottom(block.kind === 'assistant' && block.streaming ? block.text.length : null)
  switch (block.kind) {
    case 'user':
      return (
        <div className="flex justify-end anim-rise">
          <div className="min-w-0 max-w-[82%] rounded-[13px] px-3.5 py-2 text-[13.5px] text-ink leading-[1.55]" style={{ background: '#F4F3FF' }}>
            {block.attachments && block.attachments.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mb-1.5">
                {block.attachments.map((name, i) => (
                  <span key={name + i} className="inline-flex items-center gap-1 bg-surface border border-line2 rounded-[7px] px-2 py-0.5 text-[11.5px] text-muted max-w-[220px]">
                    <Icon name="file" size={12} /> <span className="truncate">{name}</span>
                  </span>
                ))}
              </div>
            )}
            {block.text && <div className="whitespace-pre-wrap break-words">{block.text}</div>}
          </div>
        </div>
      )
    case 'assistant': {
      const hasThinking = (block.thinking ?? '').trim() !== ''
      const hasText = block.text.trim() !== ''
      // The model is still thinking and hasn't begun the answer — the thinking panel
      // carries the live caret, so the (empty) answer area is suppressed until text
      // starts, avoiding a stray second caret.
      const thinkingActive = block.streaming && hasThinking && !hasText
      // A turn may produce an assistant message with no text (tool-only) or just
      // whitespace; don't render an empty bubble — unless there's reasoning to show.
      if (!block.streaming && !hasText && !hasThinking) return null
      return (
        <BotRow>
          <div className="flex flex-col gap-2">
            {hasThinking && <ThinkingPanel text={block.thinking ?? ''} streaming={thinkingActive} />}
            {!thinkingActive && (hasText || block.streaming) && (
              <div
                ref={block.streaming ? aScroll.ref : undefined}
                onScroll={block.streaming ? aScroll.onScroll : undefined}
                className={`text-[15px] text-[#3f4653] leading-[1.75] break-words${block.streaming ? ' max-h-[58vh] overflow-y-auto pr-1' : ''}`}
              >
                <Markdown onOpenFile={onOpenFile} resolveFile={resolveFile}>{block.text}</Markdown>
                {block.streaming && <span className="caret">▍</span>}
              </div>
            )}
          </div>
        </BotRow>
      )
    }
    case 'error':
      return (
        <BotRow>
          <div className="max-w-full px-3.5 py-2.5 rounded-[10px] text-[13.5px] bg-redbg text-red">{block.text}</div>
        </BotRow>
      )
    case 'warning':
      return (
        <BotRow>
          <div className="max-w-full px-3.5 py-2.5 rounded-[10px] text-[13.5px] bg-[#fff7e8] text-[#9a6b12]">{block.text}</div>
        </BotRow>
      )
    case 'notice':
      return (
        <div className="flex justify-center anim-rise">
          <div className="px-3 py-1.5 rounded-full text-[12.5px] bg-surface2 border border-line2 text-muted">
            {block.text}
          </div>
        </div>
      )
    case 'usage':
      return (
        <BotRow>
          <div className="flex justify-end -mt-1.5">
            <span
              className="text-[11px] text-faint font-mono tabular-nums select-none"
              title="本轮用量(仅本会话的模型调用;子代理另计)"
            >
              ↑{fmtTokens(block.inTok)} ↓{fmtTokens(block.outTok)}{block.durMs ? ` · ${fmtDuration(block.durMs)}` : ''}
            </span>
          </div>
        </BotRow>
      )
    case 'tool':
      // Live Task with sub-agent activity → nested observable view; a resumed Task
      // (no live nested data) falls back to the normal card showing its result.
      if (block.tool.toolName === 'Task' && block.nested) return <BotRow><AgentTaskCard tool={block.tool} nested={block.nested} /></BotRow>
      return <BotRow><ExecutionCard tools={[block.tool]} /></BotRow>
    case 'planchoice':
      return null // rendered as PlanChoiceCard in the group loop
  }
}

function PermissionModal({ req, onDecide, remaining = 0, onDenyRest }: { req: PermissionRequest; onDecide: (decision: string) => void; remaining?: number; onDenyRest?: () => void }) {
  const s = req.summary
  const td = 'py-[7px] px-1.5 align-top border-t border-line'
  return (
    <div className="fixed inset-0 bg-[rgba(30,33,50,0.32)] backdrop-blur-[2px] flex items-center justify-center z-20 anim-rise">
      <div className="w-[560px] max-w-[92vw] bg-surface rounded-[16px] p-[22px] shadow-[0_30px_70px_rgba(30,35,60,0.28)]">
        <h3 className="m-0 mb-4 text-[16px] font-bold flex items-center gap-2.5">
          <span className="w-[9px] h-[9px] rounded-[3px] bg-primary" />权限请求
          {remaining > 0 && (
            <span className="ml-auto text-[12px] font-medium text-muted bg-surface2 border border-line2 rounded-full px-2.5 py-0.5">还有 {remaining} 个待处理</span>
          )}
        </h3>
        {req.samplingServer ? (
          <div className="mb-3.5">
            <div className="flex items-start gap-2 bg-primarysoft border border-line2 rounded-lg px-3 py-2.5">
              <span className="text-primaryink flex-none mt-px"><Icon name="bot" size={16} /></span>
              <div className="min-w-0 text-[13px] text-ink">
                MCP 服务器 <b className="font-mono">{req.samplingServer}</b> 请求使用你的模型生成一段内容（sampling）。
                <div className="text-[12px] text-muted mt-1">允许即用你配置的模型和额度替它完成一次生成。选「本次会话」后本会话内不再询问；仅在信任该服务器时允许。</div>
              </div>
            </div>
          </div>
        ) : (
          <>
            {req.harmReason && (
              <div className="mb-3 flex items-start gap-2 bg-redbg border border-[rgba(224,86,74,0.35)] rounded-lg px-3 py-2.5">
                <span className="text-red flex-none mt-px"><Icon name="shield" size={16} /></span>
                <div className="min-w-0">
                  <div className="text-[12.5px] font-semibold text-red">模型判定可能有害</div>
                  <div className="text-[12.5px] text-ink mt-0.5 break-words">{req.harmReason}</div>
                </div>
              </div>
            )}
            {req.command && (
              <div className="mb-3">
                <div className="text-[12px] text-muted mb-1.5">将执行命令</div>
                <pre className="m-0 bg-surface2 border border-line rounded-lg px-3 py-2.5 font-mono text-[12.5px] text-ink whitespace-pre-wrap break-all max-h-[160px] overflow-auto">{req.command}</pre>
              </div>
            )}
            <table className="w-full border-collapse text-[13px]">
              <tbody>
                <tr><td className={`${td} text-muted w-16`}>工具</td><td className={td}>{s.ToolName}</td></tr>
                <tr><td className={`${td} text-muted`}>操作</td><td className={td}>{s.Operation} · 风险 {s.Risk}{s.CommandCategory ? ` · ${s.CommandCategory}` : ''}</td></tr>
                {s.NetworkHost && <tr><td className={`${td} text-muted`}>主机</td><td className={td}>{s.NetworkHost}</td></tr>}
                {s.MCPServer && <tr><td className={`${td} text-muted`}>MCP</td><td className={td}>{s.MCPServer}/{s.MCPTool}</td></tr>}
                {req.targets && req.targets.length > 0 && (
                  <tr><td className={`${td} text-muted`}>目标</td><td className={td}>{req.targets.map((t) => <code key={t} className="bg-inset px-1.5 py-0.5 rounded mr-1.5 mb-1.5 inline-block">{t}</code>)}</td></tr>
                )}
              </tbody>
            </table>
            <div className="mt-3.5 text-[12px] text-faint">
              「本次会话」后，对本项目文件的增删改、或同类命令在本次会话内都不再询问（推荐）；「仅此一次」每次都会再问。
            </div>
          </>
        )}
        <div className="flex gap-2.5 mt-2.5">
          <button className={`${BTN} flex-1 ${BTN_PRIMARY}`} onClick={() => onDecide('allow-session')}>本次会话</button>
          <button className={`${BTN} flex-1`} onClick={() => onDecide('allow-once')}>仅此一次</button>
          <button className={`${BTN} flex-1`} onClick={() => onDecide('allow-project')}>本项目</button>
          <button className={`${BTN} flex-1 ${BTN_DANGER}`} onClick={() => onDecide('deny')}>拒绝</button>
        </div>
        {remaining > 0 && onDenyRest && (
          <button className="mt-2 w-full text-[12.5px] text-muted hover:text-red transition-colors" onClick={onDenyRest}>拒绝全部（含其余 {remaining} 个）</button>
        )}
      </div>
    </div>
  )
}

function shorten(p?: string): string {
  if (!p) return ''
  const parts = p.replace(/\\/g, '/').split('/')
  return parts.length <= 2 ? p : '…/' + parts.slice(-2).join('/')
}
function dirname(p: string): string {
  const s = p.replace(/\\/g, '/')
  const i = s.lastIndexOf('/')
  return i > 0 ? s.slice(0, i) : ''
}
function ext(p: string): string {
  const e = basename(p).split('.').pop() || ''
  return e.length <= 4 ? e.toUpperCase() : 'FILE'
}
function fmtTime(s: number): string {
  const m = Math.floor(s / 60)
  const ss = s % 60
  return `${String(m).padStart(2, '0')}:${String(ss).padStart(2, '0')}`
}
