import { describe, expect, it } from 'vitest'
import type { AutomationRun } from '@/core/bridge'
import type { Block } from '@/chat/blocks'
import { AUTOMATION_SUBMITTING_NOTE } from '@/chat/automation-run'
import {
  emptyAutomationTurn,
  loadSessionAutomationRuns,
  mergeResumedBlocks,
  reconcileAutomationTurn,
  type AutomationEnvelope,
} from './automation-turns'

const ctx = { viewId: 'view-1', epoch: 1 }

function user(id: string, questionId: string, text: string): Block {
  return { kind: 'user', id, questionId, text, ts: '' }
}
function assistant(id: string, text: string, streaming = false): Block {
  return { kind: 'assistant', id, text, streaming, ts: '' }
}
function run(partial: Partial<AutomationRun> & { id: string }): AutomationRun {
  return {
    taskId: 'task-1',
    taskRevision: 1,
    runRevision: 2,
    status: 'running',
    scheduledAtUTC: '2026-11-18T01:00:00Z',
    schedule: { mode: 'once', timeZone: 'Asia/Shanghai', startDate: '2026-11-18', timeOfDay: '09:00:00' },
    name: '每日整理记录',
    prompt: '整理今天的记录',
    workspace: 'D:\\notes',
    executionMode: 'reuse_session',
    sessionId: 'sess-1',
    ...partial,
  }
}
function env(partial: Partial<AutomationEnvelope> & { event: string }): AutomationEnvelope {
  return {
    sessionId: 'sess-1',
    seq: 10,
    payload: { text: '' },
    automationSource: { context: ctx, runId: 'run-1', leaseGeneration: 3 },
    ...partial,
  }
}
function texts(blocks: Block[]): string[] {
  return blocks.filter((b) => b.kind === 'assistant').map((b) => b.text)
}
function cardCount(blocks: Block[]): number {
  return blocks.filter((b) => b.kind === 'tool' && b.tool.toolName === 'automation_task').length
}

describe('reconcileAutomationTurn', () => {
  it('keeps one user, one card, and the whole stream when the receipt lands after the first delta', () => {
    const prior = [user('u0', 'q0', '上一问'), assistant('a0', '上一轮')]
    let state = reconcileAutomationTurn(emptyAutomationTurn('sess-1'), {
      kind: 'bind', context: ctx, sessionGeneration: 1, sessionId: 'sess-1', blocks: prior,
    })
    const first = env({ event: 'assistant:delta', seq: 10, payload: { text: '开始' } })
    state = reconcileAutomationTurn(state, { kind: 'envelope', sessionGeneration: 1, envelope: first })
    expect(first.seq).toBe(10)
    expect(texts(state.blocks)).toEqual(['上一轮', '开始'])
    expect(state.blocks.some((b) => b.kind === 'notice' && b.text === AUTOMATION_SUBMITTING_NOTE)).toBe(true)
    expect(state.blocks.filter((b) => b.kind === 'user')).toHaveLength(1)
    expect(state.busy).toBe(true)

    state = reconcileAutomationTurn(state, {
      kind: 'envelope', sessionGeneration: 1,
      envelope: env({ event: 'assistant:delta', seq: 11, payload: { text: '整理' } }),
    })
    state = reconcileAutomationTurn(state, {
      kind: 'envelope', sessionGeneration: 1,
      envelope: env({ event: 'tool:event', seq: 12, payload: { type: 'started', toolName: 'Bash', toolUseID: 'bash-1', input: { command: 'dir' } } }),
    })
    expect(texts(state.blocks)).toEqual(['上一轮', '开始整理'])

    const receipt = run({ id: 'run-1', questionId: 'q-new', prompt: '整理今天的记录', status: 'running' })
    state = reconcileAutomationTurn(state, { kind: 'run', context: ctx, sessionGeneration: 1, run: receipt })
    state = reconcileAutomationTurn(state, {
      kind: 'runs', context: ctx, sessionGeneration: 1, sessionId: 'sess-1', runs: [receipt],
    })
    const owned = state.blocks.filter((b) => b.kind === 'user' && b.questionId === 'q-new')
    expect(owned).toHaveLength(1)
    expect(cardCount(state.blocks)).toBe(1)
    expect(texts(state.blocks)).toEqual(['上一轮', '开始整理'])
    expect(state.blocks.some((b) => b.kind === 'tool' && b.tool.toolUseID === 'bash-1')).toBe(true)
    expect(state.busy).toBe(true)
    expect(state.blocks.some((b) => b.kind === 'notice' && b.text === AUTOMATION_SUBMITTING_NOTE)).toBe(false)
    const cardAt = state.blocks.findIndex((b) => b.kind === 'tool' && b.tool.toolName === 'automation_task')
    const streamAt = state.blocks.findIndex((b) => b.kind === 'assistant' && b.text === '开始整理')
    expect(cardAt).toBeGreaterThan(-1)
    expect(streamAt).toBeGreaterThan(cardAt)

    state = reconcileAutomationTurn(state, {
      kind: 'run', context: ctx, sessionGeneration: 1,
      run: run({ id: 'run-1', questionId: 'q-new', status: 'succeeded', runRevision: 4, summary: '好了' }),
    })
    expect(state.busy).toBe(false)
    expect(state.runs[0]?.status).toBe('succeeded')
    expect(cardCount(state.blocks)).toBe(1)
    const card = state.blocks.find((b): b is Extract<Block, { kind: 'tool' }> => b.kind === 'tool' && b.tool.toolName === 'automation_task')
    expect(card?.tool.type).toBe('completed')

    state = reconcileAutomationTurn(state, {
      kind: 'run', context: ctx, sessionGeneration: 1,
      run: run({ id: 'run-1', questionId: 'q-new', status: 'running', runRevision: 3 }),
    })
    expect(state.busy).toBe(false)
    expect(state.runs[0]?.status).toBe('succeeded')
    expect(state.runs[0]?.questionId).toBe('q-new')
  })

  it('does not invent an accepted user when submission fails before a receipt', () => {
    let state = reconcileAutomationTurn(emptyAutomationTurn('sess-1'), {
      kind: 'bind', context: ctx, sessionGeneration: 1, sessionId: 'sess-1', blocks: [assistant('a0', '上一轮')],
    })
    state = reconcileAutomationTurn(state, {
      kind: 'envelope', sessionGeneration: 1,
      envelope: env({ event: 'assistant:delta', payload: { text: '半句' } }),
    })
    state = reconcileAutomationTurn(state, {
      kind: 'run', context: ctx, sessionGeneration: 1,
      run: run({ id: 'run-1', questionId: '', status: 'failed', reason: '目录不存在', prompt: '' }),
    })
    expect(state.blocks.filter((b) => b.kind === 'user')).toHaveLength(0)
    expect(cardCount(state.blocks)).toBe(0)
    expect(state.blocks.some((b) => b.kind === 'notice' && b.text.includes('未能提交'))).toBe(true)
    expect(state.busy).toBe(false)
  })

  it('drops a stale generation and a foreign session, and clears the projection on context change', () => {
    let state = reconcileAutomationTurn(emptyAutomationTurn('sess-1'), {
      kind: 'bind', context: ctx, sessionGeneration: 2, sessionId: 'sess-1',
      blocks: [user('u1', 'q1', '问'), assistant('a1', '答')],
    })
    state = reconcileAutomationTurn(state, {
      kind: 'run', context: ctx, sessionGeneration: 2,
      run: run({ id: 'run-1', questionId: 'q1' }),
    })
    expect(cardCount(state.blocks)).toBe(1)
    const stale = reconcileAutomationTurn(state, {
      kind: 'runs', context: ctx, sessionGeneration: 1, sessionId: 'sess-1',
      runs: [run({ id: 'run-9', questionId: 'q9', prompt: '不该出现' })],
    })
    expect(stale).toBe(state)
    const other = reconcileAutomationTurn(state, {
      kind: 'bind', context: ctx, sessionGeneration: 3, sessionId: 'sess-2',
      blocks: [user('u2', 'q2', '另一会话')],
    })
    expect(cardCount(other.blocks)).toBe(0)
    expect(other.blocks.filter((b) => b.kind === 'user').map((b) => b.questionId)).toEqual(['q2'])
    const back = reconcileAutomationTurn(other, {
      kind: 'bind', context: ctx, sessionGeneration: 4, sessionId: 'sess-1', blocks: state.blocks,
    })
    expect(cardCount(back.blocks)).toBe(1)
    const cleared = reconcileAutomationTurn(back, {
      kind: 'context', context: { viewId: 'view-1', epoch: 2 }, sessionGeneration: 4,
    })
    expect(cardCount(cleared.blocks)).toBe(0)
    expect(cleared.runs).toEqual([])
    expect(texts(cleared.blocks)).toContain('答')
  })
})

