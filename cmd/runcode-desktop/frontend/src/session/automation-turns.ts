// reconcileAutomationTurn groups a leased run's live stream separately from the
// previous turn, then fills the real user question and summary card once the
// receipt exists. It does not rewrite Core seq.
import type { AutomationContext, AutomationRun, AutomationRunPage } from '@/core/bridge'
import type { Block } from '@/chat/blocks'
import { endTool, finalizeStreaming, mergeTool } from '@/chat/blocks'
import {
  automationRunActive,
  clearAutomationProjection,
  ensureSubmittingNote,
  isAutomationTerminal,
  mergeAutomationRun,
  mergeRunList,
  projectSessionAutomationRuns,
} from '@/chat/automation-run'

export interface AutomationTurnState {
  context: AutomationContext | null
  sessionGeneration: number
  sessionId: string
  blocks: Block[]
  runs: AutomationRun[]
  busy: boolean
  seq: number
}

export type AutomationEnvelope = {
  event: string
  sessionId?: string
  seq?: number
  payload?: { text?: string; type?: string; toolName?: string; toolUseID?: string; message?: string; input?: unknown }
  automationSource?: { context: AutomationContext; runId: string; leaseGeneration: number }
}

export type AutomationTurnInput =
  | { kind: 'bind'; context: AutomationContext; sessionGeneration: number; sessionId: string; blocks: Block[] }
  | { kind: 'context'; context: AutomationContext; sessionGeneration: number }
  | { kind: 'envelope'; sessionGeneration: number; envelope: AutomationEnvelope }
  | { kind: 'run'; context: AutomationContext; sessionGeneration: number; run: AutomationRun }
  | { kind: 'runs'; context: AutomationContext; sessionGeneration: number; sessionId: string; runs: AutomationRun[] }

type Internals = {
  live: string[]
  settled: string[]
  bySession: Record<string, AutomationRun[]>
}

const internals = new WeakMap<AutomationTurnState, Internals>()

function bag(state: AutomationTurnState): Internals {
  return internals.get(state) ?? { live: [], settled: [], bySession: {} }
}

function finish(state: AutomationTurnState, extra: Internals): AutomationTurnState {
  const sessionRuns = extra.bySession[state.sessionId] ?? []
  const next: AutomationTurnState = {
    ...state,
    runs: sessionRuns,
    blocks: projectSessionAutomationRuns(state.blocks, sessionRuns),
    busy: computeBusy(sessionRuns, extra.live, extra.settled),
  }
  internals.set(next, extra)
  return next
}

function computeBusy(runs: AutomationRun[], live: string[], settled: string[]): boolean {
  const done = new Set(settled)
  if (live.some((id) => !done.has(id))) return true
  return runs.some((run) => automationRunActive(run.status) && !done.has(run.id))
}

export function sameAutomationContext(a: AutomationContext | null | undefined, b: AutomationContext | null | undefined): boolean {
  return !!a && !!b && a.viewId === b.viewId && a.epoch === b.epoch
}

export function emptyAutomationTurn(sessionId = ''): AutomationTurnState {
  const state: AutomationTurnState = {
    context: null,
    sessionGeneration: 0,
    sessionId,
    blocks: [],
    runs: [],
    busy: false,
    seq: 0,
  }
  internals.set(state, { live: [], settled: [], bySession: {} })
  return state
}

function guard(state: AutomationTurnState, sessionGeneration: number, context: AutomationContext | null | undefined): boolean {
  if (sessionGeneration !== state.sessionGeneration) return false
  if (!sameAutomationContext(state.context, context)) return false
  return true
}

function runAnchor(blocks: Block[], runId: string): number {
  let idx = -1
  const note = 'automation-note:' + runId
  const fail = 'automation-fail:' + runId
  const user = 'automation-user:' + runId
  const tool = 'automation:' + runId
  blocks.forEach((b, i) => {
    if (b.kind === 'notice' && (b.id === note || b.id === fail)) idx = i
    if (b.kind === 'user' && b.id === user) idx = i
    if (b.kind === 'tool' && b.tool.toolUseID === tool) idx = i
  })
  return idx
}

