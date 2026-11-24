import { describe, expect, it } from 'vitest'
import { htmlZoomScrollLeft } from './static-detect'

describe('HTML preview center zoom', () => {
  it('centers overflowing content when zooming out of a fitted page', () => {
    expect(htmlZoomScrollLeft({ previousWidth: 390, previousViewport: 780, scrollLeft: 0, width: 975, viewport: 780 })).toBe(97.5)
  })

  it('keeps the same content point at the center after zooming', () => {
    expect(htmlZoomScrollLeft({ previousWidth: 975, previousViewport: 780, scrollLeft: 97.5, width: 1170, viewport: 780 })).toBe(195)
    // 用户先横向移动过，缩放应保留当前中心，不跳回页面正中。
    expect(htmlZoomScrollLeft({ previousWidth: 975, previousViewport: 780, scrollLeft: 195, width: 1170, viewport: 780 })).toBe(312)
  })

  it('restores centered margins once the page fits and handles viewport resizing', () => {
    expect(htmlZoomScrollLeft({ previousWidth: 975, previousViewport: 780, scrollLeft: 195, width: 195, viewport: 780 })).toBe(0)
    expect(htmlZoomScrollLeft({ previousWidth: 975, previousViewport: 780, scrollLeft: 97.5, width: 975, viewport: 320 })).toBe(327.5)
  })

  it('clamps stale scroll positions and invalid geometry to a reachable range', () => {
    expect(htmlZoomScrollLeft({ previousWidth: 975, previousViewport: 780, scrollLeft: 9999, width: 1170, viewport: 780 })).toBe(390)
    expect(htmlZoomScrollLeft({ previousWidth: 0, previousViewport: 0, scrollLeft: 0, width: 975, viewport: 780 })).toBe(97.5)
    expect(htmlZoomScrollLeft({ previousWidth: Number.NaN, previousViewport: 0, scrollLeft: Number.NaN, width: 0, viewport: 780 })).toBe(0)
  })
})
