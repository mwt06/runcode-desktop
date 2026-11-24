// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const bridge = vi.hoisted(() => ({
  createAutomationTask: vi.fn(),
  sendMessage: vi.fn(),
  sendMessageWithImages: vi.fn(),
  openSession: vi.fn(),
  resumeSession: vi.fn(),
  pickWorkspaceFolder: vi.fn(),
  getAutomationDefaults: vi.fn(),
  listAutomationTasks: vi.fn(),
}))

vi.mock('@/core/bridge', () => ({
  createAutomationTask: bridge.createAutomationTask,
  sendMessage: bridge.sendMessage,
  sendMessageWithImages: bridge.sendMessageWithImages,
  openSession: bridge.openSession,
  resumeSession: bridge.resumeSession,
  pickWorkspaceFolder: bridge.pickWorkspaceFolder,
  getAutomationDefaults: bridge.getAutomationDefaults,
  listAutomationTasks: bridge.listAutomationTasks,
  errText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  errCode: () => '',
  Events: {
    AutomationTaskChanged: 'automation:task_changed',
    AutomationContextChanged: 'automation:context_changed',
    AutomationServiceChanged: 'automation:service_changed',
    AutomationRunChanged: 'automation:run_changed',
  },
  onEnvelope: () => () => {},
}))

import { TaskForm } from './task-form'
import { AutomationWorkspaceDialog } from './workspace-dialog'
import { defaultAutomationDraft } from './draft'

let root: Root
let host: HTMLDivElement

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.stubGlobal('crypto', { randomUUID: () => 'uuid-fixed' })
  for (const fn of Object.values(bridge)) fn.mockReset()
  bridge.getAutomationDefaults.mockResolvedValue({
    context: { viewId: 'view', epoch: 1 },
    available: true,
    recentWorkspaces: ['D:\\资料'],
    model: { name: 'model-a', kind: 'passport', permissionMode: 'interactive' },
    permissionMode: 'interactive',
    backgroundSupported: false,
    bindingVersion: 7,
  })
  bridge.listAutomationTasks.mockResolvedValue({ context: { viewId: 'view', epoch: 1 }, items: [] })
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})

function setSelect(label: string, value: string) {
  const select = host.querySelector(`select[aria-label="${label}"]`) as HTMLSelectElement
  Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

function field(label: string) {
  return [...host.querySelectorAll('label')].find((el) => el.textContent?.includes(label))
}

describe('task form fields', () => {
  it('shows both execution locations and only the fields for each of the seven modes', async () => {
    const initial = defaultAutomationDraft(new Date('2026-11-18T01:07:01Z'), 'Asia/Shanghai', ['D:\\资料'])
    await act(async () => {
      root.render(<TaskForm initial={{ ...initial, name: 'n', prompt: 'p' }} onSave={async () => {}} onCancel={() => {}} />)
    })
    const exec = host.querySelector('select[aria-label="执行位置"]') as HTMLSelectElement
    expect([...exec.options].map((o) => o.textContent)).toEqual(['新建会话并复用', '每次新建会话'])
    expect(host.textContent).not.toContain('自定义')
    expect(host.textContent).not.toContain('cron')
    expect(host.textContent).not.toContain('创建时配置')
    const zones = [...(host.querySelector('select[aria-label="时区"]') as HTMLSelectElement).options].map((o) => o.value)
    expect(zones).toContain('Asia/Shanghai')
    expect(zones.every((zone) => !zone.includes('中国标准时间'))).toBe(true)

    const units: Record<string, string | null> = {
      weekly: '周',
      weekdays: '周',
      daily: '天',
      every_hours: '小时',
      every_minutes: '分钟',
      monthly: '月',
      once: null,
    }
    for (const [mode, unit] of Object.entries(units)) {
      await act(async () => { setSelect('重复', mode) })
      const interval = field('重复间隔')
      if (unit == null) {
        expect(interval).toBeUndefined()
      } else {
        expect(interval?.textContent).toContain(unit)
      }
      expect(!!field('星期')).toBe(mode === 'weekly')
      expect(field('日期')).toBeTruthy()
      expect(!![...host.querySelectorAll('label')].some((el) => el.textContent?.includes('每月几号'))).toBe(mode === 'monthly')
      expect(host.textContent?.includes('无此日期的月份跳过') ?? false).toBe(mode === 'monthly')
    }
  })
})

describe('task form save', () => {
  async function openCreate() {
    await act(async () => {
      root.render(<AutomationWorkspaceDialog open onClose={() => {}} onOpenRun={async () => {}} />)
    })
    const create = [...host.querySelectorAll('button')].find((b) => b.textContent === '新建自动化任务')
    await act(async () => { create!.click() })
    const name = host.querySelector('input[aria-label="任务名称"]') as HTMLInputElement
    const prompt = host.querySelector('textarea[aria-label="任务内容"]') as HTMLTextAreaElement
    await act(async () => {
      setValue(name, '整理记录')
      setValue(prompt, '只回复时间')
    })
  }

  it('reuses one clientRequestId, ignores a second click, and does not run the task', async () => {
    let rejectFirst: (e: Error) => void = () => {}
    const first = new Promise<never>((_resolve, reject) => { rejectFirst = reject })
    bridge.createAutomationTask.mockImplementationOnce(() => first)
    bridge.createAutomationTask.mockResolvedValue({
      context: { viewId: 'view', epoch: 1 },
      task: {
        id: 't1', revision: 1, name: '整理记录', prompt: '只回复时间', workspace: 'D:\\资料',
        executionMode: 'reuse_session',
        schedule: { mode: 'daily', timeZone: 'Asia/Shanghai', startDate: '2026-11-19', timeOfDay: '09:00:00', interval: 1 },
        state: 'enabled', nextRunAtUTC: '2026-11-19T01:00:00Z',
        binding: { modelKind: 'passport', modelName: 'model-a', bindingVersion: 1 },
      },
      warning: '',
    })

    await openCreate()
    const save = [...host.querySelectorAll('button')].find((b) => b.textContent === '保存')!
    await act(async () => { save.click() })
    await act(async () => { save.click() })
    expect(bridge.createAutomationTask).toHaveBeenCalledTimes(1)

    await act(async () => { rejectFirst(new Error('网络中断')) })
    expect(host.textContent).toContain('网络中断')
    expect((host.querySelector('input[aria-label="任务名称"]') as HTMLInputElement).value).toBe('整理记录')
    expect((host.querySelector('textarea[aria-label="任务内容"]') as HTMLTextAreaElement).value).toBe('只回复时间')

    await act(async () => { save.click() })
    expect(bridge.createAutomationTask).toHaveBeenCalledTimes(2)
    const firstReq = bridge.createAutomationTask.mock.calls[0][0]
    const secondReq = bridge.createAutomationTask.mock.calls[1][0]
    expect(firstReq.clientRequestId).toBe(secondReq.clientRequestId)
    expect(firstReq.clientRequestId).toBe('uuid-fixed')
    expect(firstReq.expectedBindingVersion).toBe(7)
    expect(firstReq.draft.prompt).toBe('只回复时间')
    expect(JSON.stringify(firstReq)).not.toContain('apiKey')
    expect(bridge.sendMessage).not.toHaveBeenCalled()
    expect(bridge.sendMessageWithImages).not.toHaveBeenCalled()
    expect(bridge.openSession).not.toHaveBeenCalled()
  })
})

function setValue(el: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}
