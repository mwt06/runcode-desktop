// Automation execution cards are a desktop projection. They are not Core tool
// messages: toolUseId is `automation:` + run id, and nothing here rewrites a
// Core seq or persists a fake tool into history.
import type { AutomationRun, ToolEvent } from '@/core/bridge'
import type { Block } from '@/chat/blocks'

export const AUTOMATION_TOOL_NAME = 'automation_task'
export const AUTOMATION_SUBMITTING_NOTE = '正在提交自动化任务'
export const AUTOMATION_HISTORY_NOTE = '正在执行，完整历史将在本次结束后加载'

const TERMINAL = new Set(['succeeded', 'failed', 'interrupted', 'timed_out', 'skipped'])
const ACTIVE = new Set(['queued', 'submitting', 'running', 'waiting_permission'])

export function automationToolUseId(runId: string): string {
  return 'automation:' + runId
}
export function automationNoteId(runId: string): string {
  return 'automation-note:' + runId
}
export function automationUserId(runId: string): string {
  return 'automation-user:' + runId
}
export function automationCardId(runId: string): string {
  return 'automation-card:' + runId
}
export function automationFailId(runId: string): string {
  return 'automation-fail:' + runId
}

export function isAutomationTerminal(status: string | undefined): boolean {
  return TERMINAL.has(status ?? '')
}

export function automationRunActive(status: string | undefined): boolean {
  return ACTIVE.has(status ?? '')
}

export function automationExecutionPlace(mode?: string): string {
  switch (mode) {
    case 'reuse_session':
      return '复用当前会话'
    case 'new_session_each_run':
      return '每次新建会话'
    default:
      return mode ?? ''
  }
}

function statusLabel(status: string): string {
  switch (status) {
    case 'succeeded': return '成功'
    case 'failed': return '失败'
    case 'interrupted': return '已中断'
    case 'timed_out': return '已超时'
    case 'skipped': return '已跳过'
    case 'queued': return '排队中'
    case 'submitting': return '提交中'
    case 'running': return '执行中'
    case 'waiting_permission': return '等待确认'
    default: return status || '未知'
  }
}

export function automationFailureText(run: AutomationRun): string {
  const why = (run.reason ?? '').trim()
  const lead = run.status === 'skipped'
    ? '自动化任务已跳过'
    : run.status === 'timed_out'
      ? '自动化任务已超时'
      : run.status === 'interrupted'
        ? '自动化任务已中断'
        : '自动化任务未能提交'
  return why ? `${lead}：${why}` : lead
}

// mergeAutomationRun keeps a terminal snapshot when an older or in-flight copy
// arrives later. A late receipt may fill questionId and prompt only.
export function mergeAutomationRun(prev: AutomationRun | undefined, next: AutomationRun): AutomationRun {
  if (!prev || prev.id !== next.id) return next
  const fill = (kept: AutomationRun): AutomationRun => ({
    ...kept,
    questionId: kept.questionId || next.questionId,
    prompt: kept.prompt || next.prompt,
    summary: kept.summary || next.summary,
    reason: kept.reason || next.reason,
    name: kept.name || next.name,
    sessionId: kept.sessionId || next.sessionId,
    workspace: kept.workspace || next.workspace,
    executionMode: kept.executionMode || next.executionMode,
    startedAtUTC: kept.startedAtUTC || next.startedAtUTC,
    finishedAtUTC: kept.finishedAtUTC || next.finishedAtUTC,
  })
  if (isAutomationTerminal(prev.status) && !isAutomationTerminal(next.status)) return fill(prev)
  if (prev.status === 'succeeded' && next.status !== 'succeeded') return fill(prev)
  if (next.runRevision < prev.runRevision) return fill(prev)
  return {
    ...prev,
    ...next,
    questionId: next.questionId || prev.questionId,
    prompt: next.prompt || prev.prompt,
    summary: next.summary || prev.summary,
    reason: next.reason || prev.reason,
    name: next.name || prev.name,
    sessionId: next.sessionId || prev.sessionId,
  }
}

export function mergeRunList(prev: AutomationRun[], incoming: AutomationRun[]): AutomationRun[] {
  const byId = new Map<string, AutomationRun>()
  for (const run of prev) byId.set(run.id, run)
  for (const run of incoming) byId.set(run.id, mergeAutomationRun(byId.get(run.id), run))
  return [...byId.values()]
}

export function automationRunToTool(run: AutomationRun): ToolEvent {
  const failed = isAutomationTerminal(run.status) && run.status !== 'succeeded'
  const type = run.status === 'succeeded' ? 'completed' : failed ? 'failed' : 'started'
  const lines: NonNullable<ToolEvent['output']> = [
    { stream: 'stdout', text: `状态：${statusLabel(run.status)}` },
  ]
  if (run.startedAtUTC) lines.push({ stream: 'stdout', text: `开始：${run.startedAtUTC}` })
  if (run.finishedAtUTC) lines.push({ stream: 'stdout', text: `结束：${run.finishedAtUTC}` })
  if (run.summary) lines.push({ stream: 'stdout', text: `结果：${run.summary}` })
  if (run.reason) lines.push({ stream: 'stdout', text: `原因：${run.reason}` })
  return {
    type,
    toolName: AUTOMATION_TOOL_NAME,
    toolUseID: automationToolUseId(run.id),
    message: failed ? statusLabel(run.status) : '',
    input: {
      name: run.name ?? '',
      scheduledAtUTC: run.scheduledAtUTC ?? '',
      workspace: run.workspace ?? '',
      executionMode: automationExecutionPlace(run.executionMode),
      prompt: run.prompt ?? '',
    },
    output: lines,
  }
}

