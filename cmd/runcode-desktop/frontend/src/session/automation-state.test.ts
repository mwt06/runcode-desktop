import { describe, expect, it } from 'vitest'
import type { AutomationContext, AutomationTask, AutomationTaskPage } from '@/core/bridge'
import {
  emptyAutomationState,
  reduceAutomation,
  type AutomationState,
} from './automation-state'

function ctx(epoch: number, viewId = 'view'): AutomationContext {
  return { viewId, epoch }
}

function task(id: string, revision: number, extra: Partial<AutomationTask> = {}): AutomationTask {
  return {
    id,
    revision,
    name: extra.name ?? id,
    prompt: extra.prompt ?? '',
    workspace: extra.workspace ?? 'D:\\资料',
    executionMode: extra.executionMode ?? 'reuse_session',
    schedule: extra.schedule ?? {
      mode: 'daily', timeZone: 'Asia/Shanghai', startDate: '2026-11-18', timeOfDay: '09:00:00', interval: 1,
    },
    state: extra.state ?? 'enabled',
    nextRunAtUTC: extra.nextRunAtUTC,
    blockedReason: extra.blockedReason,
    lastRun: extra.lastRun,
    activeRunId: extra.activeRunId,
    lastLinkedRunId: extra.lastLinkedRunId,
    linkedSessionTitle: extra.linkedSessionTitle,
    deletedAtUtc: extra.deletedAtUtc,
    binding: extra.binding ?? { modelKind: 'passport', modelName: 'model-a', bindingVersion: 1 },
  }
}

function page(context: AutomationContext, items: AutomationTask[], nextCursor?: string): AutomationTaskPage {
  return { context, items, nextCursor }
}

function snap(state: AutomationState, context: AutomationContext, generation: number, items: AutomationTask[], nextCursor?: string) {
  return reduceAutomation(state, { type: 'snapshot', context, generation, page: page(context, items, nextCursor) })
}

