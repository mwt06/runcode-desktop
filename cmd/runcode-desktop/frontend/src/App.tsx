import { useEffect, useMemo, useRef, useState, type PointerEvent } from 'react'
import { Icon, Logo } from '@/ui/icons'
import {
  Events,
  onEvent,
  startSession,
  sendMessage,
  sendMessageWithImages,
  interrupt,
  resolvePermission,
  setPermissionMode,
  switchModel,
  setPlanMode,
  setReasoningScenario,
  setThinkingEffort,
  compact,
  listSessions,
  resumeSession,
  deleteSession,
  newSession,
  pickWorkspaceFolder,
  switchWorkspace,
  loadConfig,
  listFiles,
  listEdits,
  revertEdit,
  type SessionInfo,
  type SessionSummary,
  type StartSessionRequest,
  type PermissionRequest,
  type ResumedTool,
  type ToolEvent,
  type PlanSnapshot,
  errText,
} from '@/core/bridge'
import {
  finalizeStreaming,
  finalizeTools,
  mergeTool,
  groupBlocks,
  parsePlan,
  resumedMatchedFiles,
  type Block,
  type AgentNested,
} from '@/chat/blocks'
import { BTN, BTN_PRIMARY, BTN_DANGER, DRAG, NO_DRAG } from '@/ui/tokens'
import { diffStats, hasDiff, toolTargetPath } from '@/chat/tool-text'
import { AgentTaskGroup } from '@/chat/agent-task'
import { AnalyzeCard } from '@/chat/analyze-card'
import { AskCard, PlanChoiceCard } from '@/chat/ask-card'
import { BlockView } from '@/chat/block-view'
import { BotRow } from '@/chat/bot-row'
import { ContextMeter } from '@/chat/context-meter'
import { ExecutionCard } from '@/chat/execution-card'
import { PlanPill } from '@/chat/plan-pill'
import { ReplyArtifacts } from '@/chat/reply-artifacts'
import { type ModelOption } from '@/ui/model-picker'
import { PluginsPage } from '@/pages/plugins'
import { PermissionsPage } from '@/pages/permissions'
import { MemoryPage } from '@/pages/memory'
import { StartForm } from '@/pages/start'
import { SettingsPage } from '@/pages/settings'
import { Sidebar } from './sidebar'
import { Composer } from '@/composer'
import { PreviewPane } from '@/preview/pane'
import { FileBrowser } from '@/preview/file-browser'
import { EditedCards } from '@/chat/edited-card'
import { isPreviewable, toWorkspaceRel, clampPreviewWidth, lastPreviewablePath } from '@/preview/classify'
import { openTab, closeTab, type PreviewTab } from '@/preview/tabs'
import { basename, shortenPath } from '@/core/paths'

let seq = 0
const nextID = () => `b${++seq}`
const now = () =>
  new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })


