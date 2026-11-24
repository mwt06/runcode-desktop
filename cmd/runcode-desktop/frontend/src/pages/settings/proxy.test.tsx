// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  webProxy: vi.fn(),
  setWebProxy: vi.fn(),
}))

vi.mock('@/core/bridge', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/core/bridge')>()
  return {
    ...actual,
    ...api,
    errText: (error: unknown) => error instanceof Error ? error.message : String(error),
  }
})

import { ProxySection } from './proxy'
import { SettingsSaveProvider } from './settings-save-provider'

let host: HTMLDivElement
let root: Root

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  api.webProxy.mockReset()
  api.setWebProxy.mockReset()
  api.webProxy.mockResolvedValue('')
  api.setWebProxy.mockImplementation(async (value: string) => value)
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})

afterEach(async () => {
  vi.useRealTimers()
  await act(async () => root.unmount())
  host.remove()
})

async function mount() {
  await act(async () => root.render(<SettingsSaveProvider><ProxySection /></SettingsSaveProvider>))
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

function input() {
  return host.querySelector<HTMLInputElement>('input')!
}

async function type(value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    setter.call(input(), value)
    input().dispatchEvent(new InputEvent('input', { bubbles: true, data: value, inputType: 'insertText' }))
  })
}

describe('ProxySection autosave', () => {
  it('does not render a local save button', async () => {
    await mount()
    expect(Array.from(host.querySelectorAll('button')).some((button) => button.textContent === '保存')).toBe(false)
  })

  it('debounces edits and saves the latest proxy value', async () => {
    vi.useFakeTimers()
    await mount()
    await type('127.0.0.1:7890')
    await type('socks5://127.0.0.1:1080')
    await act(async () => { await vi.advanceTimersByTimeAsync(599) })
    expect(api.setWebProxy).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(api.setWebProxy).toHaveBeenCalledWith('socks5://127.0.0.1:1080')
  })

  it('flushes a pending edit on blur', async () => {
    await mount()
    await type('127.0.0.1:7890')
    await act(async () => {
      input().focus()
      input().blur()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(api.setWebProxy).toHaveBeenCalledWith('127.0.0.1:7890')
  })

  it('saves an explicit empty value as direct connection', async () => {
    api.webProxy.mockResolvedValue('http://127.0.0.1:7890')
    await mount()
    await type('')
    await act(async () => {
      input().focus()
      input().blur()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(api.setWebProxy).toHaveBeenCalledWith('')
  })

  it('keeps invalid input and reports the failure', async () => {
    api.setWebProxy.mockRejectedValueOnce(new Error('代理地址无效'))
    await mount()
    await type('ftp://bad')
    await act(async () => {
      input().focus()
      input().blur()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(input().value).toBe('ftp://bad')
    expect(host.textContent).toContain('代理地址无效')
  })
})
