// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  closeMainWindow: vi.fn(),
  minimise: vi.fn(),
  toggle: vi.fn(),
}))

vi.mock('@/core/bridge', () => ({
  closeMainWindow: api.closeMainWindow,
  errText: (e: unknown) => e instanceof Error ? e.message : String(e),
}))

vi.mock('@wailsio/runtime', () => ({
  Window: { Minimise: api.minimise, ToggleMaximise: api.toggle },
}))

import { TitleBar } from './title-bar'

let host: HTMLDivElement
let root: Root

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  api.closeMainWindow.mockReset().mockResolvedValue(undefined)
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

it('routes title-bar close through the shared window lifecycle command', async () => {
  await act(async () => root.render(<TitleBar />))
  const close = host.querySelector('button[title="关闭"]') as HTMLButtonElement
  await act(async () => close.click())
  expect(api.closeMainWindow).toHaveBeenCalledTimes(1)
})

it('re-enables close after the native chooser returns or is cancelled', async () => {
  await act(async () => root.render(<TitleBar />))
  const close = host.querySelector('button[title="关闭"]') as HTMLButtonElement
  await act(async () => close.click())
  await act(async () => { await Promise.resolve() })
  expect(close.disabled).toBe(false)
  await act(async () => close.click())
  expect(api.closeMainWindow).toHaveBeenCalledTimes(2)
})

it('surfaces a close failure without pretending the window was hidden', async () => {
  api.closeMainWindow.mockRejectedValueOnce(new Error('窗口不可用'))
  const onError = vi.fn()
  await act(async () => root.render(<TitleBar onError={onError} />))
  const close = host.querySelector('button[title="关闭"]') as HTMLButtonElement
  await act(async () => close.click())
  await act(async () => { await Promise.resolve() })
  expect(onError).toHaveBeenCalledWith('窗口不可用')
})
