// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  openArtifact: vi.fn(),
  readArtifact: vi.fn(),
  releaseArtifact: vi.fn(),
}))
vi.mock('@/core/bridge', () => ({
  ...api,
  errText: (e: unknown) => e instanceof Error ? e.message : String(e),
  errCode: (e: unknown) => {
    try { return JSON.parse(String((e as Error).message)).code ?? '' } catch { return '' }
  },
}))

import { InlineFilePreview } from './inline-file-preview'

let host: HTMLDivElement
let root: Root
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.stubGlobal('URL', {
    ...URL,
    createObjectURL: vi.fn(() => 'blob:file-preview'),
    revokeObjectURL: vi.fn(),
  })
  api.openArtifact.mockReset().mockResolvedValue({
    reference: 'ref-svg', sessionId: 'S', generation: 'g', path: 'charts/a.svg', url: 'http://localhost/a.svg',
  })
  api.readArtifact.mockReset().mockResolvedValue('<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>')
  api.releaseArtifact.mockReset().mockResolvedValue(undefined)
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})

it('reads a path-only SVG through the source session reference without opening the side preview', async () => {
  const open = vi.fn()
  await act(async () => root.render(
    <InlineFilePreview path="charts/a.svg" sessionId="S" messageId="m1" previewId="file:charts/a.svg" onOpenCodePreview={open} />,
  ))
  await act(async () => { await Promise.resolve(); await Promise.resolve() })
  expect(api.openArtifact).toHaveBeenCalledWith('S', 'charts/a.svg')
  expect(api.readArtifact).toHaveBeenCalledWith('ref-svg')
  expect(host.querySelector('img')?.getAttribute('src')).toBe('blob:file-preview')
  expect(api.releaseArtifact).toHaveBeenCalledTimes(1)

  await act(async () => (host.querySelector('img') as HTMLImageElement).click())
  expect(open).not.toHaveBeenCalled()
})

it('reports not_found without auto-generating a replacement', async () => {
  api.openArtifact.mockRejectedValueOnce(new Error(JSON.stringify({ code: 'not_found', message: 'gone' })))
  await act(async () => root.render(
    <InlineFilePreview path="charts/missing.svg" sessionId="S" messageId="m1" previewId="file:missing" />,
  ))
  await act(async () => { await Promise.resolve(); await Promise.resolve() })
  expect(host.textContent).toContain('文件不存在')
  expect(host.textContent).toContain('charts/missing.svg')
  expect(api.readArtifact).not.toHaveBeenCalled()
})
