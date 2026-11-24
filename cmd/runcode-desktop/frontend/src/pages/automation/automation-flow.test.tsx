// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  createAutomationTask: vi.fn(),
  getAutomationDefaults: vi.fn(),
  listAutomationTasks: vi.fn(),
  listAutomationRuns: vi.fn(),
  setAutomationTaskPaused: vi.fn(),
  updateAutomationTask: vi.fn(),
  pickWorkspaceFolder: vi.fn(),
}))

vi.mock('@/core/bridge', () => ({
  ...api,
  errText: (e: unknown) => e instanceof Error ? e.message : String(e),
  Events: {
    AutomationTaskChanged: 'automation:task_changed',
    AutomationContextChanged: 'automation:context_changed',
    AutomationServiceChanged: 'automation:service_changed',
    AutomationRunChanged: 'automation:run_changed',
  },
  onEnvelope: () => () => {},
}))

import { AutomationWorkspaceDialog } from './workspace-dialog'

const context = { viewId: 'view', epoch: 1 }
const schedule = {
  mode: 'daily', timeZone: 'Asia/Shanghai', startDate: '2026-11-19', timeOfDay: '09:00:00', interval: 1,
}
const task = {
  id: 'task-1', revision: 1, name: '整理记录', prompt: '生成摘要', workspace: 'D:\\资料',
  executionMode: 'reuse_session', schedule, state: 'enabled',
  nextRunAtUTC: '2026-11-20T01:00:00Z',
  binding: { modelKind: 'passport', modelName: 'model-a', bindingVersion: 1 },
}

let root: Root
let host: HTMLDivElement

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.stubGlobal('crypto', { randomUUID: () => 'request-1' })
  for (const fn of Object.values(api)) fn.mockReset()
  api.getAutomationDefaults.mockResolvedValue({
    context,
    available: true,
    recentWorkspaces: ['D:\\资料'],
    model: { name: 'model-a', kind: 'passport', permissionMode: 'interactive' },
    permissionMode: 'interactive',
    backgroundSupported: true,
    bindingVersion: 1,
  })
  api.listAutomationTasks.mockResolvedValue({ context, items: [] })
  api.createAutomationTask.mockResolvedValue({ context, task, warning: '' })
  api.setAutomationTaskPaused.mockResolvedValue({
    context,
    task: { ...task, revision: 2, state: 'paused' },
  })
  api.listAutomationRuns.mockResolvedValue({
    context,
    items: [{
      id: 'run-1', taskId: 'task-1', taskRevision: 2, runRevision: 1, status: 'succeeded',
      scheduledAtUTC: '2026-11-20T01:00:00Z', sessionId: 'session-1', schedule,
    }],
  })
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})

function setValue(el: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

it('creates, pauses and opens linked history without submitting a chat turn', async () => {
  const onOpenRun = vi.fn(async () => {})
  await act(async () => {
    root.render(<AutomationWorkspaceDialog open onClose={() => {}} onOpenRun={onOpenRun} />)
  })
  await act(async () => { await Promise.resolve() })
  await act(async () => {
    ;([...host.querySelectorAll('button')].find((button) => button.textContent === '新建自动化任务') as HTMLButtonElement).click()
  })
  await act(async () => {
    setValue(host.querySelector('input[aria-label="任务名称"]') as HTMLInputElement, '整理记录')
    setValue(host.querySelector('textarea[aria-label="任务内容"]') as HTMLTextAreaElement, '生成摘要')
  })
  await act(async () => {
    ;([...host.querySelectorAll('button')].find((button) => button.textContent === '保存') as HTMLButtonElement).click()
  })
  expect(api.createAutomationTask).toHaveBeenCalledTimes(1)
  expect(host.querySelector('input[aria-label="任务名称"]') as HTMLInputElement).toBeTruthy()

  await act(async () => {
    ;(host.querySelector('button[aria-label="暂停任务"]') as HTMLButtonElement).click()
  })
  await act(async () => { await Promise.resolve() })
  expect(api.setAutomationTaskPaused).toHaveBeenCalledWith(expect.objectContaining({
    taskId: 'task-1', expectedRevision: 1, paused: true,
  }))
  expect(host.querySelector('button[aria-label="恢复任务"]')).toBeTruthy()

  await act(async () => {
    ;([...host.querySelectorAll('button')].find((button) => button.textContent === '任务运行记录') as HTMLButtonElement).click()
  })
  await act(async () => { await Promise.resolve() })
  const run = host.querySelector('[data-run-id="run-1"] button') as HTMLButtonElement
  await act(async () => run.click())
  expect(onOpenRun).toHaveBeenCalledWith({ context, taskId: 'task-1', runId: 'run-1' })
})
