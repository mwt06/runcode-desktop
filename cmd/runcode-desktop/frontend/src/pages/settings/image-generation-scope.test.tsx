// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { ImageGenerationCatalog, ImageGenerationSettings } from '@/core/bridge'

const api = vi.hoisted(() => ({ listImageGenerationModels: vi.fn(), getImageGenerationSettings: vi.fn(), saveImageGenerationSettings: vi.fn() }))
vi.mock('@/core/bridge', () => ({ ...api, errText: (e: unknown) => String(e) }))
import { ImageGenerationSection } from './image-generation'

let root: Root, host: HTMLDivElement
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  host = document.createElement('div')
  root = createRoot(host)
  vi.resetAllMocks()
  api.listImageGenerationModels.mockImplementation(async (tenantId: string): Promise<ImageGenerationCatalog> => ({
    scopeId: tenantId, models: [{ provider: 'fixture', model: { kind: 'platform', name: tenantId, bridge: 'https://bridge.example', tenantId }, profile: { available: true, supportedSizes: ['1024x1024'], defaultSize: '1024x1024', responseFormats: ['url'] } }],
  }))
  api.getImageGenerationSettings.mockImplementation(async (tenantId: string): Promise<ImageGenerationSettings> => ({ scopeId: tenantId, revision: 0, defaultSize: '' }))
})
afterEach(async () => { await act(async () => root.unmount()) })
async function render(tenantId: string) { await act(async () => root.render(<ImageGenerationSection tenantId={tenantId} />)) }
const model = () => host.querySelector('select')!
async function choose() {
  await act(async () => {
    model().value = model().options[1].value
    model().dispatchEvent(new Event('change', { bubbles: true }))
  })
}
const save = () => [...host.querySelectorAll('button')].find((b) => b.textContent?.startsWith('保存'))!

it.each(['success', 'failure'])('allows tenant B to save while tenant A has a late %s', async (outcome) => {
  let resolve!: (v: ImageGenerationSettings) => void
  let reject!: (e: Error) => void
  api.saveImageGenerationSettings.mockReturnValueOnce(new Promise<ImageGenerationSettings>((ok, fail) => { resolve = ok; reject = fail }))
  await render('A')
  await choose()
  await act(async () => save().click())
  expect(model().disabled).toBe(true)
  await render('B')
  expect(model().disabled).toBe(false)
  await choose()
  expect(save().disabled).toBe(false)
  await act(async () => {
    if (outcome === 'success') resolve({ scopeId: 'A', revision: 1, defaultSize: '' })
    else reject(new Error('old A failed'))
  })
  expect(model().value).toContain('B')
  expect(host.textContent).not.toContain('old A failed')
  expect(host.textContent).not.toContain('已保存')
  api.saveImageGenerationSettings.mockResolvedValueOnce({ scopeId: 'B', revision: 1, defaultSize: '' })
  await act(async () => save().click())
  expect(api.saveImageGenerationSettings.mock.calls[1][0]).toMatchObject({ tenantId: 'B', scopeId: 'B' })
})
