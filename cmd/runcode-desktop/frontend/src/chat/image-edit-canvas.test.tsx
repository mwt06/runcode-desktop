// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  saveImageGenerationEdit: vi.fn(),
  readArtifactBytes: vi.fn(),
}))
vi.mock('@/core/bridge', () => ({
  ...api,
  errText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
}))

import { ImageEditCanvas } from './image-edit-canvas'
import { IMAGE_EDIT_MAX_BYTES } from './image-edit-ops'

let host: HTMLDivElement, root: Root
beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  api.saveImageGenerationEdit.mockReset().mockResolvedValue({
    path: '生成图片/gen_1/edited-20261117-120000001.png', mimeType: 'image/png', bytes: 100, width: 64, height: 64,
  })
  api.readArtifactBytes.mockReset().mockResolvedValue(new Uint8Array([1, 2, 3]).buffer)
  HTMLCanvasElement.prototype.toDataURL = vi.fn(() => 'data:image/png;base64,aaaa')
  HTMLCanvasElement.prototype.getContext = vi.fn(() => ({
    drawImage: vi.fn(),
    clearRect: vi.fn(),
    beginPath: vi.fn(),
    moveTo: vi.fn(),
    lineTo: vi.fn(),
    stroke: vi.fn(),
    fillText: vi.fn(),
    set lineWidth(_v: number) {},
    set strokeStyle(_v: string) {},
    set fillStyle(_v: string) {},
    set font(_v: string) {},
    set lineCap(_v: string) {},
    set lineJoin(_v: string) {},
    set textBaseline(_v: string) {},
  })) as unknown as typeof HTMLCanvasElement.prototype.getContext
  vi.stubGlobal('Image', class {
    onload: null | (() => void) = null
    onerror: null | (() => void) = null
    naturalWidth = 100
    naturalHeight = 100
    set src(_u: string) { queueMicrotask(() => this.onload?.()) }
  })
  vi.stubGlobal('URL', {
    createObjectURL: () => 'blob:mock',
    revokeObjectURL: vi.fn(),
  })
  Element.prototype.getBoundingClientRect = vi.fn(() => ({
    x: 0, y: 0, top: 0, left: 0, bottom: 200, right: 200, width: 200, height: 200, toJSON: () => ({}),
  }))
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

const props = {
  sessionId: 'S',
  sourcePath: '生成图片/gen_1/image.png',
  sourceReference: 'ref-1',
  imageUrl: 'http://127.0.0.1:9/img',
  naturalWidth: 100,
  naturalHeight: 100,
}

describe('ImageEditCanvas', () => {
  it('Escape 对未保存面板提示；确认后关闭', async () => {
    const onClose = vi.fn()
    await act(async () => root.render(
      <ImageEditCanvas {...props} onClose={onClose} onSaved={vi.fn()} />,
    ))
    const stage = host.querySelector('[data-edit-stage]') as HTMLElement
    expect(stage).toBeTruthy()
    await act(async () => {
      stage.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, clientX: 10, clientY: 10, pointerId: 1 }))
      stage.dispatchEvent(new PointerEvent('pointermove', { bubbles: true, clientX: 40, clientY: 40, pointerId: 1 }))
      stage.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, clientX: 40, clientY: 40, pointerId: 1 }))
    })
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(host.textContent).toContain('尚未保存')
    expect(onClose).not.toHaveBeenCalled()
    const confirm = Array.from(host.querySelectorAll('button')).find((b) => b.textContent?.includes('放弃'))
    expect(confirm).toBeTruthy()
    await act(async () => { (confirm as HTMLButtonElement).click() })
    expect(onClose).toHaveBeenCalled()
  })

  it('保存经 reference 读字节并带 sourceReference', async () => {
    const onSaved = vi.fn()
    HTMLCanvasElement.prototype.toDataURL = vi.fn(() => 'data:image/png;base64,' + btoa('png-bytes'))
    await act(async () => root.render(
      <ImageEditCanvas {...props} onClose={vi.fn()} onSaved={onSaved} />,
    ))
    const save = Array.from(host.querySelectorAll('button')).find((b) => b.textContent === '保存副本') as HTMLButtonElement
    await act(async () => { save.click() })
    await act(async () => { await Promise.resolve() })
    await act(async () => { await Promise.resolve() })
    expect(api.readArtifactBytes).toHaveBeenCalledWith('ref-1')
    expect(api.saveImageGenerationEdit).toHaveBeenCalledWith(expect.objectContaining({
      sessionId: 'S',
      sourcePath: '生成图片/gen_1/image.png',
      sourceReference: 'ref-1',
    }))
    expect(onSaved).toHaveBeenCalled()
  })

  it('合成结果超过字节上限时提示且不发送', async () => {
    // atob 长度 ≈ base64字符数 * 3/4；造出解码后超过 16MiB 的串。
    const oversized = 'A'.repeat(Math.ceil(((IMAGE_EDIT_MAX_BYTES + 16) * 4) / 3))
    HTMLCanvasElement.prototype.toDataURL = vi.fn(() => 'data:image/png;base64,' + oversized)
    await act(async () => root.render(
      <ImageEditCanvas {...props} onClose={vi.fn()} onSaved={vi.fn()} />,
    ))
    const save = Array.from(host.querySelectorAll('button')).find((b) => b.textContent === '保存副本') as HTMLButtonElement
    await act(async () => { save.click() })
    for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve() })
    expect(api.saveImageGenerationEdit).not.toHaveBeenCalled()
    expect(host.textContent).toMatch(/超过|太大|上限/)
  })
})
