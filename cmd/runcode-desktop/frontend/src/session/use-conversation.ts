// useConversation 拥有"对话本身"的全部状态：消息块、引擎事件订阅、发送/停止/
// 压缩、上下文占用与计划快照。它不知道会话是怎么创建或切换的——那是 use-session
// 的事；两者的接缝只有 infoRef(读当前会话，判断有没有会话可压缩)与几个回调。
// 阶段化计划模式是第三块状态，独立在 use-plan 里，与这里只经 send 相连。
import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from 'react'
import {
  compact as compactSession,
  Events,
  errText,
  injectMessage,
  injectMessageWithImages,
  interrupt,
  listEdits,
  onEnvelope,
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
  resumedPlan,
  turnErrorText,
  turnProducedText,
  type AgentNested,
  type Block,
} from '@/chat/blocks'
import { fmtTokens } from '@/core/format'
import { basename } from '@/core/paths'
import { minutesDisplayText, parseRecordingMarker, type RecordingMark } from '@/recorder/minutes'
import {
  convOf, dropConv, lastUserText, patchConv, sessionOf, withReverted,
  type ConvMap, type ConversationState,
} from './conversation-state'

let seq = 0
const nextID = () => `b${++seq}`
const now = () =>
  new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })

export type Conversation = ReturnType<typeof useConversation>

