// Pure conversation-model logic for the chat UI: the block/group data model and the
// reducers that fold streaming tool events into it. Kept free of React so it can be
// unit-tested directly (see chat.test.ts).
import type { ToolEvent, PlanSnapshot, PlanItem, EditRecord } from '@/core/bridge'
import { isEditRecord } from '@/core/bridge'

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
  // compaction marks the point where earlier turns were folded into a summary,
  // rendered as a divider across the conversation flow. inTok/outTok are what the
  // summary call itself spent; contextTokens is the estimated working-history size
  // after compaction (both shown so the fold's cost and result are visible).
  | { kind: 'compaction'; id: string; before: number; after: number; inTok: number; outTok: number; contextTokens: number }
  // retry marks a transient LLM-request failure being retried, rendered as a
  // divider carrying the disconnect reason and the attempt number.
  | { kind: 'retry'; id: string; reason: string; attempt: number; maxAttempts: number }
  | { kind: 'planchoice'; id: string }
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
  | { kind: 'edits'; id: string; edits: EditRecord[] }
  // taskgroup collects a fan-out of parallel Task delegations (adjacent Task calls
  // issued in one assistant response) so they render as one container of compact
  // rows instead of N fully-expanded streaming cards.
  | { kind: 'taskgroup'; id: string; tasks: ToolBlock[] }

export function finalizeStreaming(blocks: Block[]): Block[] {
  return blocks.map((b) => (b.kind === 'assistant' && b.streaming ? { ...b, streaming: false } : b))
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
      // TodoWrite drives the progress pill, never a stream row. This also covers
      // resumed sessions, whose tool blocks bypass the live handler.
      if (b.tool.toolName === 'TodoWrite') continue
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
  const items: PlanItem[] = o.items.map((raw) => {
    const it = (raw ?? {}) as { content?: unknown; status?: unknown; activeForm?: unknown }
    return {
      content: String(it.content ?? ''),
      status: String(it.status ?? 'pending'),
      activeForm: it.activeForm ? String(it.activeForm) : undefined,
    }
  })
  const done = typeof o.done === 'number' ? o.done : items.filter((i) => i.status === 'completed').length
  const total = typeof o.total === 'number' ? o.total : items.length
  return { items, done, total }
}
