// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AutomationRun } from '@/core/bridge'
import { RunList } from './run-list'

const bridge = vi.hoisted(() => ({
  listAutomationRuns: vi.fn(),
  onEnvelope: vi.fn(),
  handler: null as ((env: { payload: { context: { viewId: string; epoch: number }; run: AutomationRun } }) => void) | null,
}))

vi.mock('@/core/bridge', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/core/bridge')>()
  return {
    ...actual,
    listAutomationRuns: bridge.listAutomationRuns,
    onEnvelope: (_event: string, handler: typeof bridge.handler) => {
      bridge.handler = handler
      return () => { bridge.handler = null }
    },
    Events: { AutomationRunChanged: 'automation:run_changed' },
    errText: (e: unknown) => e instanceof Error ? e.message : String(e),
  }
})

const context = { viewId: 'view', epoch: 1 }
const schedule = {
  mode: 'daily', timeZone: 'Asia/Shanghai', startDate: '2026-11-19', timeOfDay: '09:00:00', interval: 1,
}

function run(id: string, status: string, extra: Partial<AutomationRun> = {}): AutomationRun {
  return {
    id,
    taskId: 'task-1',
    taskRevision: 1,
    runRevision: 1,
    status,
    scheduledAtUTC: id === 'new' ? '2026-11-19T02:00:00Z' : '2026-11-19T01:00:00Z',
    sessionId: 'session-1',
    schedule,
    ...extra,
  }
}

let root: Root
let host: HTMLDivElement

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  bridge.listAutomationRuns.mockReset()
  bridge.onEnvelope.mockReset()
  bridge.handler = null
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: vi.fn(async () => {}) },
  })
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.restoreAllMocks()
})

describe('automation run list', () => {
  it('paginates without duplicates, keeps newest order, and only marks success green', async () => {
    bridge.listAutomationRuns
      .mockResolvedValueOnce({ context, items: [run('new', 'succeeded')], nextCursor: 'cursor-2' })
      .mockResolvedValueOnce({ context, items: [run('new', 'succeeded'), run('old', 'failed', { reason: '目录不存在' })] })
    await act(async () => {
      root.render(<RunList taskId="task-1" context={context} onOpen={async () => {}} />)
    })
    await act(async () => { await Promise.resolve() })
    expect(host.querySelectorAll('[data-run-id]')).toHaveLength(1)

    const more = [...host.querySelectorAll('button')].find((button) => button.textContent === '加载更多')!
    await act(async () => more.click())
    await act(async () => { await Promise.resolve() })
    expect([...host.querySelectorAll('[data-run-id]')].map((el) => el.getAttribute('data-run-id'))).toEqual(['new', 'old'])
    expect((host.querySelector('[data-run-id="new"]') as HTMLElement).dataset.success).toBe('true')
    expect((host.querySelector('[data-run-id="old"]') as HTMLElement).dataset.success).toBe('false')

    const copy = [...host.querySelectorAll('button')].find((button) => button.textContent === '复制原因')!
    await act(async () => copy.click())
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('目录不存在')
  })

  it('labels a user-triggered run as manual', async () => {
    bridge.listAutomationRuns.mockResolvedValue({
      context,
      items: [run('manual', 'succeeded', { trigger: 'manual' })],
    })
    await act(async () => {
      root.render(<RunList taskId="task-1" context={context} onOpen={async () => {}} />)
    })
    await act(async () => { await Promise.resolve() })
    expect(host.querySelector('[data-run-id="manual"]')?.textContent).toContain('手动执行')
  })

  it('merges a run event without moving an older row and opens linked history', async () => {
    bridge.listAutomationRuns.mockResolvedValue({
      context,
      items: [run('new', 'succeeded'), run('old', 'failed', { reason: '旧失败' })],
    })
    const open = vi.fn(async () => {})
    await act(async () => {
      root.render(<RunList taskId="task-1" context={context} onOpen={open} />)
    })
    await act(async () => { await Promise.resolve() })

    await act(async () => {
      bridge.handler?.({
        payload: { context, run: run('old', 'succeeded', { runRevision: 2, reason: '重跑成功' }) },
      })
    })
    expect([...host.querySelectorAll('[data-run-id]')].map((el) => el.getAttribute('data-run-id'))).toEqual(['new', 'old'])
    expect((host.querySelector('[data-run-id="old"]') as HTMLElement).dataset.success).toBe('true')

    const old = [...host.querySelectorAll('[data-run-id="old"] button')][0] as HTMLButtonElement
    await act(async () => old.click())
    expect(open).toHaveBeenCalledWith('task-1', 'old')
  })
})
