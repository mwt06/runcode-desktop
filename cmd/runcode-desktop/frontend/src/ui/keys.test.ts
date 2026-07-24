import { describe, expect, it } from 'vitest'
import { isComposingKey } from './keys'

const key = (isComposing?: boolean, keyCode?: number) => ({
  nativeEvent: isComposing === undefined ? {} : { isComposing },
  keyCode,
})

describe('isComposingKey', () => {
  it('挡住 Chromium 的组字态：keydown 先于 compositionend，isComposing 为 true', () => {
    expect(isComposingKey(key(true, 13))).toBe(true)
    expect(isComposingKey(key(true, 229))).toBe(true)
  })

  it('挡住 WebKit 的上屏 Enter：compositionend 已过，只剩 keyCode 229', () => {
    // 这一条就是 mac 上"打完中文按回车直接把消息发出去"的那次按键。
    expect(isComposingKey(key(false, 229))).toBe(true)
  })

  it('放行真正的按键', () => {
    expect(isComposingKey(key(false, 13))).toBe(false)
    expect(isComposingKey(key(false, 27))).toBe(false)
  })

  it('两个字段都缺时按非组字处理，不至于让回车永久失灵', () => {
    expect(isComposingKey(key())).toBe(false)
    expect(isComposingKey({ nativeEvent: {} })).toBe(false)
  })
})
