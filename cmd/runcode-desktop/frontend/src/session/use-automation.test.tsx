// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AutomationContext, AutomationTask } from '@/core/bridge'

const api = vi.hoisted(() => ({
  createAutomationTask: vi.fn(),
  deleteAutomationTask: vi.fn(),
  getAutomationDefaults: vi.fn(),
  listAutomationTasks: vi.fn(),
  onEnvelope: vi.fn(),
  runAutomationTaskNow: vi.fn(),
  setAutomationTaskPaused: vi.fn(),
  updateAutomationTask: vi.fn(),
  handlers: new Map<string, (env: { payload?: unknown }) => void>(),
}))

vi.mock('@/core/bridge', () => ({
  createAutomationTask: api.createAutomationTask,
  deleteAutomationTask: api.deleteAutomationTask,
  errText: (e: unknown) => e instanceof Error ? e.message : String(e),
  getAutomationDefaults: api.getAutomationDefaults,
  listAutomationTasks: api.listAutomationTasks,
  onEnvelope: api.onEnvelope,
  runAutomationTaskNow: api.runAutomationTaskNow,
  setAutomationTaskPaused: api.setAutomationTaskPaused,
  updateAutomationTask: api.updateAutomationTask,
  Events: {
    AutomationTaskChanged: 'automation:task_changed',
    AutomationContextChanged: 'automation:context_changed',
    AutomationServiceChanged: 'automation:service_changed',
    SessionRenamed: 'session:renamed',
  },
}))

import { useAutomation, type AutomationController } from './use-automation'

const context: AutomationContext = { viewId: 'view', epoch: 1 }
const nextContext: AutomationContext = { viewId: 'view', epoch: 2 }
const schedule = {
  mode: 'daily' as const, timeZone: 'Asia/Shanghai', startDate: '2026-11-19', timeOfDay: '09:00:00', interval: 1,
}
const task: AutomationTask = {
  id: 'task-1', revision: 3, name: '整理记录', prompt: '生成摘要', workspace: 'D:\\资料',
  executionMode: 'reuse_session', schedule, state: 'paused', nextRunAtUTC: '2026-11-20T01:00:00Z',
  binding: { modelKind: 'passport', modelName: 'model-a', bindingVersion: 1 },
}

let root: Root
let host: HTMLDivElement
let controller: AutomationController

function Harness() {
  controller = useAutomation(true)
  return null
}

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.stubGlobal('crypto', { randomUUID: () => 'request-1' })
  for (const fn of [
    api.createAutomationTask, api.deleteAutomationTask, api.getAutomationDefaults, api.listAutomationTasks,
    api.onEnvelope, api.runAutomationTaskNow, api.setAutomationTaskPaused, api.updateAutomationTask,
  ]) fn.mockReset()
  api.handlers.clear()
  api.onEnvelope.mockImplementation((event: string, handler: (env: { payload?: unknown }) => void) => {
    api.handlers.set(event, handler)
    return () => { api.handlers.delete(event) }
  })
  api.getAutomationDefaults.mockResolvedValue({
    context,
    available: true,
    currentWorkspace: 'D:\\资料',
    recentWorkspaces: ['D:\\资料'],
    model: { name: 'model-a', kind: 'passport', permissionMode: 'interactive' },
    permissionMode: 'interactive',
    backgroundSupported: true,
    bindingVersion: 1,
  })
  api.listAutomationTasks.mockResolvedValue({ context, items: [task] })
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})

async function renderController() {
  await act(async () => root.render(<Harness />))
  await act(async () => { await Promise.resolve() })
  await act(async () => { await Promise.resolve() })
}