export function useConversation({ focusedId, infoRef, permissions, onFilesChanged, onOpenPreview, showToast }: {
  /** focusedId 是当前**看得见**的那条会话。状态每会话存一份，渲染只取这一条。 */
  focusedId: string
  infoRef: RefObject<SessionInfo | null>
  permissions: {
    pending: PermissionRequest | null
    enqueue: (sessionID: string, req: PermissionRequest) => void
    clear: (sessionID: string) => void
  }
  onFilesChanged: () => void
  onOpenPreview: (absPath: string) => void
  showToast: (text: string) => void
}) {
  // 每会话一份状态。引擎本来就是多会话的，每条信封都带 sessionId；存成一份全局
  // 状态时，后台会话的输出会记到前台会话头上。planOpen 不在其中——它是"胶囊下拉
  // 开着没有"这种即时界面态，不是对话数据。
  const [convs, setConvs] = useState<ConvMap>({})
  const [planOpen, setPlanOpen] = useState(false)
  const cur = convOf(convs, focusedId)
  const { blocks, harmAllows, busy, plan, ctxTokens, ctxEstimated, compacting, revertedEdits } = cur

  // focusedRef 让"只注册一次"的事件处理器读得到最新的聚焦会话 id（闭包里不能直接
  // 读 focusedId）。事件本身按信封里的 id 落，这个只用于判断"要不要动界面"。
  const focusedRef = useRef(focusedId)
  focusedRef.current = focusedId

  // patch 改**指定**会话；下面那组 setXxx 是"改聚焦会话"的薄壳，签名与原来的
  // useState setter 一致，所以发送/停止/压缩那些本来就只作用于当前会话的动作
  // 一行都不用改。事件处理器则一律走 patch(sessionOf(env), …)。
  //
  // patch/blocksOf 必须是**稳定引用**:事件订阅只注册一次,而 exhaustive-deps 会
  // 要求把它们列进依赖。每次渲染重建的话,列进去就等于每次渲染都重订阅(漏事件 +
  // 重复处理),不列又要压规则。useCallback 让"列进依赖"与"只订阅一次"同时成立。
  const patch = useCallback((id: string, fn: (s: ConversationState) => ConversationState) => {
    setConvs((m) => patchConv(m, id, fn))
  }, [])
  const blocksOf = useCallback((id: string, fn: (prev: Block[]) => Block[]) => {
    patch(id, (s) => {
      const next = fn(s.blocks)
      return next === s.blocks ? s : { ...s, blocks: next }
    })
  }, [patch])

  type Upd<T> = T | ((prev: T) => T)
  const apply = <T,>(u: Upd<T>, prev: T): T => (typeof u === 'function' ? (u as (p: T) => T)(prev) : u)

  const setBlocks = (u: Upd<Block[]>) => blocksOf(focusedRef.current, (prev) => apply(u, prev))
  const setBusy = (v: boolean) => patch(focusedRef.current, (s) => ({ ...s, busy: v }))
  const setCtxTokens = (v: number) => patch(focusedRef.current, (s) => ({ ...s, ctxTokens: v }))
  const setCtxEstimated = (v: boolean) => patch(focusedRef.current, (s) => ({ ...s, ctxEstimated: v }))
  const setCompacting = (v: boolean) => patch(focusedRef.current, (s) => ({ ...s, compacting: v }))

  const push = (b: Block) => setBlocks((prev) => [...prev, b])
  const pushError = (text: string) => push({ kind: 'error', id: nextID(), text })

  // userStopped 记住哪些会话的下一次取消是用户按了「停止」，那样它的
  // "context canceled" turn:error 会被吞掉而不是红着显示；上游/网络导致的取消不
  // 在集合里，原因照常暴露。**按会话记**：并行时 A 会话按停止不该让 B 会话的失败
  // 被静默吞掉。用 ref 是因为只注册一次的事件处理器要读到最新值。
  const userStopped = useRef<Set<string>>(new Set())

  // 事件订阅只注册一次(空依赖)，所以回调经 ref 取最新值，避免闭包里拿到旧的
  // onFilesChanged / onOpenPreview / permissions。
  const cb = useRef({ permissions, onFilesChanged, onOpenPreview })
  cb.current = { permissions, onFilesChanged, onOpenPreview }

  useEffect(() => {
    const offs = [
      onEnvelope(Events.AssistantDelta, (env) => {
        const { text } = env.payload
        blocksOf(sessionOf(env), (prev) => {
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
      onEnvelope(Events.AssistantThinking, (env) => {
        const { text } = env.payload
        blocksOf(sessionOf(env), (prev) => {
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
      onEnvelope(Events.Retry, (env) => {
        const { reason, attempt, maxAttempts } = env.payload
        blocksOf(sessionOf(env), (prev) => {
          const divider = { kind: 'retry' as const, id: nextID(), reason, attempt, maxAttempts }
          const last = prev[prev.length - 1]
          if (last && last.kind === 'assistant' && last.streaming) {
            return [...prev.slice(0, -1), divider]
          }
          return [...prev, divider]
        })
      }),
      onEnvelope(Events.ToolEvent, (env) => {
        const ev = env.payload
        // The main agent's TodoWrite plan drives the right-rail progress board, not a
        // stream row, so intercept it here (all of its started/progress/completed
        // events carry toolName). Sub-agent todos (parentToolUseID set) are left to
        // nest inside their Task card, so only the main plan reaches the board.
        if (!ev.parentToolUseID && ev.toolName === 'TodoWrite') {
          const snap = parsePlan(ev)
          if (snap) patch(sessionOf(env), (s) => ({ ...s, plan: snap }))
          return
        }
        // 只有**看得见**的那条会话才准打开预览面板:后台会话弹一个面板出来会把
        // 用户正在看的东西顶掉,而他甚至不知道那是哪条会话干的。
        if (!ev.parentToolUseID && ev.toolName === 'open_preview' && sessionOf(env) === focusedRef.current) {
          const p = (ev.data as { path?: string } | undefined)?.path
          if (p) cb.current.onOpenPreview(p)
        }
        blocksOf(sessionOf(env), (prev) => {
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
      // Live context occupancy: emitted before every model round-trip inside a turn,
      // and again right after automatic context control shortens the history, so the
      // meter shows both the climb and the drop as they happen.
      onEnvelope(Events.ContextUsage, (env) => {
        patch(sessionOf(env), (s) => ({ ...s, ctxTokens: env.payload.contextTokens, ctxEstimated: false }))
      }),
      onEnvelope(Events.TurnEnd, (env) => {
        const end = env.payload
        const sid = sessionOf(env)
        patch(sid, (s) => ({
          ...s,
          busy: false,
          // 有 contextTokens 才更新:现在是按已提交的历史实测出来的。
          ...(end.contextTokens ? { ctxTokens: end.contextTokens, ctxEstimated: false } : {}),
        }))
        userStopped.current.delete(sid)
        // TODO(P3 多工作区): 这里刷的是"当前工作区"的文件列表。等会话可以各在
        // 各的目录时,要按这条会话的工作区刷,而不是无条件刷当前那个。
        cb.current.onFilesChanged()
        // The turn is over; any still-queued prompts were denied on the backend
        // (context cancel / DenyAll), so drop stale modals — **这条会话的**。
        cb.current.permissions.clear(sid)
        blocksOf(sid, (prev) => {
          let next = finalizeTools(finalizeStreaming(prev))
          // Context control ran during this turn. Say so: the meter just fell, and an
          // unexplained drop reads as a glitch rather than the system working.
          if (end.contextTokensSaved) {
            next = [...next, {
              kind: 'notice', id: nextID(),
              text: `上下文已自动整理，回收约 ${fmtTokens(end.contextTokensSaved)} tokens(过期的工具产出会按需重新读取)`,
            }]
          }
          // Did THIS turn produce assistant text? Scoped to the current turn so an
          // earlier reply can't mask an empty one (see turnProducedText).
          const producedText = turnProducedText(next)
          if (!producedText && end.text.trim()) {
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
            end.text.trim() === '' && end.toolResultCount === 0 && !end.stopped && !isAsk && !producedText
          if (emptyResponse) {
            const lastUser = [...next].reverse().find((b) => b.kind === 'user') as Extract<Block, { kind: 'user' }> | undefined
            const hadImage = (lastUser?.attachments?.length ?? 0) > 0
            next = [...next, {
              kind: 'notice', id: nextID(),
              text: hadImage
                ? '模型返回了空内容 —— 当前模型/接口可能不支持图片输入。可在「设置」换用支持视觉的模型。'
                : '模型返回了空内容(可能被截断、触发内容限制,或当前模型/接口不兼容)。',
            }]
          }
          // Close the reply with this turn's own token spend (a faint footer). It
          // counts only the parent session's model calls; a delegated sub-agent's
          // tokens are shown on its Task card instead, so the two never double-count.
          if (end.inputTokens > 0 || end.outputTokens > 0) {
            next = [...next, { kind: 'usage', id: nextID(), inTok: end.inputTokens, outTok: end.outputTokens, durMs: end.durationMs }]
          }
          return next
        })
      }),
      onEnvelope(Events.TurnError, (env) => {
        const { error } = env.payload
        const sid = sessionOf(env)
        patch(sid, (s) => ({ ...s, busy: false }))
        cb.current.permissions.clear(sid)
        // 只有用户确实点了「停止」,取消才当作停止吞掉(乐观停止已把界面收成「已停止」)。
        // 上游/网络/超时导致的取消不是用户意图,必须显示——否则就成了"错误了却看不到
        // 原因"。turnErrorText 兜住这层判断并给空原因兜底。
        const text = turnErrorText(error, userStopped.current.has(sid))
        userStopped.current.delete(sid)
        blocksOf(sid, (prev) => {
          const base = finalizeTools(finalizeStreaming(prev))
          return text === null ? base : [...base, { kind: 'error', id: nextID(), text }]
        })
      }),
      // 授权请求按会话入队。**不能**只收聚焦会话的:后台会话卡在等授权而界面
      // 毫无表示,是并行里最坏的一种失败——任务停着,用户以为它在跑。
      onEnvelope(Events.PermissionRequest, (env) => cb.current.permissions.enqueue(sessionOf(env), env.payload)),
      onEnvelope(Events.Warning, (env) =>
        blocksOf(sessionOf(env), (prev) => [...prev, { kind: 'warning', id: nextID(), text: env.payload.message }]),
      ),
      // Judge ("smart") mode auto-allowed a risky action without a prompt, or tripped
      // its per-session breaker. An auto-allow is marked on the very tool card it
      // decided (by tool-use id), with the judge's reason shown when the row expands;
      // a breaker trip is a session-level notice.
      onEnvelope(Events.HarmAutoAllow, (env) => {
        const e = env.payload
        if (e.outcome === 'breaker_tripped') {
          blocksOf(sessionOf(env), (prev) => [...prev, { kind: 'warning', id: nextID(), text: '智能模式已达本会话自动放行上限，后续操作转为逐个确认' }])
          return
        }
        if (e.toolUseID) {
          patch(sessionOf(env), (s) => ({
            ...s,
            harmAllows: { ...s.harmAllows, [e.toolUseID]: e.reason || '模型判定为安全，无破坏性操作' },
          }))
        }
      }),
    ]
    return () => offs.forEach((off) => off && off())
    // 只订阅一次:引擎事件是进程级的,重订阅会漏事件也会重复处理。列进依赖的三个
    // 都是稳定引用(infoRef 是 ref;patch/blocksOf 经 useCallback 固定),所以这条
    // 依赖数组不会导致重订阅;其余外部回调经上面的 cb ref 取最新值。
  }, [infoRef, patch, blocksOf])

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

  // display 让「发给模型的」和「显示在对话里的」分开。
  //
  // 目前只有会后纪要用它：那条消息里带着整篇转写，几千字原样铺在对话里，把用户
  // 自己的对话历史整个冲掉了；设计稿那个位置本来就只有一句话。传了 display 就
  // 只影响这一条气泡的显示，发给模型的仍是 text。
  //
  // 代价：会话恢复时历史由引擎回放，那边只有真正的 text，所以恢复后会看到全文。
  // 这是可接受的——恢复本来就是"看当时到底发生了什么"的场景。
  async function send(text: string, attach: string[] = [], display?: string) {
    if (!text && attach.length === 0) return
    // 回合进行中:改为"中途插入"——把消息交给引擎,在下一个工具回合边界喂给模型
    // (mid-turn steering),而不是被丢弃或干等整轮结束。见 supplement。
    if (busy) { void supplement(text, attach, display); return }
    const names = attach.map((p) => basename(p))
    // 这一轮属于**按下发送时**的那条会话:下面几步是异步的,中途聚焦可能已经切走。
    const sid = focusedRef.current
    // A fresh turn is not a stop: any cancellation from here on must surface.
    userStopped.current.delete(sid)
    chatStick.current = true
    push({ kind: 'user', id: nextID(), text: display ?? text, ts: now(), attachments: names.length ? names : undefined })
    setBusy(true)
    try {
      if (attach.length) await sendMessageWithImages(sid, text, attach)
      else await sendMessage(sid, text)
    } catch (e) {
      setBusy(false)
      pushError(errText(e))
    }
  }

  // supplement 是"补充/中途插入":回合进行中把消息插进正在跑的回合。引擎在下一次
  // 模型调用前(当前工具回合结束时)把它喂进上下文,模型随即就能看到,无需等整轮跑完。
  // 消息先乐观入流(用户能立刻看到自己插了什么);若插入时回合恰好已结束(竞态),引擎
  // 退化为新起一轮并回传 startedTurn=true,这里据此把 busy 重新置起。
  async function supplement(text: string, attach: string[] = [], display?: string) {
    if (!text && attach.length === 0) return
    const names = attach.map((p) => basename(p))
    const sid = focusedRef.current
    chatStick.current = true
    push({ kind: 'user', id: nextID(), text: display ?? text, ts: now(), attachments: names.length ? names : undefined })
    try {
      const startedTurn = attach.length
        ? await injectMessageWithImages(sid, text, attach)
        : await injectMessage(sid, text)
      // 竞态:插入时回合刚结束,引擎改起新一轮——补上 busy(当前回合的 turn:end 已把它置回 false)。
      if (startedTurn) {
        userStopped.current.delete(focusedRef.current)
        setBusy(true)
      }
    } catch (e) {
      pushError(errText(e))
    }
  }

  // 乐观停止：立即取消引擎回合，并即刻收尾 UI（清 busy、结束流式渲染）。
  // 不等 turn:end 事件——事件延迟或漏收都不会让停止“看起来没反应”；真在跑的
  // 回合由 interrupt() 取消，其后续事件到达时 busy 已为 false，幂等无副作用。
  function stop() {
    // Mark the stop as user-initiated so the resulting "context canceled" turn:error
    // is swallowed (this optimistic finalize is the visible outcome), not shown red.
    userStopped.current.add(focusedRef.current)
    void interrupt(focusedRef.current)
    setBusy(false)
    setBlocks((prev) => finalizeTools(finalizeStreaming(prev)))
  }

  // compact 强制把内存历史总结压缩一次。磁盘记录保持完整；这里只缩小下一回合
  // 重新发给模型的内容。
  async function compact() {
    if (busy || compacting || !infoRef.current) return
    setCompacting(true)
    try {
      const r = await compactSession(focusedRef.current)
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
      await revertEdit(focusedRef.current, snapshotId)
      patch(focusedRef.current, (s) => withReverted(s, snapshotId))
      onFilesChanged()
    } catch (e) {
      push({ kind: 'warning', id: nextID(), text: '撤销失败：' + errText(e) })
    }
  }

  // pushRecording 把一场录完的录音钉进当前这条对话。
  //
  // 它必须在发纪要请求**之前**调用，这样卡片就落在请求上方——录音先于请求发生，
  // 顺序反了读起来就是"先请求整理，然后才录的音"。
  function pushRecording(mark: RecordingMark) {
    chatStick.current = true
    push({ kind: 'recording', id: nextID(), mark })
  }

  // reset 丢掉**上一条**会话的显示状态——新建/切工作区时用。
  //
  // 状态按会话存之后这件事变简单了：新会话的 id 在表里本来就没有条目，取到的
  // 就是空状态，不需要"清空"。这里要做的是把被替换掉的那条从表里删掉，否则每
  // 换一次会话就留一份再也不会被看到的历史在内存里。
  //
  // 调用时机很关键：use-session 里 reset() 跟在 setInfo(新会话) 之后同步调用，
  // 那一刻 React 还没重渲染，focusedRef 仍指着**旧**会话——正是要删的那个。
  function reset() {
    setConvs((m) => dropConv(m, focusedRef.current))
    setPlanOpen(false)
  }

  // applyResumed 用恢复出来的历史重建对话：持久化的工具结果重建成执行卡，编辑
  // 元数据按 tool-use id 重新挂回去（恢复载荷本身不带 diff）。
  async function applyResumed(r: ResumedSession, isStale: () => boolean) {
    // 恢复出来的历史必须落到**这条被恢复的会话**头上,不能用 focusedRef。
    //
    // use-session 里 setInfo(r.info) 与本函数是同步相邻的两句,那一刻 React 还没
    // 重渲染,focusedRef 仍指着上一条会话——按它写就是把整段历史塞进了别人的对话,
    // 而恢复出来的这条看着空空如也。恢复载荷自带 info,直接用它。
    const sid = r.info?.sessionId ?? ''
    const setBlocks = (u: Block[] | ((prev: Block[]) => Block[])) =>
      blocksOf(sid, (prev) => (typeof u === 'function' ? u(prev) : u))
    const setRevertedEdits = (v: Set<string>) => patch(sid, (st) => ({ ...st, revertedEdits: v }))
    const setPlan = (v: PlanSnapshot | null) => patch(sid, (st) => ({ ...st, plan: v }))
    const setCtxTokens = (v: number) => patch(sid, (st) => ({ ...st, ctxTokens: v }))
    const setCtxEstimated = (v: boolean) => patch(sid, (st) => ({ ...st, ctxEstimated: v }))

    setBlocks(
      (r.blocks ?? []).flatMap((b): Block[] => {
        if (b.kind === 'user') {
          // 纪要请求：把开头那行标记还原成录音卡片，正文换回那一句短的。
          // 不做这一步，恢复出来的历史里会是几千字的提示词原文，而卡片没了。
          const mark = parseRecordingMarker(b.text ?? '')
          if (mark) {
            return [
              { kind: 'recording', id: nextID(), mark },
              { kind: 'user', id: nextID(), text: minutesDisplayText(mark.title), ts: '' },
            ]
          }
          return [{ kind: 'user', id: nextID(), text: b.text ?? '', ts: '' }]
        }
        if (b.kind === 'assistant') return [{ kind: 'assistant', id: nextID(), text: b.text ?? '', streaming: false, ts: '' }]
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
        return [{
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
        }]
      }),
    )
    const edits = (await listEdits(sid)) ?? []
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
    // Rebuild the progress board from the reopened history: the live board is fed by
    // TodoWrite events, which a resume never replays, so an unfinished task list would
    // otherwise disappear the moment the session is reopened.
    setPlan(resumedPlan(r.blocks))
    setPlanOpen(false)
    // Seed the usage bar with the reopened history's estimated occupancy so it
    // isn't 0; the first turn replaces it with the provider's exact count.
    setCtxTokens(r.contextTokens ?? 0)
    setCtxEstimated((r.contextTokens ?? 0) > 0)
  }

  // busyBySession 是"哪几条会话有回合在跑"，给会话列表的运行指示用。
  //
  // 用前端这份而不是后端快照：回合状态每秒都在变，靠轮询后端要么不及时要么太吵；
  // 这份本来就是按会话记的（P1a），顺手导出即可。
  const busyBySession = useMemo(() => {
    const out: Record<string, boolean> = {}
    for (const [id, s] of Object.entries(convs)) {
      if (s.busy) out[id] = true
    }
    return out
  }, [convs])

  // lastUserBySession 是每条会话最近一次提问，给会话列表当**临时标题**用：
  // 自动标题要等回合结束才由模型生成，而并行时最需要认出哪条是哪条的时刻恰恰是
  // 回合正在跑的时候，那之前整栏都是「新对话」，谁是谁全靠猜。
  //
  // 每次 delta 都会重算，所以 lastUserText 是从末尾往前找、找到就停——扫过的量
  // 是当前回合产生的块数，与整条对话的长度无关。
  const lastUserBySession = useMemo(() => {
    const out: Record<string, string> = {}
    for (const [id, s] of Object.entries(convs)) {
      const text = lastUserText(s)
      if (text) out[id] = text
    }
    return out
  }, [convs])

  // dropSession 丢掉一条会话的对话状态。用户关掉一条会话时调用——留着就是纯占
  // 内存，而且会让 busyBySession 里挂着一条已经不存在的会话。
  const dropSession = (id: string) => setConvs((m) => dropConv(m, id))

  return {
    blocks, harmAllows, busy, plan, planOpen, setPlanOpen, busyBySession, lastUserBySession, dropSession,
    ctxTokens, ctxEstimated, compacting, revertedEdits,
    scrollRef, onChatScroll,
    send, stop, compact, undo, pushError, reset, applyResumed, pushRecording,
  }
}
