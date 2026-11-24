// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  recorderSettings: vi.fn(),
  recorderDevices: vi.fn(),
  patchRecorderSettings: vi.fn(),
}))

vi.mock('@/core/bridge', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/core/bridge')>()
  return {
    ...actual,
    ...api,
    errText: (error: unknown) => error instanceof Error ? error.message : String(error),
  }
})

import { RecorderSection } from './recorder'
import { SettingsSaveProvider } from './settings-save-provider'

let host: HTMLDivElement
let root: Root

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  api.recorderSettings.mockReset()
  api.recorderDevices.mockReset()
  api.patchRecorderSettings.mockReset()
  api.recorderSettings.mockResolvedValue({
    gatewayUrl: 'ws://recorder.test/ws', speakerName: '', lang: '', version: 1,
    micDeviceId: '', sysDeviceId: '', keepAudio: true, summaryModel: '',
    gatewayToken: '', micDiarize: true, autoFullMinutes: false,
  })
  api.recorderDevices.mockResolvedValue({ mics: [], systems: [] })
  api.patchRecorderSettings.mockImplementation(async (patch) => patch)
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

async function mount() {
  await act(async () => root.render(<SettingsSaveProvider><RecorderSection /></SettingsSaveProvider>))
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

describe('RecorderSection autosave', () => {
  it('removes the local save button and patches only the edited field', async () => {
    await mount()
    expect(Array.from(host.querySelectorAll('button')).some((button) => button.textContent === '保存')).toBe(false)
    const label = Array.from(host.querySelectorAll('label')).find((node) => node.textContent?.includes('保留本地音频'))
    const toggle = label?.querySelector<HTMLButtonElement>('[role="switch"]')
    if (!toggle) throw new Error('keepAudio toggle missing')
    await act(async () => toggle.click())
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(api.patchRecorderSettings).toHaveBeenCalledWith({ keepAudio: false })
  })
})
