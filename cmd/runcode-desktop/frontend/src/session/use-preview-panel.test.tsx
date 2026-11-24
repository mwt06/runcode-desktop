// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { ArtifactReference, PreviewLifecycle } from '@/core/protocol/types'

const api = vi.hoisted(() => ({
  openArtifact: vi.fn(), releaseArtifact: vi.fn(), openExternal: vi.fn(),
  lifecycle: null as ((event: PreviewLifecycle) => void) | null,
}))
vi.mock('@/core/bridge', () => ({ ...api, Events: { PreviewLifecycle: 'preview:lifecycle' },
  onEvent: (_name: string, cb: (event: PreviewLifecycle) => void) => { api.lifecycle = cb; return () => { api.lifecycle = null } },
  errText: String, copyText: vi.fn(), resolveArtifactPath: vi.fn(), revealInFolder: vi.fn(),
}))
import { usePreviewPanel, type PreviewPanelState } from './use-preview-panel'

let root: Root, panel: PreviewPanelState
const ref = (sessionId: string, generation = 'g1', reference = sessionId + generation): ArtifactReference => ({ sessionId, generation, reference, path: 'report.html', url: 'http://localhost/' + reference })
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.resetAllMocks()
  localStorage.clear()
  api.lifecycle = null
  api.releaseArtifact.mockResolvedValue(undefined)
  root = createRoot(document.createElement('div'))
})
afterEach(async () => { await act(async () => root.unmount()) })
function Harness({ id }: { id: string }) { panel = usePreviewPanel(id); return null }
async function render(id: string) { await act(async () => root.render(<Harness id={id} />)) }
async function event(sessionId: string, phase: string, generation?: string) {
  await act(async () => api.lifecycle?.({ sessionId, phase, generation }))
}

it('closes invalidated tabs for A, releases references, and preserves B', async () => {
  await render('A')
  await act(async () => panel.openReference(ref('A')))
  await render('B')
  await act(async () => panel.openReference(ref('B')))
  await event('A', 'invalidating')
  expect(panel.tabs).toHaveLength(1)
  expect(api.releaseArtifact).toHaveBeenCalledWith('Ag1')
  expect(api.releaseArtifact).not.toHaveBeenCalledWith('Bg1')
  await render('A')
  expect(panel.tabs).toHaveLength(0)
  await event('A', 'ready', 'g2')
  await act(async () => panel.openReference(ref('A', 'g1', 'stale')))
  expect(panel.tabs).toHaveLength(0)
  expect(api.releaseArtifact).toHaveBeenCalledWith('stale')
  await act(async () => panel.openReference(ref('A', 'g2')))
  expect(panel.tabs).toHaveLength(1)
  await event('A', 'closed')
  expect(panel.tabs).toHaveLength(0)
  expect(api.releaseArtifact).toHaveBeenCalledWith('Ag2')
})

it('discards a pending preview after invalidation even when ready has arrived', async () => {
  let resolve!: (value: ArtifactReference) => void
  api.openArtifact.mockReturnValueOnce(new Promise<ArtifactReference>((ok) => { resolve = ok }))
  await render('A')
  let pending!: Promise<boolean>
  await act(async () => { pending = panel.openFile('report.html') })
  await event('A', 'invalidating')
  await event('A', 'ready', 'g2')
  await act(async () => { resolve(ref('A')); expect(await pending).toBe(false) })
  expect(panel.tabs).toHaveLength(0)
  expect(api.releaseArtifact).toHaveBeenCalledWith('Ag1')
  api.openArtifact.mockResolvedValueOnce(ref('A', 'g2'))
  await act(async () => { expect(await panel.openFile('report.html')).toBe(true) })
  expect(panel.tabs).toHaveLength(1)
})

it('discards pending external actions and previews on lifecycle change or unmount', async () => {
  let resolve!: (value: ArtifactReference) => void
  api.openArtifact.mockReturnValueOnce(new Promise<ArtifactReference>((ok) => { resolve = ok }))
  await render('A')
  let pending!: Promise<void>
  await act(async () => { pending = panel.actions.external('report.html') })
  await event('A', 'closed')
  await act(async () => { resolve(ref('A')); await pending })
  expect(api.openExternal).not.toHaveBeenCalled()
  expect(api.releaseArtifact).toHaveBeenCalledWith('Ag1')
  await event('A', 'ready', 'g2')
  api.openArtifact.mockReturnValueOnce(new Promise<ArtifactReference>((ok) => { resolve = ok }))
  let preview!: Promise<boolean>
  await act(async () => { preview = panel.openFile('report.html') })
  await act(async () => root.unmount())
  resolve(ref('A', 'g2'))
  expect(await preview).toBe(false)
  expect(api.releaseArtifact).toHaveBeenCalledWith('Ag2')
})

it('opens and updates a code-only static tab without requesting an artifact', async () => {
  await render('A')
  await act(async () => (panel as any).openCodePreview({
    kind: 'inline-static',
    ownerKey: 'inline:A:m1:0:10',
    title: 'Mermaid 预览',
    sourceKind: 'mermaid',
    source: 'graph TD;A-->B',
    messageId: 'm1',
  }))
  expect(api.openArtifact).not.toHaveBeenCalled()
  expect(panel.tabs).toHaveLength(1)
  expect(panel.tabs[0]).toMatchObject({
    kind: 'inline-static',
    ownerKey: 'inline:A:m1:0:10',
    source: 'graph TD;A-->B',
  })

  await act(async () => (panel as any).openCodePreview({
    kind: 'inline-static',
    ownerKey: 'inline:A:m1:0:10',
    title: 'Mermaid 预览',
    sourceKind: 'mermaid',
    source: 'graph TD;A-->C',
    messageId: 'm1',
  }))
  expect(panel.tabs).toHaveLength(1)
  expect(panel.tabs[0]).toMatchObject({ source: 'graph TD;A-->C' })
})

it('routes a code preview with a workspace file through the artifact-backed file tab', async () => {
  api.openArtifact.mockResolvedValue({ ...ref('A'), path: 'charts/a.svg' })
  await render('A')
  await act(async () => {
    ;(panel as any).openCodePreview({
      kind: 'file',
      relPath: 'charts/a.svg',
      ownerKey: 'file:A:charts/a.svg',
    })
    await Promise.resolve()
  })
  expect(api.openArtifact).toHaveBeenCalledWith('A', 'charts/a.svg')
  expect(panel.tabs[0]).toMatchObject({ kind: 'file', relPath: 'charts/a.svg' })
})