describe('use automation controller', () => {
  it('runs now with a stable request id and removes a soft-deleted task', async () => {
    api.runAutomationTaskNow.mockResolvedValue({
      context,
      run: {
        id: 'run-manual', taskId: task.id, taskRevision: task.revision, runRevision: 1,
        status: 'queued', scheduledAtUTC: '2026-11-19T01:00:00Z', trigger: 'manual', schedule,
      },
    })
    api.deleteAutomationTask.mockResolvedValue({ context, taskId: task.id })
    await renderController()

    await act(async () => { await controller.runNow(task) })
    expect(api.runAutomationTaskNow).toHaveBeenCalledWith({
      context,
      taskId: task.id,
      expectedRevision: task.revision,
      clientRequestId: 'request-1',
    })

    await act(async () => { await controller.deleteTask(task) })
    expect(api.deleteAutomationTask).toHaveBeenCalledWith({
      context,
      taskId: task.id,
      expectedRevision: task.revision,
    })
    expect(controller.tasks).toEqual([])
    expect(controller.selectedTaskId).toBe('')
  })

  it('rejects a manual-run response that returns under the previous identity', async () => {
    let resolveRun!: (value: unknown) => void
    api.runAutomationTaskNow.mockReturnValue(new Promise((resolve) => { resolveRun = resolve }))
    api.getAutomationDefaults
      .mockResolvedValueOnce({
        context,
        available: true,
        currentWorkspace: 'D:\\资料',
        recentWorkspaces: ['D:\\资料'],
        model: { name: 'model-a', kind: 'passport', permissionMode: 'interactive' },
        permissionMode: 'interactive',
        backgroundSupported: true,
        bindingVersion: 1,
      })
      .mockReturnValueOnce(new Promise(() => {}))
    await renderController()

    let pending!: Promise<void>
    await act(async () => {
      pending = controller.runNow(task)
      await Promise.resolve()
    })
    await act(async () => {
      api.handlers.get('automation:context_changed')?.({ payload: { context: nextContext } })
      await Promise.resolve()
    })
    const rejection = expect(pending).rejects.toThrow('身份已变化，请刷新后重试')
    await act(async () => {
      resolveRun({
        context,
        run: {
          id: 'run-old', taskId: task.id, taskRevision: task.revision, runRevision: 1,
          status: 'queued', scheduledAtUTC: '2026-11-19T01:00:00Z', trigger: 'manual', schedule,
        },
      })
    })
    await rejection
    expect(controller.pendingTaskId).toBe('')
  })

  it('does not remove a task when delete returns under the previous identity', async () => {
    let resolveDelete!: (value: unknown) => void
    api.deleteAutomationTask.mockReturnValue(new Promise((resolve) => { resolveDelete = resolve }))
    api.getAutomationDefaults
      .mockResolvedValueOnce({
        context,
        available: true,
        currentWorkspace: 'D:\\资料',
        recentWorkspaces: ['D:\\资料'],
        model: { name: 'model-a', kind: 'passport', permissionMode: 'interactive' },
        permissionMode: 'interactive',
        backgroundSupported: true,
        bindingVersion: 1,
      })
      .mockReturnValueOnce(new Promise(() => {}))
    await renderController()

    let pending!: Promise<void>
    await act(async () => {
      pending = controller.deleteTask(task)
      await Promise.resolve()
    })
    await act(async () => {
      api.handlers.get('automation:context_changed')?.({ payload: { context: nextContext } })
      await Promise.resolve()
    })
    const rejection = expect(pending).rejects.toThrow('身份已变化，请刷新后重试')
    await act(async () => {
      resolveDelete({ context, taskId: task.id })
    })
    await rejection
    expect(controller.pendingTaskId).toBe('')
  })

  it('reloads a linked task after its conversation receives a generated title', async () => {
    const linked = {
      ...task,
      lastRun: {
        id: 'run-linked', taskId: task.id, taskRevision: task.revision, runRevision: 2,
        status: 'succeeded' as const, scheduledAtUTC: '2026-11-19T01:00:00Z',
        sessionId: 'session-linked', schedule,
      },
      lastLinkedRunId: 'run-linked',
      linkedSessionTitle: 'say hello',
    }
    api.listAutomationTasks
      .mockResolvedValueOnce({ context, items: [linked] })
      .mockResolvedValueOnce({ context, items: [{ ...linked, linkedSessionTitle: '问候用户' }] })
    await renderController()

    await act(async () => {
      api.handlers.get('session:renamed')?.({ payload: { id: 'session-linked', title: '问候用户' } })
      await Promise.resolve()
    })
    await act(async () => { await Promise.resolve() })

    expect(api.listAutomationTasks).toHaveBeenCalledTimes(2)
    expect(controller.tasks[0]?.linkedSessionTitle).toBe('问候用户')
  })
})