export function ensureSubmittingNote(blocks: Block[], runId: string): Block[] {
  if (!runId) return blocks
  const noteId = automationNoteId(runId)
  const userId = automationUserId(runId)
  const toolId = automationToolUseId(runId)
  for (const b of blocks) {
    if (b.kind === 'notice' && (b.id === noteId || b.id === automationFailId(runId))) return blocks
    if (b.kind === 'user' && b.id === userId) return blocks
    if (b.kind === 'tool' && b.tool.toolUseID === toolId) return blocks
  }
  return [...blocks, { kind: 'notice', id: noteId, text: AUTOMATION_SUBMITTING_NOTE }]
}

function ordered(runs: AutomationRun[]): AutomationRun[] {
  return [...runs].sort((a, b) => (a.scheduledAtUTC || '').localeCompare(b.scheduledAtUTC || '') || a.id.localeCompare(b.id))
}

function placeAutomationRun(
  blocks: Block[],
  run: AutomationRun,
  owned: boolean,
  claimedUsers?: Set<string>,
  matchPrompt = false,
): Block[] {
  const toolId = automationToolUseId(run.id)
  const noteId = automationNoteId(run.id)
  const userId = automationUserId(run.id)
  const failId = automationFailId(run.id)
  if (!run.questionId) {
    if (!isAutomationTerminal(run.status)) return blocks
    let changed = false
    const next = blocks.flatMap((b): Block[] => {
      if (b.kind === 'notice' && b.id === noteId) {
        changed = true
        return [{ kind: 'notice', id: failId, text: automationFailureText(run) }]
      }
      if ((b.kind === 'tool' && b.tool.toolUseID === toolId) || (b.kind === 'user' && b.id === userId)) {
        changed = true
        return []
      }
      return [b]
    })
    return changed ? next : blocks
  }
  const card: Block = {
    kind: 'tool',
    id: automationCardId(run.id),
    sessionId: run.sessionId,
    tool: automationRunToTool(run),
  }
  const stripped = blocks.filter((b) => !(b.kind === 'tool' && b.tool.toolUseID === toolId) && !(b.kind === 'notice' && b.id === failId))
  const user: Block = { kind: 'user', id: userId, questionId: run.questionId, text: run.prompt ?? '', ts: '' }
  const withoutNote = stripped.filter((b) => !(b.kind === 'notice' && b.id === noteId))
  let idx = withoutNote.findIndex((b) =>
    b.kind === 'user'
    && (b.questionId === run.questionId || b.id === userId)
    && (!claimedUsers || !claimedUsers.has(b.id)),
  )
  if (idx < 0 && matchPrompt && !!run.prompt) {
    idx = withoutNote.findIndex((b) =>
      b.kind === 'user'
      && b.text === run.prompt
      && (!claimedUsers || !claimedUsers.has(b.id)),
    )
  }
  if (idx >= 0) {
    const matched = withoutNote[idx]
    if (matched.kind === 'user') claimedUsers?.add(matched.id)
    const copy = withoutNote.slice()
    copy.splice(idx + 1, 0, card)
    return copy
  }
  const noteIdx = stripped.findIndex((b) => b.kind === 'notice' && b.id === noteId)
  if (noteIdx >= 0) {
    const copy = stripped.slice()
    copy.splice(noteIdx, 1, user, card)
    return copy
  }
  if (!owned) return blocks
  claimedUsers?.add(userId)
  return [...stripped, user, card]
}

// projectAutomationRuns inserts a summary card after the user question that owns
// the run. A run with no questionId, and a run whose question is not in these
// blocks, is not guessed onto some other question. Passing the same runs twice
// does not duplicate the card.
export function projectAutomationRuns(blocks: Block[], runs: AutomationRun[]): Block[] {
  return ordered(runs).reduce((acc, run) => placeAutomationRun(acc, run, false), blocks)
}

// projectSessionAutomationRuns is the same placement for runs already known to
// belong to this session. A receipt that arrives before any stream is appended
// once; it is not matched to an unrelated earlier question.
export function projectSessionAutomationRuns(blocks: Block[], runs: AutomationRun[]): Block[] {
  const claimedUsers = new Set<string>()
  return ordered(runs).reduce((acc, run) => placeAutomationRun(acc, run, true, claimedUsers, true), blocks)
}

export function clearAutomationProjection(blocks: Block[]): Block[] {
  return blocks.filter((b) => {
    if (b.kind === 'notice' && (b.id.startsWith('automation-note:') || b.id.startsWith('automation-fail:') || b.id.startsWith('automation-history:'))) return false
    if (b.kind === 'user' && b.id.startsWith('automation-user:')) return false
    if (b.kind === 'tool' && (b.tool.toolName === AUTOMATION_TOOL_NAME || (b.tool.toolUseID ?? '').startsWith('automation:'))) return false
    return true
  })
}
