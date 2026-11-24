// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Sidebar } from './sidebar'

const bridge = vi.hoisted(() => ({
  openSession: vi.fn(),
  pickWorkspaceFolder: vi.fn(),
  resumeSession: vi.fn(),
  getAutomationDefaults: vi.fn(),
  listAutomationTasks: vi.fn(),
  getMailSettings: vi.fn(),
  listMailDrafts: vi.fn(),
  listMailContacts: vi.fn(),
  listMailOutbox: vi.fn(),
}))

vi.mock('@/core/bridge', () => ({
  openSession: bridge.openSession,
  pickWorkspaceFolder: bridge.pickWorkspaceFolder,
  resumeSession: bridge.resumeSession,
  getAutomationDefaults: bridge.getAutomationDefaults,
  listAutomationTasks: bridge.listAutomationTasks,
  getMailSettings: bridge.getMailSettings,
  listMailDrafts: bridge.listMailDrafts,
  listMailContacts: bridge.listMailContacts,
  listMailOutbox: bridge.listMailOutbox,
  errText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  Events: {
    AutomationTaskChanged: 'automation:task_changed',
    AutomationContextChanged: 'automation:context_changed',
    AutomationServiceChanged: 'automation:service_changed',
    AutomationRunChanged: 'automation:run_changed',
  },
  onEnvelope: () => () => {},
}))

import { AutomationWorkspaceDialog } from '@/pages/automation/workspace-dialog'
import { MailPage } from '@/pages/mail'

let root: Root
let host: HTMLDivElement

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  bridge.openSession.mockReset()
  bridge.pickWorkspaceFolder.mockReset()
  bridge.resumeSession.mockReset()
  bridge.getAutomationDefaults.mockReset()
  bridge.listAutomationTasks.mockReset()
  bridge.getMailSettings.mockReset()
  bridge.listMailDrafts.mockReset()
  bridge.listMailContacts.mockReset()
  bridge.listMailOutbox.mockReset()
  bridge.getAutomationDefaults.mockResolvedValue({
    context: { viewId: 'view', epoch: 1 },
    available: true,
    recentWorkspaces: [],
    model: { name: 'model-a', permissionMode: 'default' },
    backgroundSupported: false,
    bindingVersion: 3,
  })
  bridge.listAutomationTasks.mockResolvedValue({
    context: { viewId: 'view', epoch: 1 },
    items: [],
  })
  bridge.getMailSettings.mockResolvedValue({ secretStorageOk: true })
  bridge.listMailDrafts.mockResolvedValue([])
  bridge.listMailContacts.mockResolvedValue([])
  bridge.listMailOutbox.mockResolvedValue([])
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

function Harness() {
  const [view, setView] = useState<'chat' | 'settings' | 'plugins' | 'market' | 'permissions' | 'memory' | 'usage' | 'mail'>('chat')
  const [open, setOpen] = useState(false)
  return (
    <>
      <Sidebar
        collapsed={false}
        recents={[]}
        openSessions={[]}
        onFocusSession={() => {}}
        onCloseSession={() => {}}
        recentWorkspaces={[]}
        onPickWorkspace={() => { bridge.openSession('D:\\should-not') }}
        onSwitchWorkspace={() => { bridge.pickWorkspaceFolder() }}
        onDelete={() => {}}
        view={view}
        onNav={setView}
        onNew={() => {}}
        onResume={() => {}}
        onLogout={() => {}}
        onOpenAutomation={() => setOpen(true)}
        onOpenMail={() => setView('mail')}
      />
      <div data-testid="view">{view}</div>
      <AutomationWorkspaceDialog open={open} onClose={() => setOpen(false)} onOpenRun={async () => {}} />
      {view === 'mail' ? <MailPage onOpenSettings={() => setView('settings')} /> : null}
    </>
  )
}

function textOf(selector: string) {
  return [...host.querySelectorAll(selector)].map((el) => el.textContent?.trim() ?? '')
}

describe('more menu', () => {
  it('opens 自动化任务 under 市场 without leaving the current view or starting a session', async () => {
    await act(async () => { root.render(<Harness />) })
    const navText = textOf('nav button, nav div')
    const marketAt = navText.findIndex((t) => t.includes('市场'))
    const moreAt = navText.findIndex((t) => t === '更多' || t.includes('更多'))
    expect(marketAt).toBeGreaterThanOrEqual(0)
    expect(moreAt).toBeGreaterThan(marketAt)

    const more = [...host.querySelectorAll('button')].find((b) => b.textContent?.includes('更多'))
    expect(more).toBeTruthy()
    await act(async () => { more!.click() })
    expect(host.textContent).toContain('自动化任务')
    expect(host.textContent).toContain('邮箱与通信录')
    expect(host.textContent).not.toContain('浏览更多插件')
    expect(host.textContent).not.toContain('创建你的插件')
    expect(host.querySelector('[data-testid="view"]')?.textContent).toBe('chat')

    const entry = [...host.querySelectorAll('button')].find((b) => b.textContent?.includes('自动化任务'))
    expect(entry).toBeTruthy()
    await act(async () => { entry!.click() })
    expect(host.querySelector('[role="dialog"][aria-label="自动化任务"]')).toBeTruthy()
    expect(host.querySelector('[data-testid="view"]')?.textContent).toBe('chat')
    expect(bridge.openSession).not.toHaveBeenCalled()
    expect(bridge.pickWorkspaceFolder).not.toHaveBeenCalled()
    expect(bridge.resumeSession).not.toHaveBeenCalled()
  })

  it('opens 邮箱与通信录 as a main view without session side effects or automation dialog', async () => {
    await act(async () => { root.render(<Harness />) })
    const moreButtons = [...host.querySelectorAll('button')].filter((b) => b.textContent?.includes('更多'))
    expect(moreButtons).toHaveLength(1)

    await act(async () => { moreButtons[0].click() })
    const entry = [...host.querySelectorAll('button')].find((b) => b.textContent?.includes('邮箱与通信录'))
    expect(entry).toBeTruthy()
    await act(async () => { entry!.click() })
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(host.querySelector('[data-testid="view"]')?.textContent).toBe('mail')
    expect(host.textContent).toContain('邮箱与通信录')
    expect(host.querySelector('[role="dialog"][aria-label="自动化任务"]')).toBeNull()
    expect(bridge.openSession).not.toHaveBeenCalled()
    expect(bridge.pickWorkspaceFolder).not.toHaveBeenCalled()
    expect(bridge.resumeSession).not.toHaveBeenCalled()

    // PROFILE_NAV 不进更多菜单；更多里只有 automation + mail。
    await act(async () => { moreButtons[0].click() })
    const menu = host.querySelector('[role="menu"]')
    expect(menu?.textContent).toContain('自动化任务')
    expect(menu?.textContent).toContain('邮箱与通信录')
    expect(menu?.textContent).not.toContain('用量统计')
  })
})
