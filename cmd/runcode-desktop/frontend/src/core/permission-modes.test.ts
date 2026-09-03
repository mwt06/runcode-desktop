import { describe, expect, it } from 'vitest'
import {
  ALL_PERMISSION_MODES, DEFAULT_MODE, HIDDEN_PERMISSION_MODES, MODE_LABEL,
  nextMode, offeredModes,
} from './permission-modes'

describe('offeredModes', () => {
  it('隐藏的模式不出现在可选项里', () => {
    expect(offeredModes().map((m) => m.key)).not.toContain('safe')
  })

  it('当前就是隐藏模式时照样列出它', () => {
    // 否则设置页的下拉显示空白，用户一动就把模式改掉了——而他并没有想改。
    // 隐藏是「不主动提供」，不是「假装它不存在」。
    expect(offeredModes('safe').map((m) => m.key)).toContain('safe')
  })

  it('当前是可选模式时不会多列出别的隐藏项', () => {
    expect(offeredModes('judge').map((m) => m.key)).not.toContain('safe')
  })
})

describe('nextMode', () => {
  it('在可选项之间循环，跳过隐藏的', () => {
    expect(nextMode('interactive')).toBe('judge')
    expect(nextMode('judge')).toBe('flight')
    // 转一圈回到第一个可选项——不是 safe。
    expect(nextMode('flight')).toBe('interactive')
  })

  it('从隐藏模式点一下能出来，而且回不去', () => {
    // 旧会话可能停在 safe 上。indexOf 得 -1 → 落到第一个可选项，正是想要的：
    // 一点就出来，出来之后循环里再也没有它。
    expect(nextMode('safe')).toBe('interactive')
    const reachable = new Set<string>()
    let m = nextMode('interactive')
    for (let i = 0; i < ALL_PERMISSION_MODES.length + 2; i++) {
      reachable.add(m)
      m = nextMode(m)
    }
    expect(reachable.has('safe')).toBe(false)
  })

  it('没有当前模式时给出第一个可选项', () => {
    expect(nextMode(undefined)).toBe('interactive')
    expect(nextMode('')).toBe('interactive')
  })
})

describe('标签与默认值', () => {
  it('名字表认得全部模式，包括隐藏的', () => {
    // 会话可能停在隐藏模式上，工具条得如实说它是什么，不能显示成空白或别的模式。
    for (const m of ALL_PERMISSION_MODES) {
      expect(MODE_LABEL[m.key]).toBeTruthy()
    }
    expect(MODE_LABEL.safe).toBe('安全模式')
  })

  it('默认模式与后端 defaultRequest() 一致，且不是被隐藏的那个', () => {
    // 后端 store.go 的 defaultRequest() 写的是 interactive。两边对不上时，
    // 界面显示的模式和会话实际用的模式会差一档，而没有任何东西会报错。
    expect(DEFAULT_MODE).toBe('interactive')
    expect(HIDDEN_PERMISSION_MODES).not.toContain(DEFAULT_MODE)
  })
})