function appendText(blocks: Block[], runId: string, text: string, thinking: boolean, seq: () => string): Block[] {
  const withNote = ensureSubmittingNote(blocks, runId)
  const anchor = runAnchor(withNote, runId)
  for (let i = withNote.length - 1; i > anchor; i--) {
    const b = withNote[i]
    if (b.kind === 'user') break
    if (b.kind === 'assistant' && b.streaming) {
      const copy = withNote.slice()
      copy[i] = thinking
        ? { ...b, thinking: (b.thinking ?? '') + text }
        : { ...b, text: b.text + text }
      return copy
    }
  }
  return [...withNote, {
    kind: 'assistant',
    id: seq(),
    text: thinking ? '' : text,
    thinking: thinking ? text : undefined,
    streaming: true,
    ts: '',
  }]
}

function applyEnvelope(state: AutomationTurnState, env: AutomationEnvelope, extra: Internals): AutomationTurnState {
  const src = env.automationSource
  if (!src?.runId) return state
  if (!guard(state, state.sessionGeneration, src.context)) return state
  if (env.sessionId && state.sessionId && env.sessionId !== state.sessionId) return state
  const runId = src.runId
  let blocks = state.blocks
  if (env.event === 'turn:end' || env.event === 'turn:error') {
    extra = {
      ...extra,
      live: extra.live.filter((id) => id !== runId),
      settled: extra.settled.includes(runId) ? extra.settled : [...extra.settled, runId],
    }
    blocks = blocks.map((b) => {
      if (b.kind === 'assistant' && b.streaming) return { ...b, streaming: false }
      if (b.kind === 'tool' && b.tool.toolName !== 'automation_task') return { ...b, tool: endTool(b.tool) }
      return b
    })
    return finish({ ...state, blocks }, extra)
  }
  if (!extra.settled.includes(runId) && !extra.live.includes(runId)) {
    extra = { ...extra, live: [...extra.live, runId] }
  }
  const nextSeq = () => {
    state = { ...state, seq: state.seq + 1 }
    return 'auto-' + runId + '-' + state.seq
  }
  if (env.event === 'assistant:delta') {
    blocks = appendText(blocks, runId, env.payload?.text ?? '', false, nextSeq)
  } else if (env.event === 'assistant:thinking') {
    blocks = appendText(blocks, runId, env.payload?.text ?? '', true, nextSeq)
  } else if (env.event === 'tool:event' && env.payload) {
    const ev = env.payload as import('@/core/bridge').ToolEvent
    blocks = finalizeStreaming(ensureSubmittingNote(blocks, runId))
    const idx = blocks.findIndex((b) => b.kind === 'tool' && b.tool.toolUseID && b.tool.toolUseID === ev.toolUseID)
    if (idx >= 0) {
      const existing = blocks[idx]
      if (existing.kind === 'tool') {
        const copy = blocks.slice()
        copy[idx] = { ...existing, tool: mergeTool(existing.tool, ev) }
        blocks = copy
      }
    } else {
      blocks = [...blocks, { kind: 'tool', id: nextSeq(), tool: ev, sessionId: state.sessionId }]
    }
  }
  return finish({ ...state, blocks }, extra)
}

