import { describe, expect, it } from 'vitest'
import { canRemember, decisionOptions } from './permission-decisions'

describe('decisionOptions', () => {
  it('缺失或空清单当作未声明，给全部四个（老后端的行为）', () => {
    expect(decisionOptions(undefined)).toEqual(['allow-session', 'allow-once', 'allow-project', 'deny'])
    expect(decisionOptions(null)).toEqual(['allow-session', 'allow-once', 'allow-project', 'deny'])
    expect(decisionOptions([])).toEqual(['allow-session', 'allow-once', 'allow-project', 'deny'])
  })

  it('不可记住的请求只剩仅此一次与拒绝', () => {
    expect(decisionOptions(['allow-once', 'deny'])).toEqual(['allow-once', 'deny'])
  })

  it('按显示顺序输出，不跟随后端清单的顺序', () => {
    expect(decisionOptions(['deny', 'allow-project', 'allow-once', 'allow-session'])).toEqual([
      'allow-session',
      'allow-once',
      'allow-project',
      'deny',
    ])
  })

  it('拒绝永远在：被限制的是允许能记多远，不是能不能拒绝', () => {
    expect(decisionOptions(['allow-once'])).toEqual(['allow-once', 'deny'])
  })

  it('不认识的答案丢掉——画不出来的按钮留在清单里也没人能选', () => {
    expect(decisionOptions(['allow-once', 'allow-forever'])).toEqual(['allow-once', 'deny'])
  })
})

describe('canRemember', () => {
  it('有会话或项目级允许才算能记住', () => {
    expect(canRemember(['allow-session', 'allow-once', 'allow-project', 'deny'])).toBe(true)
    expect(canRemember(['allow-project', 'deny'])).toBe(true)
    expect(canRemember(['allow-once', 'deny'])).toBe(false)
  })
})
