// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAutoSaveChannel, type AutoSaveChannel } from './autosave-channel'
import {
  SettingsSaveProvider,
  useSettingsSaveRegistry,
} from './settings-save-provider'

type Draft = { value: string }

let host: HTMLDivElement
let root: Root

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

function Probe({
  channel,
  channelKey = 'proxy',
  expose,
}: {
  channel: AutoSaveChannel<Draft>
  channelKey?: string
  expose?: (api: ReturnType<typeof useSettingsSaveRegistry>) => void
}) {
  const registry = useSettingsSaveRegistry()
  expose?.(registry)
  if (registry.getChannel<Draft>(channelKey) !== channel) registry.register(channelKey, '出网代理', channel)
  return <div>{registry.errors.map((error) => error.message).join('|')}</div>
}

async function mount(node: ReactNode) {
  await act(async () => root.render(<SettingsSaveProvider>{node}</SettingsSaveProvider>))
}

describe('SettingsSaveProvider', () => {
  it('flushes every registered channel before navigation completes', async () => {
    const firstWrite = vi.fn(async (value: Draft) => value)
    const secondWrite = vi.fn(async (value: Draft) => value)
    const first = createAutoSaveChannel({ initial: { value: '' }, write: firstWrite, delayMs: 600 })
    const second = createAutoSaveChannel({ initial: { value: '' }, write: secondWrite, delayMs: 600 })
    let api!: ReturnType<typeof useSettingsSaveRegistry>

    await mount(<>
      <Probe channel={first} expose={(next) => { api = next }} />
      <Probe channel={second} channelKey="recorder" />
    </>)
    first.edit({ value: 'a' }, 'deferred')
    second.edit({ value: 'b' }, 'deferred')

    let ok = false
    await act(async () => { ok = await api.flushAll() })

    expect(ok).toBe(true)
    expect(firstWrite).toHaveBeenCalledTimes(1)
    expect(secondWrite).toHaveBeenCalledTimes(1)
  })

  it('keeps a failed channel error visible after the settings page unmounts', async () => {
    const write = vi.fn().mockRejectedValueOnce(new Error('disk full'))
    const channel = createAutoSaveChannel({ initial: { value: '' }, write, delayMs: 600 })
    let api!: ReturnType<typeof useSettingsSaveRegistry>

    await mount(<Probe channel={channel} expose={(next) => { api = next }} />)
    channel.edit({ value: 'x' }, 'deferred')
    await act(async () => { await api.flushAll() })
    expect(host.textContent).toContain('disk full')

    await act(async () => root.render(<SettingsSaveProvider><div /></SettingsSaveProvider>))
    await act(async () => root.render(<SettingsSaveProvider><Probe channel={channel} expose={(next) => { api = next }} /></SettingsSaveProvider>))
    expect(host.textContent).toContain('disk full')
  })

  it('replaces a channel with the same key without duplicate flush', async () => {
    const firstWrite = vi.fn(async (value: Draft) => value)
    const secondWrite = vi.fn(async (value: Draft) => value)
    const first = createAutoSaveChannel({ initial: { value: '' }, write: firstWrite, delayMs: 600 })
    const second = createAutoSaveChannel({ initial: { value: '' }, write: secondWrite, delayMs: 600 })
    let api!: ReturnType<typeof useSettingsSaveRegistry>

    await mount(<Probe channel={first} expose={(next) => { api = next }} />)
    await act(async () => root.render(<SettingsSaveProvider><Probe channel={second} expose={(next) => { api = next }} /></SettingsSaveProvider>))
    first.edit({ value: 'old' }, 'deferred')
    second.edit({ value: 'new' }, 'deferred')
    await act(async () => { await api.flushAll() })

    expect(firstWrite).not.toHaveBeenCalled()
    expect(secondWrite).toHaveBeenCalledTimes(1)
  })

  it('stops flushing a channel after it is unregistered', async () => {
    const write = vi.fn(async (value: Draft) => value)
    const channel = createAutoSaveChannel({ initial: { value: '' }, write, delayMs: 600 })
    const extra = createAutoSaveChannel({ initial: { value: '' }, write, delayMs: 600 })
    let api!: ReturnType<typeof useSettingsSaveRegistry>
    let unregister!: () => void

    await mount(<Probe channel={channel} expose={(next) => {
      api = next
      unregister ??= next.register('extra', '额外设置', extra)
    }} />)
    extra.edit({ value: 'x' }, 'deferred')
    unregister()
    await act(async () => { await api.flushAll() })

    expect(write).not.toHaveBeenCalled()
  })
})
