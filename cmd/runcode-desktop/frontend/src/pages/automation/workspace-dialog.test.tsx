// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/core/bridge', () => ({
  getAutomationDefaults: vi.fn(async () => ({
    context: { viewId: 'view', epoch: 1 },
    available: true,
    recentWorkspaces: [],
    model: { name: 'model-a' },
    backgroundSupported: false,
    bindingVersion: 1,
  })),
  listAutomationTasks: vi.fn(async () => ({
    context: { viewId: 'view', epoch: 1 },
    items: [{
      id: 'task-1', revision: 1, name: '任务 A', prompt: '生成摘要', workspace: 'D:\\资料',
      executionMode: 'reuse_session',
      schedule: { mode: 'daily', timeZone: 'Asia/Shanghai', startDate: '2026-11-19', timeOfDay: '09:00:00', interval: 1 },
      state: 'enabled', nextRunAtUTC: '2026-11-20T01:00:00Z',
      binding: { modelKind: 'passport', modelName: 'model-a', bindingVersion: 1 },
    }],
  })),
  listAutomationRuns: vi.fn(async () => ({
    context: { viewId: 'view', epoch: 1 },
    items: [{
      id: 'run-1', taskId: 'task-1', taskRevision: 1, runRevision: 1, status: 'succeeded',
      scheduledAtUTC: '2026-11-19T01:00:00Z', sessionId: 'session-1',
      schedule: { mode: 'daily', timeZone: 'Asia/Shanghai', startDate: '2026-11-19', timeOfDay: '09:00:00', interval: 1 },
    }],
  })),
  errText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  Events: {
    AutomationTaskChanged: 'automation:task_changed',
    AutomationContextChanged: 'automation:context_changed',
    AutomationServiceChanged: 'automation:service_changed',
    AutomationRunChanged: 'automation:run_changed',
  },
  onEnvelope: () => () => {},
  openSession: vi.fn(),
  pickWorkspaceFolder: vi.fn(),
}))

import { AutomationWorkspaceDialog } from './workspace-dialog'

let root: Root
let host: HTMLDivElement

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

function Harness() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <textarea aria-label="composer" defaultValue="未发送的草稿" />
      <button type="button" onClick={() => setOpen(true)}>更多</button>
      <AutomationWorkspaceDialog open={open} onClose={() => setOpen(false)} onOpenRun={async () => {}} />
    </>
  )
}

function OpenRunHarness({ onOpenRun }: {
  onOpenRun: (req: { context: { viewId: string; epoch: number }; taskId: string; runId: string }) => Promise<void>
}) {
  const [open, setOpen] = useState(true)
  return <AutomationWorkspaceDialog open={open} onClose={() => setOpen(false)} onOpenRun={onOpenRun} />
}

describe('automation workspace dialog', () => {
  it('returns focus to the trigger and leaves the composer draft untouched', async () => {
    await act(async () => { root.render(<Harness />) })
    const draft = host.querySelector('textarea') as HTMLTextAreaElement
    const trigger = [...host.querySelectorAll('button')].find((b) => b.textContent === '更多') as HTMLButtonElement
    expect(draft.value).toBe('未发送的草稿')
    // jsdom 的 click 不会把焦点送进按钮；真实点击会先 focus 再触发。这里补上这一步。
    trigger.focus()

    await act(async () => { trigger.click() })
    expect(host.querySelector('[role="dialog"]')).toBeTruthy()
    expect(host.textContent).toContain('关闭客户端后任务将停止')
    const close = [...host.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === '关闭')
    expect(close).toBeTruthy()
    await act(async () => { close!.click() })

    expect(host.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(trigger)
    expect(draft.value).toBe('未发送的草稿')
  })

  it('opens linked history through the client session callback and closes the manager', async () => {
    const onOpenRun = vi.fn(async () => {})
    await act(async () => {
      root.render(<OpenRunHarness onOpenRun={onOpenRun} />)
    })
    await act(async () => { await Promise.resolve() })
    const task = host.querySelector('[data-task-id="task-1"]') as HTMLButtonElement
    await act(async () => task.click())
    const runs = [...host.querySelectorAll('button')].find((button) => button.textContent === '任务运行记录')!
    await act(async () => runs.click())
    await act(async () => { await Promise.resolve() })
    const run = host.querySelector('[data-run-id="run-1"] button') as HTMLButtonElement
    await act(async () => run.click())

    expect(onOpenRun).toHaveBeenCalledWith({ context: { viewId: 'view', epoch: 1 }, taskId: 'task-1', runId: 'run-1' })
    await act(async () => { await Promise.resolve() })
    expect(host.querySelector('[role="dialog"]')).toBeNull()
  })
})
