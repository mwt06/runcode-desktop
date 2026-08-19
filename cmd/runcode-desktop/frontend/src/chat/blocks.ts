// Pure conversation-model logic for the chat UI: the block/group data model and the
// reducers that fold streaming tool events into it. Kept free of React so it can be
// unit-tested directly (see chat.test.ts).
import type { ToolEvent, PlanSnapshot, PlanItem, EditRecord, ResumedBlock } from '@/core/bridge'
import { isEditRecord } from '@/core/bridge'
import { type RecordingMark } from '@/recorder/minutes'

// AgentNested holds a sub-agent's live activity, shown nested inside its Task card:
// the streamed assistant text and the child tool events (merged by tool-use id).
export type AgentNested = { agent: string; text: string; tools: ToolEvent[]; usage?: { inTok: number; outTok: number; durMs?: number } }
export type Block =
  | { kind: 'user'; id: string; text: string; ts: string; attachments?: string[] }
  | { kind: 'assistant'; id: string; text: string; thinking?: string; streaming: boolean; ts: string }
  | { kind: 'tool'; id: string; tool: ToolEvent; nested?: AgentNested }
  | { kind: 'error'; id: string; text: string }
  | { kind: 'warning'; id: string; text: string }
  | { kind: 'notice'; id: string; text: string }
  // recording 是一场已结束的录音。它是对话的一部分——发起纪要的那一刻钉在这里，
  // 换条对话就看不见，恢复历史时原样回来。不是浮在界面上的组件。
  | { kind: 'recording'; id: string; mark: RecordingMark }
  // compaction marks the point where earlier turns were folded into a summary,
  // rendered as a divider across the conversation flow. inTok/outTok are what the
  // summary call itself spent; contextTokens is the estimated working-history size
  // after compaction (both shown so the fold's cost and result are visible).
  | { kind: 'compaction'; id: string; before: number; after: number; inTok: number; outTok: number; contextTokens: number }
  // retry marks a transient LLM-request failure being retried, rendered as a
  // divider carrying the disconnect reason and the attempt number.
  | { kind: 'retry'; id: string; reason: string; attempt: number; maxAttempts: number }
  // usage closes a completed reply with that turn's own token spend (the parent
  // session's model calls — sub-agent tokens are shown on the Task card instead).
  | { kind: 'usage'; id: string; inTok: number; outTok: number; durMs?: number }

// ToolBlock narrows Block to a tool row; taskgroup members are always Task tools.
export type ToolBlock = Extract<Block, { kind: 'tool' }>

export type Group =
  | { kind: 'block'; block: Block }
  | { kind: 'exec'; id: string; tools: ToolEvent[] }
  | { kind: 'ask'; id: string; tool: ToolEvent }
  | { kind: 'analyze'; id: string; tool: ToolEvent }
  // skill is one "模型加载了某个技能" moment. It leaves the compact tool list
  // because a bare 加载技能 row says nothing: the call carries only a name, so
  // what the model actually picked up (and why) is only visible on a card.
  | { kind: 'skill'; id: string; tool: ToolEvent }
  | { kind: 'edits'; id: string; edits: EditRecord[] }
  // taskgroup collects a fan-out of parallel Task delegations (adjacent Task calls
  // issued in one assistant response) so they render as one container of compact
  // rows instead of N fully-expanded streaming cards.
  | { kind: 'taskgroup'; id: string; tasks: ToolBlock[] }

export function finalizeStreaming(blocks: Block[]): Block[] {
  return blocks.map((b) => (b.kind === 'assistant' && b.streaming ? { ...b, streaming: false } : b))
}

// turnProducedText reports whether the just-ended turn produced any assistant text.
// Only the blocks after the last user message count — those belong to this turn.
// Scanning the whole conversation would let an earlier turn's reply mask an empty
// one: switch models mid-chat, ask again, get nothing back, and the "empty content"
// notice would wrongly stay hidden because a prior answer still sits in the history.
export function turnProducedText(blocks: Block[]): boolean {
  const lastUser = blocks.map((b) => b.kind).lastIndexOf('user')
  return blocks.slice(lastUser + 1).some((b) => b.kind === 'assistant' && b.text.trim() !== '')
}

