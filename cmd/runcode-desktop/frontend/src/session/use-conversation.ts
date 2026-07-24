// useConversation 拥有"对话本身"的全部状态：消息块、引擎事件订阅、发送/停止/
// 压缩、上下文占用与计划快照。它不知道会话是怎么创建或切换的——那是 use-session
// 的事；两者的接缝只有 infoRef(读当前会话的 planMode)与几个回调。
import { useEffect, useRef, useState, type RefObject } from 'react'
import {
  compact as compactSession,
  Events,
  errText,
  interrupt,
  listEdits,
  onEvent,
  revertEdit,
  sendMessage,
  sendMessageWithImages,
  type PermissionRequest,
  type PlanSnapshot,
  type ResumedSession,
  type ResumedTool,
  type SessionInfo,
} from '@/core/bridge'
import {
  finalizeStreaming,
  finalizeTools,
  mergeTool,
  parsePlan,
  resumedMatchedFiles,
  type AgentNested,
  type Block,
} from '@/chat/blocks'
import { basename } from '@/core/paths'

let seq = 0
const nextID = () => `b${++seq}`
const now = () =>
  new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })

export type Conversation = ReturnType<typeof useConversation>

export function useConversation({ infoRef, permissions, onFilesChanged, onOpenPreview, showToast }: {
  infoRef: RefObject<SessionInfo | null>
  permissions: { pending: PermissionRequest | null; enqueue: (req: PermissionRequest) => void; clear: () => void }
  onFilesChanged: () => void
  onOpenPreview: (absPath: string) => void
  showToast: (text: string) => void
}) {
  const [blocks, setBlocks] = useState<Block[]>([])
  // harmAllows maps a tool-use id to the harm judge's reason when smart mode
  // auto-allowed that call, so its tool card can be marked and explain why.
  const [harmAllows, setHarmAllows] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  // plan is the latest TodoWrite snapshot (null until the model records one), shown
  // as the top-center progress pill. planOpen toggles the pill's dropdown (the full
  // task timeline); the pill itself stays visible whenever a plan exists.
  const [plan, setPlan] = useState<PlanSnapshot | null>(null)
  const [planOpen, setPlanOpen] = useState(false)
  // ctxTokens is the current context-window occupancy (last turn's final input
  // tokens), shown against the compaction budget so the user sees how full it is.
  // ctxEstimated marks it as a resume-time estimate until the first turn reports
  // the provider's exact count.
  const [ctxTokens, setCtxTokens] = useState(0)
  const [ctxEstimated, setCtxEstimated] = useState(false)
  const [compacting, setCompacting] = useState(false)
  // revertedEdits tracks which edited-file snapshots the user has undone, by
  // snapshotId, so their "已编辑" card renders grey with 已撤销 instead of the
  // 撤销/审核 actions (both live and after a resume re-attach).
  const [revertedEdits, setRevertedEdits] = useState<Set<string>>(new Set())

  const push = (b: Block) => setBlocks((prev) => [...prev, b])
  const pushError = (text: string) => push({ kind: 'error', id: nextID(), text })

  // 事件订阅只注册一次(空依赖)，所以回调经 ref 取最新值，避免闭包里拿到旧的
  // onFilesChanged / onOpenPreview / permissions。
  const cb = useRef({ permissions, onFilesChanged, onOpenPreview })
  cb.current = { permissions, onFilesChanged, onOpenPreview }

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
          if (p) cb.current.onOpenPreview(p)
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
        cb.current.onFilesChanged()
        // The turn is over; any still-queued prompts were denied on the backend
        // (context cancel / DenyAll), so drop stale modals.
        cb.current.permissions.clear()
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
        cb.current.permissions.clear()
        // 用户中断/取消回合会以 "context canceled" 抵达——那是停止，不是失败，
        // 别渲染成红色报错块（否则点停止看起来像“出错了”而非“已停止”）。
        const cancelled = /cancel(?:l)?ed/i.test(error)
        setBlocks((prev) => {
          const base = finalizeTools(finalizeStreaming(prev))
          return cancelled ? base : [...base, { kind: 'error', id: nextID(), text: error }]
        })
      }),
      onEvent(Events.PermissionRequest, (req) => cb.current.permissions.enqueue(req)),
      onEvent(Events.Warning, ({ message }) =>
        setBlocks((prev) => [...prev, { kind: 'warning', id: nextID(), text: message }]),
      ),
      // Judge ("smart") mode auto-allowed a risky action without a prompt, or tripped
      // its per-session breaker. An auto-allow is marked on the very tool card it
      // decided (by tool-use id), with the judge's reason shown when the row expands;
      // a breaker trip is a session-level notice.
      onEvent(Events.HarmAutoAllow, (e) => {
        if (e.outcome === 'breaker_tripped') {
          setBlocks((prev) => [...prev, { kind: 'warning', id: nextID(), text: '智能模式已达本会话自动放行上限，后续操作转为逐个确认' }])
          return
        }
        if (e.toolUseID) {
          setHarmAllows((prev) => ({ ...prev, [e.toolUseID]: e.reason || '模型判定为安全，无破坏性操作' }))
        }
      }),
    ]
    return () => offs.forEach((off) => off && off())
    // 只订阅一次:引擎事件是进程级的,重订阅会漏事件也会重复处理。infoRef 是稳定的
    // ref 引用,列进依赖不改变行为;其余外部回调经上面的 cb ref 取最新值。
  }, [infoRef])

  // Pin the conversation to the bottom while output streams, but only if the user
  // is already at (or near) the bottom — once they scroll up to read, streaming
  // updates (especially sub-agents' frequent nested events) must not yank the view
  // back down. Sending a new message re-pins.
  const scrollRef = useRef<HTMLDivElement>(null)
  const chatStick = useRef(true)
  const onChatScroll = () => {
    const el = scrollRef.current
    if (el) chatStick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48
  }
  useEffect(() => {
    const el = scrollRef.current
    if (el && chatStick.current) el.scrollTo({ top: el.scrollHeight })
  }, [blocks, permissions.pending])

  async function send(text: string, attach: string[] = []) {
    if ((!text && attach.length === 0) || busy) return
    const names = attach.map((p) => basename(p))
    chatStick.current = true
    push({ kind: 'user', id: nextID(), text, ts: now(), attachments: names.length ? names : undefined })
    setBusy(true)
    try {
      if (attach.length) await sendMessageWithImages(text, attach)
      else await sendMessage(text)
    } catch (e) {
      setBusy(false)
      pushError(errText(e))
    }
  }

  // 乐观停止：立即取消引擎回合，并即刻收尾 UI（清 busy、结束流式渲染）。
  // 不等 turn:end 事件——事件延迟或漏收都不会让停止“看起来没反应”；真在跑的
  // 回合由 interrupt() 取消，其后续事件到达时 busy 已为 false，幂等无副作用。
  function stop() {
    void interrupt()
    setBusy(false)
    setBlocks((prev) => finalizeTools(finalizeStreaming(prev)))
  }

  // compact 强制把内存历史总结压缩一次。磁盘记录保持完整；这里只缩小下一回合
  // 重新发给模型的内容。
  async function compact() {
    if (busy || compacting || !infoRef.current) return
    setCompacting(true)
    try {
      const r = await compactSession()
      if (r.after < r.before) {
        // Compaction happened: mark the fold point with a divider in the flow
        // (carrying the summary call's own token spend and the new context size), and
        // drop the context meter now to the estimated post-compaction size (the next
        // real turn replaces it with a provider-measured exact count).
        push({ kind: 'compaction', id: nextID(), before: r.before, after: r.after, inTok: r.inputTokens, outTok: r.outputTokens, contextTokens: r.contextTokens })
        setCtxTokens(r.contextTokens)
        setCtxEstimated(true)
      } else {
        // Nothing to compact: a transient hint above the composer, not a row that
        // piles up in the conversation on repeated presses of a short chat.
        showToast('暂无可压缩内容（最近对话已很精简）')
      }
    } catch (e) {
      pushError(errText(e))
    } finally {
      setCompacting(false)
    }
  }

  // undo reverts one edit snapshot (Write/Edit) and marks its card reverted; a revert
  // can create/delete/restore a file, so the workspace file list is refreshed too.
  async function undo(snapshotId: string) {
    try {
      await revertEdit(snapshotId)
      setRevertedEdits((s) => new Set(s).add(snapshotId))
      onFilesChanged()
    } catch (e) {
      push({ kind: 'warning', id: nextID(), text: '撤销失败：' + errText(e) })
    }
  }

  function dismissPlanChoice() {
    setBlocks((prev) => prev.filter((b) => b.kind !== 'planchoice'))
  }

  // reset 清空会话相关的一切显示状态——新建/切工作区时用。
  function reset() {
    setBlocks([])
    setPlan(null)
    setPlanOpen(false)
    setCtxTokens(0)
    setCtxEstimated(false)
  }

  // applyResumed 用恢复出来的历史重建对话：持久化的工具结果重建成执行卡，编辑
  // 元数据按 tool-use id 重新挂回去（恢复载荷本身不带 diff）。
  async function applyResumed(r: ResumedSession, isStale: () => boolean) {
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
    const edits = (await listEdits()) ?? []
    if (isStale()) return
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
    setPlan(null)
    setPlanOpen(false)
    // Seed the usage bar with the reopened history's estimated occupancy so it
    // isn't 0; the first turn replaces it with the provider's exact count.
    setCtxTokens(r.contextTokens ?? 0)
    setCtxEstimated((r.contextTokens ?? 0) > 0)
  }

  return {
    blocks, harmAllows, busy, plan, planOpen, setPlanOpen,
    ctxTokens, ctxEstimated, compacting, revertedEdits,
    scrollRef, onChatScroll,
    send, stop, compact, undo, pushError, dismissPlanChoice, reset, applyResumed,
  }
}