describe('automation state', () => {
  it('keeps an event that arrives before the snapshot, and the higher revision wins', () => {
    let state = emptyAutomationState()
    state = reduceAutomation(state, { type: 'event', context: ctx(1), task: task('a', 3, { name: '来自事件' }) })
    expect(state.tasks.a).toBeUndefined()
    state = reduceAutomation(state, { type: 'bind', context: ctx(1) })
    state = snap(state, ctx(1), 0, [task('a', 1, { name: '来自快照' })])
    expect(state.tasks.a?.revision).toBe(3)
    expect(state.tasks.a?.name).toBe('来自事件')
    expect(state.list.ids).toEqual(['a'])
  })

  it('merges a higher revision in the same context and ignores an older one', () => {
    let state = reduceAutomation(emptyAutomationState(), { type: 'bind', context: ctx(1) })
    state = snap(state, ctx(1), 0, [task('a', 2, { name: '第二版' })])
    state = reduceAutomation(state, { type: 'event', context: ctx(1), task: task('a', 4, { name: '第四版' }) })
    state = reduceAutomation(state, { type: 'event', context: ctx(1), task: task('a', 3, { name: '第三版' }) })
    expect(state.tasks.a?.revision).toBe(4)
    expect(state.tasks.a?.name).toBe('第四版')
  })

  it('accepts a same-revision task snapshot when its run revision advanced', () => {
    let state = reduceAutomation(emptyAutomationState(), { type: 'bind', context: ctx(1) })
    state = snap(state, ctx(1), 0, [task('a', 2, {
      lastRun: {
        id: 'run-a', taskId: 'a', taskRevision: 2, runRevision: 1, status: 'queued',
        scheduledAtUTC: '2026-11-19T01:00:00Z', schedule: task('seed', 1).schedule,
      },
    })])
    state = reduceAutomation(state, {
      type: 'event',
      context: ctx(1),
      task: task('a', 2, {
        lastRun: {
          id: 'run-a', taskId: 'a', taskRevision: 2, runRevision: 2, status: 'running',
          scheduledAtUTC: '2026-11-19T01:00:00Z', schedule: task('seed', 1).schedule,
        },
      }),
    })
    expect(state.tasks.a?.lastRun?.runRevision).toBe(2)
    expect(state.tasks.a?.lastRun?.status).toBe('running')
  })

  it('accepts a newer run even when its run revision restarts at one', () => {
    let state = reduceAutomation(emptyAutomationState(), { type: 'bind', context: ctx(1) })
    state = snap(state, ctx(1), 0, [task('a', 2, {
      lastRun: {
        id: 'run-old', taskId: 'a', taskRevision: 2, runRevision: 5, status: 'succeeded',
        scheduledAtUTC: '2026-11-19T01:00:00Z', schedule: task('seed', 1).schedule,
      },
    })])
    state = reduceAutomation(state, {
      type: 'event',
      context: ctx(1),
      task: task('a', 2, {
        lastRun: {
          id: 'run-new', taskId: 'a', taskRevision: 2, runRevision: 1, status: 'running',
          scheduledAtUTC: '2026-11-19T02:00:00Z', schedule: task('seed', 1).schedule,
        },
      }),
    })
    expect(state.tasks.a?.lastRun?.id).toBe('run-new')
    expect(state.tasks.a?.lastRun?.status).toBe('running')
  })

  it('accepts a same-run conversation title without changing task revision', () => {
    const lastRun = {
      id: 'run-a', taskId: 'a', taskRevision: 2, runRevision: 3, status: 'succeeded',
      scheduledAtUTC: '2026-11-19T01:00:00Z', sessionId: 'session-a', schedule: task('seed', 1).schedule,
    }
    let state = reduceAutomation(emptyAutomationState(), { type: 'bind', context: ctx(1) })
    state = snap(state, ctx(1), 0, [task('a', 2, {
      lastRun, lastLinkedRunId: 'run-a', linkedSessionTitle: 'say hello',
    })])
    state = reduceAutomation(state, {
      type: 'event',
      context: ctx(1),
      task: task('a', 2, {
        lastRun, lastLinkedRunId: 'run-a', linkedSessionTitle: '问候用户',
      }),
    })
    expect(state.tasks.a?.linkedSessionTitle).toBe('问候用户')
  })

  it('clears the cache on a new context and drops late responses and events', () => {
    let state = reduceAutomation(emptyAutomationState(), { type: 'bind', context: ctx(1) })
    state = snap(state, ctx(1), 0, [task('a', 2, { name: '旧任务' })])
    state = reduceAutomation(state, { type: 'context', context: ctx(2) })
    expect(state.tasks).toEqual({})
    expect(state.list.ids).toEqual([])
    expect(state.context).toEqual(ctx(2))

    const latePage = snap(state, ctx(1), state.list.generation, [task('a', 9, { name: '过期快照' })])
    expect(latePage.tasks).toEqual({})
    const lateEvent = reduceAutomation(state, { type: 'event', context: ctx(1), task: task('a', 9, { name: '过期事件' }) })
    expect(lateEvent.tasks).toEqual({})
  })

  it('does not let a slow response for the previous search replace the current query', () => {
    let state = reduceAutomation(emptyAutomationState(), { type: 'bind', context: ctx(1) })
    state = reduceAutomation(state, { type: 'query', query: { filter: 'enabled', text: '甲' } })
    const firstGen = state.list.generation
    state = reduceAutomation(state, { type: 'query', query: { filter: 'enabled', text: '乙' } })
    const secondGen = state.list.generation
    expect(secondGen).toBeGreaterThan(firstGen)

    state = snap(state, ctx(1), firstGen, [task('old', 1, { name: '甲的结果' })])
    expect(state.list.ids).toEqual([])
    expect(state.list.query.text).toBe('乙')

    state = snap(state, ctx(1), secondGen, [task('new', 1, { name: '乙的结果' })])
    expect(state.list.ids).toEqual(['new'])
    expect(state.tasks.old).toBeUndefined()
  })

  it('appends the next page without duplicating a row', () => {
    let state = reduceAutomation(emptyAutomationState(), { type: 'bind', context: ctx(1) })
    state = snap(state, ctx(1), 0, [task('a', 1)], 'cursor-1')
    state = reduceAutomation(state, {
      type: 'append',
      context: ctx(1),
      generation: 0,
      page: page(ctx(1), [task('a', 2, { name: '更新' }), task('b', 1)], 'cursor-2'),
    })
    expect(state.list.ids).toEqual(['a', 'b'])
    expect(state.tasks.a?.revision).toBe(2)
    expect(state.list.nextCursor).toBe('cursor-2')
  })

  it('moves a task between enabled and paused tabs on the authoritative event', () => {
    let state = reduceAutomation(emptyAutomationState(), { type: 'bind', context: ctx(1) })
    state = snap(state, ctx(1), 0, [task('a', 1)])
    state = reduceAutomation(state, { type: 'event', context: ctx(1), task: task('a', 2, { state: 'paused' }) })
    expect(state.list.ids).toEqual([])
    expect(state.tasks.a?.state).toBe('paused')

    state = reduceAutomation(state, { type: 'query', query: { filter: 'paused', text: '' } })
    state = snap(state, ctx(1), 1, [task('a', 2, { state: 'paused' })])
    expect(state.list.ids).toEqual(['a'])

    state = reduceAutomation(state, { type: 'event', context: ctx(1), task: task('a', 3, { state: 'enabled' }) })
    expect(state.list.ids).toEqual([])
  })
})