export default function App() {
  const [started, setStarted] = useState(false)
  const [view, setView] = useState<'chat' | 'settings' | 'plugins' | 'permissions' | 'memory'>('chat')
  const [info, setInfo] = useState<SessionInfo | null>(null)
  const pickModel = async (choice: ModelOption) => {
    try {
      // Adopt the full returned status: switching to/from a custom model rebuilds the
      // session, which rebinds the preview server to a new port — so previewBaseURL
      // (and sessionId) must be refreshed, not just the model, or the preview iframe
      // keeps loading the dead port and shows “拒绝连接”.
      const st = await switchModel(choice.kind, choice.id)
      setInfo((prev) => (st ? { ...prev, ...st } : prev))
    } catch { /* 切换失败保持原样 */ }
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
  // toast is a transient, self-dismissing hint shown above the composer (e.g.
  // "nothing to compact"), kept out of the persistent conversation flow.
  const [toast, setToast] = useState('')
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  // 侧栏折叠态：上提到这里，让折叠开关能放到主栏顶部状态条（「空闲」前），
  // 而侧栏本身按此 prop 变宽窄。持久化到 localStorage。
  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(() => localStorage.getItem('sidebar.collapsed') === '1')
  const toggleSidebar = () =>
    setSidebarCollapsed((v) => {
      localStorage.setItem('sidebar.collapsed', v ? '0' : '1')
      return !v
    })
  const [elapsed, setElapsed] = useState(0)
  const [starting, setStarting] = useState(false)
  const [startError, setStartError] = useState('')
  const [recents, setRecents] = useState<SessionSummary[]>([])
  const [initialReq, setInitialReq] = useState<Partial<StartSessionRequest> | null>(null)
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
  const scrollRef = useRef<HTMLDivElement>(null)
  const taRef = useRef<HTMLTextAreaElement>(null)

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
      setBlocks((prev) => [...prev, { kind: 'warning', id: nextID(), text: '撤销失败：' + errText(e) }])
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

  useEffect(() => () => { if (toastTimer.current) clearTimeout(toastTimer.current) }, [])

  // Workspace file list for the composer picker (#), the file browser, and reply
  // artifact matching — refreshed per session (skills/agents refresh moved into
  // Composer).
  useEffect(() => {
    if (!started) return
    listFiles()
      .then((f) => setFiles(f ?? []))
      .catch(() => setFiles([]))
  }, [started, info?.sessionId])


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
      onEvent(Events.AssistantDelta, ({ text }) => {
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
      onEvent(Events.AssistantThinking, ({ text }) => {
        setBlocks((prev) => {
          const last = prev[prev.length - 1]
          if (last && last.kind === 'assistant' && last.streaming) {
            return [...prev.slice(0, -1), { ...last, thinking: (last.thinking ?? '') + text }]
          }
          return [...prev, { kind: 'assistant', id: nextID(), text: '', thinking: text, streaming: true, ts: now() }]
        })
      }),
      // A transient LLM-request failure is being retried. Drop any stale partial
      // output (the retry re-streams from scratch) and mark the break with a
      // divider carrying the reason and attempt number.
      onEvent(Events.Retry, ({ reason, attempt, maxAttempts }) => {
        setBlocks((prev) => {
          const divider = { kind: 'retry' as const, id: nextID(), reason, attempt, maxAttempts }
          const last = prev[prev.length - 1]
          if (last && last.kind === 'assistant' && last.streaming) {
            return [...prev.slice(0, -1), divider]
          }
          return [...prev, divider]
        })
      }),
      onEvent(Events.ToolEvent, (ev) => {
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
      onEvent(Events.TurnEnd, (end) => {
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
      onEvent(Events.TurnError, ({ error }) => {
        setBusy(false)
        setPermQueue([])
        // 用户中断/取消回合会以 "context canceled" 抵达——那是停止，不是失败，
        // 别渲染成红色报错块（否则点停止看起来像“出错了”而非“已停止”）。
        const cancelled = /cancel(?:l)?ed/i.test(error)
        setBlocks((prev) => {
          const base = finalizeTools(finalizeStreaming(prev))
          return cancelled ? base : [...base, { kind: 'error', id: nextID(), text: error }]
        })
      }),
      // Enqueue (don't replace): concurrent tools may each prompt. Dedup by id in
      // case an event is delivered twice.
      onEvent(Events.PermissionRequest, (req) =>
        setPermQueue((q) => (q.some((p) => p.id === req.id) ? q : [...q, req])),
      ),
      onEvent(Events.Warning, ({ message }) =>
        setBlocks((prev) => [...prev, { kind: 'warning', id: nextID(), text: message }]),
      ),
      // Judge ("smart") mode auto-allowed a risky action without a prompt, or tripped
      // its per-session breaker. An auto-allow is marked on the very tool card it
      // decided (by tool-use id), with the judge's reason shown when the row expands;
      // a breaker trip is a session-level notice.
      onEvent(
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
      onEvent(Events.SessionRenamed, () => {
        void refreshRecents()
      }),
    ]
    return () => offs.forEach((off) => off && off())
  }, [])

  // Pin the conversation to the bottom while output streams, but only if the user
  // is already at (or near) the bottom — once they scroll up to read, streaming
  // updates (especially sub-agents' frequent nested events) must not yank the view
  // back down. Sending a new message re-pins.
  const chatStick = useRef(true)
  const onChatScroll = () => {
    const el = scrollRef.current
    if (el) chatStick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48
  }
  useEffect(() => {
    const el = scrollRef.current
    if (el && chatStick.current) el.scrollTo({ top: el.scrollHeight })
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
      setInitialReq(req)
      setStarted(true)
    } catch (e) {
      setStartError(errText(e))
    } finally {
      setStarting(false)
    }
  }
  async function send(text: string, attach: string[] = []) {
    if ((!text && attach.length === 0) || busy) return
    const names = attach.map((p) => basename(p))
    chatStick.current = true
    setBlocks((prev) => [...prev, { kind: 'user', id: nextID(), text, ts: now(), attachments: names.length ? names : undefined }])
    setBusy(true)
    try {
      if (attach.length) await sendMessageWithImages(text, attach)
      else await sendMessage(text)
    } catch (e) {
      setBusy(false)
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: errText(e) }])
    }
  }
  async function togglePlan() {
    if (!info) return
    try {
      const i = await setPlanMode(!info.planMode)
      if (i?.model) setInfo(i)
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: errText(e) }])
    }
  }
  async function chooseReasoning(scenario: string) {
    if (!info) return
    try {
      const i = await setReasoningScenario(scenario)
      if (i?.model) setInfo(i)
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: errText(e) }])
    }
  }
  async function chooseThinking(effort: string) {
    if (!info) return
    try {
      const i = await setThinkingEffort(effort)
      if (i?.model) setInfo(i)
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: errText(e) }])
    }
  }
  // showToast flashes a transient hint above the composer; a fresh call resets
  // the dismiss timer so rapid repeats don't stack or blink.
  function showToast(text: string) {
    setToast(text)
    if (toastTimer.current) clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToast(''), 2600)
  }
  // doCompact forces a summary-compaction of the in-memory history now. The on-disk
  // log stays complete; this only shrinks what is resent to the model next turn.
  async function doCompact() {
    if (busy || compacting || !info) return
    setCompacting(true)
    try {
      const r = await compact()
      if (r.after < r.before) {
        // Compaction happened: mark the fold point with a divider in the flow
        // (carrying the summary call's own token spend and the new context size), and
        // drop the context meter now to the estimated post-compaction size (the next
        // real turn replaces it with a provider-measured exact count).
        setBlocks((prev) => [...prev, { kind: 'compaction', id: nextID(), before: r.before, after: r.after, inTok: r.inputTokens, outTok: r.outputTokens, contextTokens: r.contextTokens }])
        setCtxTokens(r.contextTokens)
        setCtxEstimated(true)
      } else {
        // Nothing to compact: a transient hint above the composer, not a row that
        // piles up in the conversation on repeated presses of a short chat.
        showToast('暂无可压缩内容（最近对话已很精简）')
      }
    } catch (e) {
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: errText(e) }])
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
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: errText(e) }])
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
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: errText(e) }])
    }
  }
  // clearPlan resets the progress pill when the conversation context is replaced
  // (new/resumed/switched session).
  function clearPlan() {
    setPlan(null)
    setPlanOpen(false)
  }
  // 会话/工作区切换的并发防护：新建/恢复/切工作区都是"发请求→拿结果→整体替换
  // 会话状态"，用户快速连点两个目标时，先发但后落地的旧响应不得覆盖新会话
  // （否则界面静默回跳，且 openRecent 第二段 await 的编辑元数据会合并进错误的
  // 会话）。每次发起切换代际 +1，每个 await 之后核对代际，过期响应整体丢弃；
  // switching 供入口回调挡住切换进行中的重复点击。
  const switchGen = useRef(0)
  const [switching, setSwitching] = useState(false)
  function beginSwitch() {
    const gen = ++switchGen.current
    setSwitching(true)
    return () => gen !== switchGen.current
  }
  function endSwitch(stale: () => boolean) {
    if (!stale()) setSwitching(false)
  }
  async function newChat() {
    const stale = beginSwitch()
    try {
      const i = await newSession()
      if (stale()) return
      setInfo(i)
      setBlocks([])
      clearPlan()
      setTokens({ in: 0, out: 0 })
      setCtxTokens(0)
      setCtxEstimated(false)
      setElapsed(0)
      refreshRecents()
    } catch (e) {
      if (stale()) return
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: errText(e) }])
    } finally {
      endSwitch(stale)
    }
  }
  // Pick a different folder and open a fresh session there (a new workspace).
  async function switchToWorkspace(dir: string) {
    const stale = beginSwitch()
    try {
      const i = await switchWorkspace(dir)
      if (stale()) return
      setInfo(i)
      setBlocks([])
      clearPlan()
      setTokens({ in: 0, out: 0 })
      setCtxTokens(0)
      setCtxEstimated(false)
      setElapsed(0)
      setView('chat')
      refreshRecents()
      loadConfig().then((c) => { if (c && !stale()) setInitialReq(c) }).catch(() => {}) // refresh recent-workspace MRU
    } catch (e) {
      if (stale()) return
      setView('chat')
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: errText(e) }])
    } finally {
      endSwitch(stale)
    }
  }
  async function switchWorkspaceFolder() {
    try {
      const dir = await pickWorkspaceFolder()
      if (dir) await switchToWorkspace(dir)
    } catch { /* cancelled */ }
  }
  async function deleteRecent(id: string) {
    try {
      await deleteSession(id)
      refreshRecents()
      showToast('会话已删除')
    } catch (e) {
      showToast(`删除失败：${errText(e)}`)
    }
  }

  async function openRecent(id: string) {
    if (id === info?.sessionId) return
    const stale = beginSwitch()
    try {
      const r = await resumeSession(id)
      if (stale()) return
      setInfo(r.info)
      setBlocks(
        (r.blocks ?? []).map((b): Block => {
          if (b.kind === 'user') return { kind: 'user', id: nextID(), text: b.text ?? '', ts: '' }
          if (b.kind === 'assistant') return { kind: 'assistant', id: nextID(), text: b.text ?? '', streaming: false, ts: '' }
          // Rebuild a tool execution card from the persisted result. Live-only
          // details (colored diffs, file chips) aren't stored, so the card shows the
          // tool, its target, and the result text. (Partial: a malformed block may
          // arrive without its tool payload — treat every field as optional.)
          const t: Partial<ResumedTool> = b.tool ?? {}
          const out = (t.output ?? '').trim()
          const lines = out ? out.split('\n').map((text) => ({ stream: t.isError ? 'stderr' : 'stdout', text })) : []
          // 查找/搜索(文件列表模式)的结果文本重建成 matched 文件引用——实时的
          // 文件事件不落盘,重建后恢复的卡片与实时共用同一个可折叠结构树渲染。
          const matched = t.isError ? null : resumedMatchedFiles(t.toolName, t.input, out)
          return {
            kind: 'tool',
            id: nextID(),
            tool: {
              type: t.isError ? 'failed' : 'completed',
              toolName: t.toolName,
              toolUseID: t.toolUseId,
              input: t.input,
              message: t.isError ? 'completed with error' : 'completed',
              files: matched ?? (t.path ? [{ path: t.path }] : undefined),
              output: matched ? [] : lines,
            },
          }
        }),
      )
      // Re-attach edit metadata to resumed tool steps by tool-use id, so the "已编辑"
      // cards + undo/review re-render (the resume payload itself carries no diffs).
      const edits = (await listEdits()) ?? []
      if (stale()) return
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
      if (stale()) return
      setBlocks((prev) => [...prev, { kind: 'error', id: nextID(), text: errText(e) }])
    } finally {
      endSwitch(stale)
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
          collapsed={sidebarCollapsed}
          recents={recents}
          currentId={info?.sessionId}
          cwd={info?.cwd}
          recentWorkspaces={initialReq?.recentWorkspaces ?? []}
          onPickWorkspace={(dir) => { if (!switching) void switchToWorkspace(dir) }}
          onSwitchWorkspace={() => { if (!switching) void switchWorkspaceFolder() }}
          onDelete={deleteRecent}
          view={view}
          onNav={setView}
          onNew={() => {
            if (switching) return
            setView('chat')
            void newChat()
          }}
          onResume={(id) => {
            if (switching) return
            setView('chat')
            void openRecent(id)
          }}
        />

        <main className="flex-1 flex flex-col min-w-0 min-h-0 bg-surface">
        {/* Secondary bar below the full-width TitleBar: status + context meter + preview toggle.
            Also a drag region, since it spans the top of the main pane. */}
        <header className="h-[52px] flex-none flex items-center justify-between pl-2 pr-1.5 bg-surface border-b border-line2 select-none" style={DRAG}>
          <div className="flex items-center gap-2">
            <button
              onClick={toggleSidebar}
              title={sidebarCollapsed ? '展开侧栏' : '折叠侧栏'}
              style={NO_DRAG}
              className="flex-none flex items-center justify-center w-8 h-8 rounded-[9px] text-muted hover:text-ink hover:bg-surface2 transition"
            >
              <Icon name="panel-left" size={17} />
            </button>
            <span className={`inline-flex items-center gap-1.5 text-[12.5px] ${busy ? 'text-green' : 'text-muted'}`}>
              <span className={`w-[7px] h-[7px] rounded-full ${busy ? 'bg-green blip shadow-[0_0_0_3px_rgba(31,157,99,0.16)]' : 'bg-faint'}`} />
              {busy ? '运行中' : '空闲'}
            </span>
          </div>
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
          <SettingsPage initial={initialReq ?? {}} info={info} onSaved={(i) => {
            setInfo(i)
            loadConfig().then((c) => setInitialReq(c ?? {})).catch(() => {})
          }} />
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
        <div className="flex-1 overflow-y-auto bg-surface px-6 pt-3 pb-8" ref={scrollRef} onScroll={onChatScroll}>
          <div className="mx-auto max-w-[1200px] flex flex-col gap-6">
            {blocks.length === 0 && (
              <div className="mt-[16vh] text-center text-faint">
                <span className="inline-flex items-center justify-center w-[52px] h-[52px] rounded-[15px] mb-3.5 bg-surface border border-line2 shadow-xs"><Logo size={34} /></span>
                <p>让 XRUN 在 <code className="font-mono bg-surface border border-line2 px-2 py-0.5 rounded-md text-muted">{shortenPath(info?.cwd)}</code> 中探索、修改或运行点什么。</p>
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
              ) : g.kind === 'taskgroup' ? (
                <BotRow key={g.id}><AgentTaskGroup tasks={g.tasks} /></BotRow>
              ) : g.block.kind === 'planchoice' ? (
                <BotRow key={g.block.id}><PlanChoiceCard busy={busy} onExecute={executePlanAs} onDismiss={dismissPlanChoice} /></BotRow>
              ) : (
                <div key={g.block.id}>
                  <BlockView block={g.block} onOpenFile={openArtifact} resolveFile={resolveWsFile} />
                  {g.block.kind === 'assistant' && (
                    <ReplyArtifacts text={g.block.text} files={files} tabs={tabs} onOpen={openArtifact} />
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

        <Composer
          input={input}
          onInputChange={setInput}
          taRef={taRef}
          busy={busy}
          toast={toast}
          info={info}
          files={files}
          sessionId={info?.sessionId}
          onRefreshFiles={() => { listFiles().then((f) => setFiles(f ?? [])).catch(() => {}) }}
          onSend={(text, attach) => void send(text, attach)}
          onStop={() => {
            // 乐观停止：立即取消引擎回合，并即刻收尾 UI（清 busy、结束流式渲染）。
            // 不等 turn:end 事件——事件延迟或漏收都不会让停止“看起来没反应”；真在跑的
            // 回合由 interrupt() 取消，其后续事件到达时 busy 已为 false，幂等无副作用。
            void interrupt()
            setBusy(false)
            setBlocks((prev) => finalizeTools(finalizeStreaming(prev)))
          }}
          onToggleMode={() => void toggleMode()}
          onTogglePlan={() => void togglePlan()}
          onChooseReasoning={(s) => void chooseReasoning(s)}
          onChooseThinking={(t) => void chooseThinking(t)}
          onPickModel={pickModel}
        />
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
                <tr><td className={`${td} text-muted w-16`}>工具</td><td className={td}>{s.toolName}</td></tr>
                <tr><td className={`${td} text-muted`}>操作</td><td className={td}>{s.operation} · 风险 {s.risk}{s.commandCategory ? ` · ${s.commandCategory}` : ''}</td></tr>
                {s.networkHost && <tr><td className={`${td} text-muted`}>主机</td><td className={td}>{s.networkHost}</td></tr>}
                {s.mcpServer && <tr><td className={`${td} text-muted`}>MCP</td><td className={td}>{s.mcpServer}/{s.mcpTool}</td></tr>}
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
