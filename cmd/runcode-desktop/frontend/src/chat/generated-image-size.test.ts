import { describe, expect, it } from 'vitest'
import type { ToolEvent } from '@/core/bridge'
import {
  computePreviewDisplaySize,
  parseSizeHint,
  resolvePreviewGeometry,
} from './generated-image-size'

describe('computePreviewDisplaySize', () => {
  it('三比例在常规上限下等比缩放且不放大', () => {
    expect(computePreviewDisplaySize(1024, 1024, 800, 900)).toEqual({ width: 560, height: 560, provisional: false })
    expect(computePreviewDisplaySize(1280, 720, 800, 900)).toEqual({ width: 560, height: 315, provisional: false })
    const portrait = computePreviewDisplaySize(720, 1280, 800, 900)
    expect(portrait.height).toBe(560)
    expect(portrait.width).toBe(Math.round(720 * (560 / 1280)))
    expect(computePreviewDisplaySize(200, 100, 800, 900)).toEqual({ width: 200, height: 100, provisional: false })
  })
})

describe('parseSizeHint', () => {
  it('从工具输入或结果读取 1024x1024 尺寸', () => {
    expect(parseSizeHint({ input: { size: '1024x1024' } } as ToolEvent)).toEqual({ w: 1024, h: 1024 })
    expect(parseSizeHint({ data: { size: '1280×720' } } as ToolEvent)).toEqual({ w: 1280, h: 720 })
    expect(parseSizeHint({ input: { size: 'auto' } } as ToolEvent)).toBeNull()
  })
})

describe('resolvePreviewGeometry', () => {
  it('running 时接受尺寸提示，完成后冻结几何', () => {
    const first = resolvePreviewGeometry(null, {
      key: 'S|tool-1', width: 1024, height: 1024, running: true, hint: null,
    })
    const hinted = resolvePreviewGeometry(first, {
      key: 'S|tool-1', width: 1280, height: 720, running: true, hint: { w: 1280, h: 720 },
    })
    const completed = resolvePreviewGeometry(hinted, {
      key: 'S|tool-1', width: 1600, height: 900, running: false, hint: { w: 1600, h: 900 },
    })
    expect(hinted).toMatchObject({ width: 1280, height: 720, locked: false })
    expect(completed).toMatchObject({ width: 1280, height: 720, locked: true })
  })
})
