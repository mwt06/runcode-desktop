import { describe, expect, it } from 'vitest'
import type { AutomationRun } from '@/core/bridge'
import type { Block } from '@/chat/blocks'
import {
  AUTOMATION_SUBMITTING_NOTE,
  automationRunToTool,
  automationToolUseId,
  clearAutomationProjection,
  ensureSubmittingNote,
  mergeAutomationRun,
  projectAutomationRuns,
  projectSessionAutomationRuns,
} from './automation-run'

const ctx = { viewId: 'view-1', epoch: 1 }

function user(id: string, questionId: string, text: string): Block {
  return { kind: 'user', id, questionId, text, ts: '' }
}
function assistant(id: string, text: string): Block {
  return { kind: 'assistant', id, text, streaming: false, ts: '' }
}
function run(partial: Partial<AutomationRun> & { id: string }): AutomationRun {
  return {
    taskId: 'task-1',
    taskRevision: 1,
    runRevision: 1,
    status: 'running',
    scheduledAtUTC: '2026-11-18T01:00:00Z',
    schedule: { mode: 'once', timeZone: 'Asia/Shanghai', startDate: '2026-11-18', timeOfDay: '09:00:00' },
    name: '每日整理记录',
    prompt: '整理今天的记录',
    workspace: 'D:\\notes',
    executionMode: 'reuse_session',
    sessionId: 'sess-a',
    ...partial,
  }
}
function cards(blocks: Block[]): Block[] {
  return blocks.filter((b) => b.kind === 'tool' && b.tool.toolUseID?.startsWith('automation:'))
}
function users(blocks: Block[]): Extract<Block, { kind: 'user' }>[] {
  return blocks.filter((b): b is Extract<Block, { kind: 'user' }> => b.kind === 'user')
}

describe('projectAutomationRuns', () => {
  it('places each run of a reused session after its own question', () => {
    const blocks = [
      user('u1', 'q1', '第一问'),
      assistant('a1', '第一答'),
      user('u2', 'q2', '第二问'),
      assistant('a2', '第二答'),
    ]
    const out = projectAutomationRuns(blocks, [
      run({ id: 'r2', questionId: 'q2', scheduledAtUTC: '2026-11-18T02:00:00Z' }),
      run({ id: 'r1', questionId: 'q1', scheduledAtUTC: '2026-11-18T01:00:00Z' }),
    ])
    expect(out.map((b) => b.kind === 'tool' ? b.tool.toolUseID : b.kind === 'user' ? b.questionId : b.kind)).toEqual([
      'q1', 'automation:r1', 'assistant', 'q2', 'automation:r2', 'assistant',
    ])
    expect(cards(out)).toHaveLength(2)
  })

  it('pairs repeated session prompts in order when question ids differ', () => {
    const blocks = [
      user('u1', 'history-q1', 'say hello'),
      assistant('a1', '第一答'),
      user('u2', 'history-q2', 'say hello'),
      assistant('a2', '第二答'),
    ]
    const out = projectSessionAutomationRuns(blocks, [
      run({ id: 'r1', questionId: 'old-q1', prompt: 'say hello', scheduledAtUTC: '2026-11-18T01:00:00Z' }),
      run({ id: 'r2', questionId: 'old-q2', prompt: 'say hello', scheduledAtUTC: '2026-11-18T02:00:00Z' }),
    ])
    expect(out.map((b) => b.kind === 'tool' ? b.tool.toolUseID : b.kind === 'user' ? b.questionId : b.kind)).toEqual([
      'history-q1', 'automation:r1', 'assistant', 'history-q2', 'automation:r2', 'assistant',
    ])
    expect(users(out).map((b) => b.id)).toEqual(['u1', 'u2'])
  })

  it('does not duplicate a card when a lookup follows the event', () => {
    const blocks = ensureSubmittingNote([user('u1', 'q1', '第一问'), assistant('a1', '已有')], 'r1')
    const once = projectAutomationRuns(blocks, [run({ id: 'r1', questionId: 'q1' })])
    const twice = projectAutomationRuns(once, [run({ id: 'r1', questionId: 'q1', summary: '完成' })])
    expect(cards(twice)).toHaveLength(1)
    expect(users(twice).filter((b) => b.questionId === 'q1')).toHaveLength(1)
    expect(twice.some((b) => b.kind === 'notice' && b.text === AUTOMATION_SUBMITTING_NOTE)).toBe(false)
  })

  it('does not guess a card onto another question when the run has no question id', () => {
    const blocks = [user('u1', 'q1', '第一问'), assistant('a1', '第一答')]
    const out = projectAutomationRuns(blocks, [run({ id: 'r1', questionId: '', status: 'failed', reason: '目录不存在' })])
    expect(out).toEqual(blocks)
    expect(cards(out)).toHaveLength(0)
  })

  it('does not paint another session’s run into these blocks', () => {
    const blocks = [user('u-b', 'q-b', '别的会话'), assistant('a-b', '别的回答')]
    const out = projectAutomationRuns(blocks, [run({ id: 'r-a', questionId: 'q-a', sessionId: 'sess-a' })])
    expect(cards(out)).toHaveLength(0)
    expect(users(out).map((b) => b.questionId)).toEqual(['q-b'])
  })

  it('clears the projection when the context changes', () => {
    const blocks = projectAutomationRuns(
      ensureSubmittingNote([user('u1', 'q1', '第一问')], 'r1'),
      [run({ id: 'r1', questionId: 'q1' })],
    )
    expect(cards(blocks)).toHaveLength(1)
    const cleared = clearAutomationProjection(blocks)
    expect(cards(cleared)).toHaveLength(0)
    expect(users(cleared).map((b) => b.id)).toEqual(['u1'])
    expect(cleared.some((b) => b.kind === 'notice')).toBe(false)
    expect(ctx.epoch).toBe(1)
  })
})

describe('mergeAutomationRun', () => {
  it('does not regress a terminal snapshot when a running copy arrives later', () => {
    const done = run({ id: 'r1', status: 'succeeded', runRevision: 4, questionId: '' })
    const late = mergeAutomationRun(done, run({ id: 'r1', status: 'running', runRevision: 5, questionId: 'q-late' }))
    expect(late.status).toBe('succeeded')
    expect(late.questionId).toBe('q-late')
    expect(automationRunToTool(late).type).toBe('completed')
    expect(automationRunToTool(run({ id: 'r1', status: 'failed' })).type).toBe('failed')
    expect(automationRunToTool(run({ id: 'r1', status: 'interrupted' })).type).toBe('failed')
    expect(automationRunToTool(run({ id: 'r1', status: 'timed_out' })).type).toBe('failed')
    expect(automationRunToTool(run({ id: 'r1', status: 'running' })).type).not.toBe('completed')
    expect(automationToolUseId('r1')).toBe('automation:r1')
  })
})
