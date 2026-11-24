// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ImageGenerationArtifact } from '@/core/bridge'

const api = vi.hoisted(() => ({ openArtifact: vi.fn(), releaseArtifact: vi.fn() }))
vi.mock('@/core/bridge', async () => {
  const actual = await vi.importActual<typeof import('@/core/bridge')>('@/core/bridge')
  return { ...actual, ...api }
})

import { GeneratedImageViewerProvider, useGeneratedImageViewer } from './generated-image-viewer-context'

const image = (path: string): ImageGenerationArtifact => ({
  path, mimeType: 'image/png', bytes: 10, width: 64, height: 64,
})

function Probe({ onApi }: { onApi: (api: ReturnType<typeof useGeneratedImageViewer>) => void }) {
  const v = useGeneratedImageViewer()
  onApi(v)
  return <div data-active={v.active ? '1' : '0'} />
}

let host: HTMLDivElement, root: Root
beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  let resolveOpen: ((v: unknown) => void) | null = null
  api.openArtifact.mockReset().mockImplementation(() => new Promise((resolve) => { resolveOpen = resolve; (api as { _resolve?: typeof resolve })._resolve = resolve }))
  api.releaseArtifact.mockReset().mockResolvedValue(undefined)
  void resolveOpen
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

describe('GeneratedImageViewerProvider mutex', () => {
  it('第二次 tryOpen 返回 null；关闭后可再开；旧 token 不能关新实例', async () => {
    let viewer!: ReturnType<typeof useGeneratedImageViewer>
    await act(async () => root.render(
      <GeneratedImageViewerProvider>
        <Probe onApi={(a) => { viewer = a }} />
      </GeneratedImageViewerProvider>,
    ))
    const a = { sessionId: 'S', sessionGeneration: 'g1', toolUseId: 't1', image: image('生成图片/a/image.png'), alt: 'a' }
    const b = { sessionId: 'S', sessionGeneration: 'g1', toolUseId: 't2', image: image('生成图片/b/image.png'), alt: 'b' }
    let h1!: { ownerToken: string } | null
    await act(async () => { h1 = viewer.tryOpen(a, 'view', null) })
    expect(h1).toBeTruthy()
    expect(viewer.tryOpen(b, 'view', null)).toBeNull()
    expect(host.getAttribute('data-active') || host.querySelector('[data-active]')?.getAttribute('data-active')).toBe('1')

    await act(async () => { viewer.closeIfCurrent(h1!.ownerToken) })
    let h2!: { ownerToken: string } | null
    await act(async () => { h2 = viewer.tryOpen(b, 'annotate', null) })
    expect(h2).toBeTruthy()
    await act(async () => { viewer.closeIfCurrent(h1!.ownerToken) })
    expect(host.querySelector('[data-active]')?.getAttribute('data-active')).toBe('1')
    await act(async () => { viewer.closeIfCurrent(h2!.ownerToken) })
    expect(host.querySelector('[data-active]')?.getAttribute('data-active')).toBe('0')
  })
})