// turnErrorText decides how a turn:error is surfaced. A cancellation is swallowed
// ONLY when the user actually pressed stop — that path is already finalized as
// "已停止" by the optimistic stop, so a red error would be wrong. Every other
// failure must be shown, including a cancellation the user did not ask for
// (upstream/network/timeout), which previously matched the same regex and vanished
// silently — "错误了却看不到原因". The empty fallback keeps a blank reason from
// rendering as an unexplained empty red box. Returns null to suppress.
export function turnErrorText(error: string, userStopped: boolean): string | null {
  if (userStopped && /cancel(?:l)?ed/i.test(error)) return null
  return error.trim() || '回合失败（未返回具体原因）'
}

// RETRY_KIND_ZH maps the engine's neutral error-kind tokens (llm.ErrorKind) to a
// Chinese label. The retry reason arrives as a bare kind ("transport") or a kind
// with a status ("server (HTTP 503)"), so retryReasonLabel translates the kind and
// keeps any "(HTTP nnn)" suffix — otherwise the divider leaks raw English like
// "transport" into the 中文 UI.
const RETRY_KIND_ZH: Record<string, string> = {
  transport: '连接中断',
  server: '服务端错误',
  rate_limited: '请求过于频繁',
  overloaded: '服务过载',
  auth: '鉴权失败',
  invalid_request: '请求无效',
  unknown: '未知错误',
}

// retryReasonLabel renders an engine retry reason as a Chinese label. Unknown or
// already-localized reasons (e.g. the "连接中断" fallback) pass through unchanged.
export function retryReasonLabel(reason: string): string {
  const m = /^([a-z_]+)(\s*\(HTTP \d+\))?$/.exec(reason.trim())
  if (!m) return reason
  const zh = RETRY_KIND_ZH[m[1]]
  if (!zh) return reason
  return m[2] ? `${zh}${m[2]}` : zh
}

// endTool marks a tool event that never reached a terminal state as cancelled, so a
// turn that ends or is interrupted mid-tool-call doesn't leave a spinning card.
export function endTool(t: ToolEvent): ToolEvent {
  if (t.type === 'completed' || t.type === 'failed') return t
  return { ...t, type: 'failed', message: t.message || 'cancelled' }
}

// finalizeTools closes out any still-running tool (and nested sub-agent child tools)
// when a turn ends or errors.
export function finalizeTools(blocks: Block[]): Block[] {
  return blocks.map((b) => {
    if (b.kind !== 'tool') return b
    const tool = endTool(b.tool)
    const nested = b.nested ? { ...b.nested, tools: b.nested.tools.map(endTool) } : b.nested
    return { ...b, tool, nested }
  })
}

// MAX_LIVE_OUTPUT_LINES caps the live-streamed output kept per tool while it runs,
// so a chatty command's tail doesn't grow unbounded in memory. The completed event
// replaces this with the tool's canonical (already bounded) output.
export const MAX_LIVE_OUTPUT_LINES = 400
export function mergeTool(prev: ToolEvent | undefined, ev: ToolEvent): ToolEvent {
  if (!prev) return ev
  // A completed/failed event carries the authoritative full output; it replaces the
  // live-streamed tail so lines aren't duplicated. Streaming 'output'/'progress'
  // events append, capped to a tail.
  const finalEvent = ev.type === 'completed' || ev.type === 'failed'
  let output: NonNullable<ToolEvent['output']>
  if (finalEvent && (ev.output?.length ?? 0) > 0) {
    output = ev.output ?? []
  } else {
    output = [...(prev.output ?? []), ...(ev.output ?? [])]
    if (output.length > MAX_LIVE_OUTPUT_LINES) output = output.slice(-MAX_LIVE_OUTPUT_LINES)
  }
  return {
    ...prev,
    type: ev.type,
    toolName: ev.toolName || prev.toolName,
    // Input arrives on the started event; keep it as later events omit it.
    input: ev.input ?? prev.input,
    message: ev.message || prev.message,
    // Keep earlier files (e.g. Glob's matched list from a progress event) when a
    // later event carries none — the completed event can arrive with an empty
    // array, which must not erase them.
    files: ev.files?.length ? ev.files : prev.files,
    filesTotal: ev.filesTotal ?? prev.filesTotal,
    output,
    outputTotal: ev.outputTotal ?? prev.outputTotal,
    outputTruncated: ev.outputTruncated ?? prev.outputTruncated,
    // Side-channel payload (edit metadata / plan snapshot) arrives on the final
    // event; keep whichever event carries it so the edited card survives the merge.
    data: ev.data ?? prev.data,
  }
}

