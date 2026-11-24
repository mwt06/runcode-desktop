// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const bridge = vi.hoisted(() => ({
  pickWorkspaceFolder: vi.fn(),
  openSession: vi.fn(),
  resumeSession: vi.fn(),
}))

vi.mock('@/core/bridge', () => ({
  pickWorkspaceFolder: bridge.pickWorkspaceFolder,
  openSession: bridge.openSession,
  resumeSession: bridge.resumeSession,
  errText: (e: unknown) => e instanceof Error ? e.message : String(e),
}))

import { WorkspacePicker } from './workspace-picker'

let root: Root
let host: HTMLDivElement

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  bridge.pickWorkspaceFolder.mockReset()
  bridge.openSession.mockReset()
  bridge.resumeSession.mockReset()
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  document.querySelectorAll('[role="listbox"]').forEach((el) => el.remove())
})

function Harness() {
  const [value, setValue] = useState('C:\\Users\\yyzxc\\Desktop\\Test-guokai-client')
  return (
    <>
      <WorkspacePicker
        value={value}
        current="C:\\Users\\yyzxc\\Desktop\\Test-guokai-client"
        recent={['C:\\Users\\yyzxc\\Desktop\\Client-Platform\\imagegen-client', 'C:\\Users\\yyzxc\\Desktop\\Bridge']}
        onChange={setValue}
      />
      <div data-value={value} />
    </>
  )
}

function clickBrowse() {
  const browse = [...document.querySelectorAll<HTMLButtonElement>('[role="listbox"] button')]
    .find((button) => button.textContent?.includes('浏览其他目录...'))
  if (!browse) throw new Error('browse workspace button not found')
  browse.click()
}

describe('workspace picker', () => {
  it('uses a folded current workspace and shows current and history without a search box', async () => {
    await act(async () => { root.render(<Harness />) })
    expect(host.textContent).toContain('Test-guokai-client')
    expect(host.querySelector('input')).toBeNull()
    expect(host.textContent).not.toContain('imagegen-client')
    await act(async () => (host.querySelector('button[aria-haspopup="listbox"]') as HTMLButtonElement).click())
    const list = document.querySelector('[role="listbox"]') as HTMLElement
    const text = list.textContent ?? ''
    expect(text).toContain('Test-guokai-client')
    expect(text).toContain('imagegen-client')
    expect(text).toContain('Bridge')
    expect(text).toContain('C:\\Users\\yyzxc\\Desktop\\Test-guokai-client')
    expect(text).toContain('浏览其他目录...')
    expect(list.querySelector('[role="option"][aria-selected="true"]')?.textContent).toContain('Test-guokai-client')
  })

  it('selects a history item through onChange and closes the list', async () => {
    await act(async () => { root.render(<Harness />) })
    await act(async () => (host.querySelector('button[aria-haspopup="listbox"]') as HTMLButtonElement).click())
    const choice = [...document.querySelectorAll('[role="option"]')].find((el) => el.textContent?.includes('Bridge')) as HTMLButtonElement
    await act(async () => choice.click())
    expect(host.querySelector('[data-value]')?.getAttribute('data-value')).toBe('C:\\Users\\yyzxc\\Desktop\\Bridge')
    expect(document.querySelector('[role="listbox"]')).toBeNull()
  })

  it('keeps the old value on cancellation and adds a newly browsed directory immediately', async () => {
    await act(async () => { root.render(<Harness />) })
    await act(async () => (host.querySelector('button[aria-haspopup="listbox"]') as HTMLButtonElement).click())
    bridge.pickWorkspaceFolder.mockResolvedValueOnce('')
    await act(async () => clickBrowse())
    expect(host.querySelector('[data-value]')?.getAttribute('data-value')).toBe('C:\\Users\\yyzxc\\Desktop\\Test-guokai-client')

    bridge.pickWorkspaceFolder.mockResolvedValueOnce('D:\\新 目录\\项目')
    // 取消原生目录选择后折叠菜单保持打开，用户可以直接再次浏览。
    await act(async () => clickBrowse())
    expect(host.querySelector('[data-value]')?.getAttribute('data-value')).toBe('D:\\新 目录\\项目')

    await act(async () => (host.querySelector('button[aria-haspopup="listbox"]') as HTMLButtonElement).click())
    expect(document.querySelector('[role="listbox"]')?.textContent).toContain('D:\\新 目录\\项目')
    expect(bridge.openSession).not.toHaveBeenCalled()
    expect(bridge.resumeSession).not.toHaveBeenCalled()
  })

  it('shows native picker failures without changing the value', async () => {
    await act(async () => { root.render(<Harness />) })
    await act(async () => (host.querySelector('button[aria-haspopup="listbox"]') as HTMLButtonElement).click())
    bridge.pickWorkspaceFolder.mockRejectedValueOnce(new Error('无法打开目录选择器'))
    await act(async () => clickBrowse())
    expect(document.querySelector('[role="listbox"]')?.textContent).toContain('无法打开目录选择器')
    expect(host.querySelector('[data-value]')?.getAttribute('data-value')).toBe('C:\\Users\\yyzxc\\Desktop\\Test-guokai-client')
  })

  it('treats the native cancelled-by-user error as a silent cancellation', async () => {
    await act(async () => { root.render(<Harness />) })
    await act(async () => (host.querySelector('button[aria-haspopup="listbox"]') as HTMLButtonElement).click())
    bridge.pickWorkspaceFolder.mockRejectedValueOnce(new Error('cancelled by user'))
    await act(async () => clickBrowse())
    expect(document.querySelector('[role="listbox"]')?.textContent).not.toContain('cancelled by user')
    expect(host.querySelector('[data-value]')?.getAttribute('data-value')).toBe('C:\\Users\\yyzxc\\Desktop\\Test-guokai-client')
  })
})