describe('loadSessionAutomationRuns', () => {
  it('walks every page and stops when the generation guard fails', async () => {
    const pages = [
      { context: ctx, items: [run({ id: 'r1' })], nextCursor: 'c2' },
      { context: ctx, items: [run({ id: 'r2' }), run({ id: 'r1' })], nextCursor: '' },
    ]
    let i = 0
    const all = await loadSessionAutomationRuns(async () => pages[i++]!, () => true)
    expect(all?.map((r) => r.id)).toEqual(['r1', 'r2'])
    let calls = 0
    const dropped = await loadSessionAutomationRuns(async () => {
      calls++
      return { context: ctx, items: [run({ id: 'late' })], nextCursor: 'again' }
    }, () => calls === 0)
    expect(dropped).toBeNull()
  })
})

describe('mergeResumedBlocks', () => {
  it('keeps the live stream and fills only the missing history', () => {
    const live = [assistant('live', '开始整理', true)]
    const resumed = [user('u', 'q-new', '整理今天的记录'), assistant('old', '开始')]
    const merged = mergeResumedBlocks(live, resumed)
    expect(merged.some((b) => b.kind === 'user' && b.questionId === 'q-new')).toBe(true)
    expect(merged.some((b) => b.kind === 'assistant' && b.text === '开始整理')).toBe(true)
    expect(merged.filter((b) => b.kind === 'assistant')).toHaveLength(1)
  })

  it('keeps each live answer in its original question position', () => {
    const live = [
      assistant('live-a1', '第一答'),
      assistant('live-a2', '第二答'),
    ]
    const resumed = [
      user('u1', 'q1', '第一问'),
      assistant('a1', '第一答'),
      user('u2', 'q2', '第二问'),
      assistant('a2', '第二答'),
    ]
    const merged = mergeResumedBlocks(live, resumed)
    expect(merged.map((b) => b.kind === 'user' ? b.questionId : b.kind === 'assistant' ? b.text : b.kind)).toEqual([
      'q1', '第一答', 'q2', '第二答',
    ])
  })
})
