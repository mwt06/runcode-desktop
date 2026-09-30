import { describe, expect, it } from 'vitest'
import { clampComposerHeight, composerHeightBounds, DEFAULT_COMPOSER_HEIGHT, draggedComposerHeight, parseComposerHeight } from './resize'

describe('composer height preference', () => {
  it.each([0, -1, NaN, Infinity, -Infinity])('defaults invalid or absent storage (%s)', (stored) => {
    expect(parseComposerHeight(stored)).toBe(DEFAULT_COMPOSER_HEIGHT)
  })
  it('rounds pixel heights and enforces a usable minimum', () => {
    expect(parseComposerHeight(240.7)).toBe(241)
    expect(parseComposerHeight(12)).toBe(64)
  })
  it('keeps the wanted height when a smaller window temporarily clamps it', () => {
    const wanted = parseComposerHeight(360)
    expect(clampComposerHeight(wanted, composerHeightBounds(680, 400))).toBe(240)
    expect(clampComposerHeight(wanted, composerHeightBounds(1000, 700))).toBe(360)
    expect(wanted).toBe(360)
  })
})

describe('composer height bounds', () => {
  it('uses no more than half the viewport', () => {
    expect(composerHeightBounds(820, 700)).toEqual({ min: 64, max: 410 })
  })
  it('reserves chat space after attachments, toolbar, and other chrome', () => {
    expect(composerHeightBounds(820, 400)).toEqual({ min: 64, max: 240 })
    expect(composerHeightBounds(820, 320)).toEqual({ min: 64, max: 160 })
  })
  it('always yields valid bounds, even with too little space', () => {
    expect(composerHeightBounds(100, -20)).toEqual({ min: 64, max: 64 })
  })
  it('rounds the maximum down rather than overflowing the layout', () => {
    expect(composerHeightBounds(821, 400.9).max).toBe(240)
  })
  it('clamps large stored heights before rendering', () => {
    expect(clampComposerHeight(1e100, composerHeightBounds(820, 700))).toBe(410)
  })
})

describe('dragging the upper input edge', () => {
  const bounds = { min: 64, max: 410 }
  it('grows upwards and shrinks downwards', () => {
    expect(draggedComposerHeight(96, 600, 500, bounds)).toBe(196)
    expect(draggedComposerHeight(196, 500, 600, bounds)).toBe(96)
  })
  it('clamps dragging well past either edge without bouncing back to default', () => {
    expect(draggedComposerHeight(96, 600, -1000, bounds)).toBe(410)
    expect(draggedComposerHeight(96, 600, 1000, bounds)).toBe(64)
  })
  it('uses the latest bounds when the viewport changes during a drag', () => {
    expect(draggedComposerHeight(240, 500, 400, { min: 64, max: 180 })).toBe(180)
  })
})
