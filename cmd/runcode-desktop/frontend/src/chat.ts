// Pure conversation-model logic for the chat UI: the block/group data model and the
// reducers that fold streaming tool events into it. Kept free of React so it can be
// unit-tested directly (see chat.test.ts).
import type { ToolEvent, PlanSnapshot, PlanItem } from './bridge'

// AgentNested holds a sub-agent's live activity, shown nested inside its Task card:
// the streamed assistant text and the child tool events (merged by tool-use id).
export type AgentNested = { agent: string; text: string; tools: ToolEvent[] }
export type Block =
  | { kind: 'user'; id: string; text: string; ts: string; attachments?: string[] }
  | { kind: 'assistant'; id: string; text: string; thinking?: string; streaming: boolean; ts: string }
  | { kind: 'tool'; id: string; tool: ToolEvent; nested?: AgentNested }
  | { kind: 'error'; id: string; text: string }
  | { kind: 'warning'; id: string; text: string }
  | { kind: 'notice'; id: string; text: string }
  | { kind: 'planchoice'; id: string }

export type Group =
  | { kind: 'block'; block: Block }
  | { kind: 'exec'; id: string; tools: ToolEvent[] }
  | { kind: 'ask'; id: string; tool: ToolEvent }
  | { kind: 'analyze'; id: string; tool: ToolEvent }

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
  }
}

// groupBlocks routes blocks into render groups: TodoWrite is dropped (it drives the
// progress pill), AskUser/Analyze/Task each become their own card, and consecutive
// plain tool calls collapse into one execution group.
export function groupBlocks(blocks: Block[]): Group[] {
  const out: Group[] = []
  for (const b of blocks) {
    if (b.kind === 'tool') {
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
      // nested tool calls), so keep it standalone instead of folding it into the
      // compact exec list — otherwise its nested activity is dropped.
      if (b.tool.toolName === 'Task') {
        out.push({ kind: 'block', block: b })
        continue
      }
      const last = out[out.length - 1]
      if (last && last.kind === 'exec') last.tools.push(b.tool)
      else out.push({ kind: 'exec', id: b.id, tools: [b.tool] })
    } else {
      out.push({ kind: 'block', block: b })
    }
  }
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