export function reconcileAutomationTurn(state: AutomationTurnState, input: AutomationTurnInput): AutomationTurnState {
  const extra = bag(state)
  if (input.kind === 'context') {
    const changed = !sameAutomationContext(state.context, input.context)
    const cleared: Internals = changed
      ? { live: [], settled: [], bySession: {} }
      : extra
    const next = finish({
      ...state,
      context: input.context,
      sessionGeneration: input.sessionGeneration,
      blocks: changed ? clearAutomationProjection(state.blocks) : state.blocks,
      runs: changed ? [] : state.runs,
    }, cleared)
    return next
  }
  if (input.kind === 'bind') {
    const changed = state.context != null && !sameAutomationContext(state.context, input.context)
    const base = changed ? { live: [], settled: [], bySession: {} as Record<string, AutomationRun[]> } : extra
    const blocks = changed ? clearAutomationProjection(input.blocks) : input.blocks
    return finish({
      ...state,
      context: input.context,
      sessionGeneration: input.sessionGeneration,
      sessionId: input.sessionId,
      blocks,
    }, base)
  }
  if (input.kind === 'envelope') {
    if (input.sessionGeneration !== state.sessionGeneration) return state
    return applyEnvelope(state, input.envelope, extra)
  }
  if (!guard(state, input.sessionGeneration, input.context)) return state
  const incoming = input.kind === 'run' ? [input.run] : input.runs
  const sessionId = input.kind === 'runs' ? input.sessionId : (incoming[0]?.sessionId || state.sessionId)
  if (sessionId && sessionId !== state.sessionId) return state
  let settled = extra.settled
  for (const run of incoming) {
    const merged = mergeAutomationRun((extra.bySession[state.sessionId] ?? []).find((r) => r.id === run.id), run)
    if (isAutomationTerminal(merged.status) && !settled.includes(run.id)) settled = [...settled, run.id]
  }
  const bySession = {
    ...extra.bySession,
    [state.sessionId]: mergeRunList(extra.bySession[state.sessionId] ?? [], incoming.map((run) => mergeAutomationRun((extra.bySession[state.sessionId] ?? []).find((r) => r.id === run.id), run))),
  }
  return finish({ ...state, blocks: state.blocks }, { ...extra, bySession, settled })
}

export async function loadSessionAutomationRuns(
  fetchPage: (cursor: string) => Promise<AutomationRunPage>,
  accept: () => boolean,
): Promise<AutomationRun[] | null> {
  const all: AutomationRun[] = []
  const seen = new Set<string>()
  let cursor = ''
  for (let pageNo = 0; pageNo < 1000; pageNo++) {
    if (!accept()) return null
    const page = await fetchPage(cursor)
    if (!accept()) return null
    for (const run of page.items ?? []) {
      if (run?.id && !seen.has(run.id)) {
        seen.add(run.id)
        all.push(run)
      }
    }
    if (!page.nextCursor || page.nextCursor === cursor) return all
    cursor = page.nextCursor
  }
  return all
}

export function mergeResumedBlocks(live: Block[], resumed: Block[]): Block[] {
  const keyOf = (b: Block): string | null => {
    if (b.kind === 'user' && b.questionId) return 'q:' + b.questionId
    if (b.kind === 'tool' && b.tool.toolUseID) return 't:' + b.tool.toolUseID
    if (b.kind === 'assistant' && b.text) return 'a:' + b.text
    return null
  }
  const relatedAssistant = (a: Extract<Block, { kind: 'assistant' }>, b: Extract<Block, { kind: 'assistant' }>): boolean =>
    !!a.text && !!b.text && (a.text === b.text || a.text.startsWith(b.text) || b.text.startsWith(a.text))
  const used = new Set<number>()
  const out: Block[] = []
  for (const b of resumed) {
    if (b.kind === 'assistant') {
      const idx = live.findIndex((item, index) =>
        !used.has(index) && item.kind === 'assistant' && relatedAssistant(item, b))
      if (idx >= 0) {
        const streamed = live[idx] as Extract<Block, { kind: 'assistant' }>
        used.add(idx)
        const streamedTextIsRicher = streamed.text.length >= b.text.length
        const streamedThinkingIsRicher = (streamed.thinking?.length ?? 0) >= (b.thinking?.length ?? 0)
        out.push({
          ...b,
          ...streamed,
          id: streamed.id || b.id,
          text: streamedTextIsRicher ? streamed.text : b.text,
          thinking: streamedThinkingIsRicher ? streamed.thinking : b.thinking,
          streaming: false,
        })
      } else {
        out.push(b)
      }
      continue
    }
    const key = keyOf(b)
    if (key) {
      const idx = live.findIndex((item, index) => !used.has(index) && keyOf(item) === key)
      if (idx >= 0) {
        out.push(live[idx])
        used.add(idx)
        continue
      }
    }
    out.push(b)
  }
  for (let i = 0; i < live.length; i++) {
    if (used.has(i)) continue
    const b = live[i]
    if (b.kind === 'assistant' && resumed.some((r) => r.kind === 'assistant' && relatedAssistant(b, r))) continue
    out.push(b)
  }
  return out
}
