// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { appendStroke, appendText, initialEditState, projectImageEditOperations } from './image-edit-ops'

const api = vi.hoisted(() => ({ readArtifactBytes: vi.fn() }))
vi.mock('@/core/bridge', () => ({ readArtifactBytes: api.readArtifactBytes }))

import { composeImageEditPng } from './image-edit-export'

describe('projectImageEditOperations', () => {
  it('三比例与小图有确定线宽／字号下限', () => {
    let s = initialEditState('#E03131')
    s = appendStroke(s, [{ x: 0.1, y: 0.2 }, { x: 0.5, y: 0.6 }], 0.012)
    s = appendText(s, '测', 0.25, 0.4, 0.045)
    const wide = projectImageEditOperations(s.operations, 1280, 720)
    const stroke = wide.find((o) => o.kind === 'stroke')!
    const text = wide.find((o) => o.kind === 'text')!
    expect(stroke.kind).toBe('stroke')
    if (stroke.kind === 'stroke') {
      expect(stroke.points[0]).toEqual({ x: 128, y: 144 })
      expect(stroke.lineWidth).toBe(Math.max(1, 0.012 * 720))
    }
    if (text.kind === 'text') {
      expect(text.fontSize).toBe(Math.max(12, 0.045 * 720))
      expect(text.x).toBe(320)
    }
    const tiny = projectImageEditOperations(s.operations, 32, 16)
    const tStroke = tiny.find((o) => o.kind === 'stroke')!
    if (tStroke.kind === 'stroke') expect(tStroke.lineWidth).toBe(1)
    const tText = tiny.find((o) => o.kind === 'text')!
    if (tText.kind === 'text') expect(tText.fontSize).toBe(12)
  })
})

describe('composeImageEditPng', () => {
  beforeEach(() => {
    api.readArtifactBytes.mockReset().mockResolvedValue(new Uint8Array([9, 9]).buffer)
    HTMLCanvasElement.prototype.toDataURL = vi.fn(() => 'data:image/png;base64,' + btoa('ok'))
    HTMLCanvasElement.prototype.getContext = vi.fn(() => ({
      drawImage: vi.fn(), beginPath: vi.fn(), moveTo: vi.fn(), lineTo: vi.fn(), stroke: vi.fn(), fillText: vi.fn(),
      set lineWidth(_v: number) {}, set strokeStyle(_v: string) {}, set fillStyle(_v: string) {},
      set font(_v: string) {}, set lineCap(_v: string) {}, set lineJoin(_v: string) {}, set textBaseline(_v: string) {},
    })) as unknown as typeof HTMLCanvasElement.prototype.getContext
    vi.stubGlobal('Image', class {
      onload: null | (() => void) = null
      naturalWidth = 64
      naturalHeight = 64
      set src(_u: string) { queueMicrotask(() => this.onload?.()) }
    })
    vi.stubGlobal('URL', { createObjectURL: () => 'blob:x', revokeObjectURL: vi.fn() })
  })

  it('读取 reference 而非 URL；成功后 revoke Blob', async () => {
    const revoke = vi.fn()
    vi.stubGlobal('URL', { createObjectURL: () => 'blob:x', revokeObjectURL: revoke })
    const out = await composeImageEditPng('ref-abc', initialEditState())
    expect(api.readArtifactBytes).toHaveBeenCalledWith('ref-abc')
    expect(out.bytes).toBe(2)
    expect(revoke).toHaveBeenCalledWith('blob:x')
  })
})