// resumedMatchedFiles reconstructs a file-list result's matched references from the
// persisted result text, so a resumed session's Glob / Grep(files_with_matches)
// card renders the same collapsible tree as the live one — the live matched-file
// events are not persisted; only the result text (one workspace-relative path per
// line, possibly ending with an "[output truncated]" marker) survives a restart.
// Returns null when the result is not a pure file list (content/count 模式、报错
// 或提示语等自由文本),此时调用方保持原样按文本行渲染。
export function resumedMatchedFiles(
  toolName: string | undefined,
  input: unknown,
  outputText: string,
): { path: string; kind: string }[] | null {
  if (toolName === 'Grep') {
    let mode = ''
    if (typeof input === 'string') {
      try {
        mode = String((JSON.parse(input) as { output_mode?: unknown }).output_mode ?? '')
      } catch {
        return null
      }
    } else if (input && typeof input === 'object') {
      mode = String((input as { output_mode?: unknown }).output_mode ?? '')
    }
    if (mode !== 'files_with_matches') return null
  } else if (toolName !== 'Glob') {
    return null
  }
  const paths = outputText.split('\n').map((l) => l.trim()).filter((l) => l !== '' && l !== '[output truncated]')
  // 文件列表一行一个路径;出现空白说明是提示语/意外格式,放弃重建按文本展示。
  if (paths.length === 0 || paths.some((p) => /\s/.test(p))) return null
  return paths.map((p) => ({ path: p, kind: 'matched' }))
}

// groupBlocks routes blocks into render groups: TodoWrite is dropped (it drives the
// progress pill), AskUser/Analyze/Task each become their own card, and consecutive
// plain tool calls collapse into one execution group. A Write/Edit that carries edit
// metadata also accumulates into that turn's `edits` group, flushed at the turn's end.
export function groupBlocks(blocks: Block[]): Group[] {
  const out: Group[] = []
  // Per-turn edited files, deduped by relPath (latest wins). Flushed as one `edits`
  // group at the end of each turn (before the next user block, or at the end), so
  // the cards sit under the reply — matching the artifact cards' placement.
  let pending = new Map<string, EditRecord>()
  let pendingId = ''
  const flush = () => {
    if (pending.size === 0) return
    out.push({ kind: 'edits', id: 'edits-' + pendingId, edits: [...pending.values()] })
    pending = new Map()
    pendingId = ''
  }
  for (const b of blocks) {
    if (b.kind === 'user') {
      flush() // end of the previous turn
      out.push({ kind: 'block', block: b })
      continue
    }
    if (b.kind === 'tool') {
      // A Write/Edit that carries edit metadata also contributes an "已编辑" card.
      if (isEditRecord(b.tool.data)) {
        const rec = b.tool.data
        pending.set(rec.relPath, rec)
        if (!pendingId) pendingId = b.id
      }
      // TodoWrite drives the progress pill and plan_write the staged plan board —
      // never a stream row, or the same content would be on screen twice. This also
      // covers resumed sessions, whose tool blocks bypass the live handler.
      if (b.tool.toolName === 'TodoWrite' || b.tool.toolName === 'plan_write') continue
      // AskUser renders as its own interactive question, not in an execution group.
      if (b.tool.toolName === 'AskUser') {
        out.push({ kind: 'ask', id: b.id, tool: b.tool })
        continue
      }
      // Analyze carries the model's structured-thinking protocol — render it as its
      // own visual card, not merged into the compact tool list.
      if (b.tool.toolName === 'Analyze') {
        out.push({ kind: 'analyze', id: b.id, tool: b.tool })
        continue
      }
      // A skill load is its own card: it announces which reusable workflow the
      // model is now following, which is worth more than one row in a list of
      // greps.
      if (b.tool.toolName === 'Skill') {
        out.push({ kind: 'skill', id: b.id, tool: b.tool })
        continue
      }
      // A Task delegation is its own observable card (its sub-agent's live text and
      // nested tool calls), so it never folds into the compact exec list — otherwise
      // its nested activity is dropped. Adjacent Task calls (a parallel fan-out
      // issued in one assistant response) merge into a taskgroup so N delegations
      // render as one container of compact rows; a lone Task stays a full card.
      if (b.tool.toolName === 'Task') {
        const last = out[out.length - 1]
        if (last && last.kind === 'taskgroup') {
          last.tasks.push(b)
        } else if (last && last.kind === 'block' && last.block.kind === 'tool' && last.block.tool.toolName === 'Task') {
          out[out.length - 1] = { kind: 'taskgroup', id: 'tasks-' + last.block.id, tasks: [last.block, b] }
        } else {
          out.push({ kind: 'block', block: b })
        }
        continue
      }
      const last = out[out.length - 1]
      if (last && last.kind === 'exec') last.tools.push(b.tool)
      else out.push({ kind: 'exec', id: b.id, tools: [b.tool] })
      continue
    }
    out.push({ kind: 'block', block: b })
  }
  flush() // trailing turn
  return out
}

// parsePlan reads the PlanSnapshot the TodoWrite tool attaches to a progress event's
// `data` field. Only the progress event carries it (started/completed do not), so
// this returns null for those. Tolerant of missing counts: done/total are recomputed
// from items when absent.
export function parsePlan(ev: ToolEvent): PlanSnapshot | null {
  const d = (ev as ToolEvent & { data?: unknown }).data
  if (!d || typeof d !== 'object') return null
  const o = d as { items?: unknown; done?: unknown; total?: unknown }
  if (!Array.isArray(o.items)) return null
  const items = toPlanItems(o.items)
  const done = typeof o.done === 'number' ? o.done : items.filter((i) => i.status === 'completed').length
  const total = typeof o.total === 'number' ? o.total : items.length
  return { items, done, total }
}

// resumedPlan rebuilds the progress board from a reopened session's blocks: the
// board is live state fed by TodoWrite events, and a resume replays history instead
// of events, so without this the pill vanishes on reopening a session whose task
// list is still unfinished. The last TodoWrite call is the current list by the
// tool's own contract (each call replaces the previous one), and its arguments ride
// along in the resumed block, so no snapshot needs to be persisted for this.
// Returns null when the session never recorded one (or the arguments are unusable).
export function resumedPlan(blocks: ResumedBlock[] | null | undefined): PlanSnapshot | null {
  for (let i = (blocks?.length ?? 0) - 1; i >= 0; i--) {
    const t = blocks?.[i]?.tool
    if (!t || t.toolName !== 'TodoWrite' || !t.input) continue
    let todos: unknown
    try {
      todos = (JSON.parse(t.input) as { todos?: unknown } | null)?.todos
    } catch {
      return null // malformed arguments: no board is better than a wrong one
    }
    if (!Array.isArray(todos) || todos.length === 0) return null
    const items = toPlanItems(todos)
    return { items, done: items.filter((it) => it.status === 'completed').length, total: items.length }
  }
  return null
}

// toPlanItems normalizes the tool's raw items (from a live event or a resumed
// call's arguments — the same shape) into PlanItems, defaulting a missing status
// to pending so one odd item cannot break the board.
function toPlanItems(raw: unknown[]): PlanItem[] {
  return raw.map((entry) => {
    const it = (entry ?? {}) as { content?: unknown; status?: unknown; activeForm?: unknown }
    return {
      content: String(it.content ?? ''),
      status: String(it.status ?? 'pending'),
      activeForm: it.activeForm ? String(it.activeForm) : undefined,
    }
  })
}
