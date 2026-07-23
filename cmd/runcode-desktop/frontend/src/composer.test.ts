import { describe, it, expect } from 'vitest'
import { computeMention } from './composer'

// computeMention 是 composer 三种触发（@技能 / /子代理 / #文件）的解析核心：
// '@' 与 '/' 只在输入最开头生效，'#' 可出现在任意空白之后；触发符到光标之间
// 不得有空白。此前它是零测试的纯函数（审核遗留项）。
describe('computeMention', () => {
  it('recognizes @ at the start of input', () => {
    expect(computeMention('@ski', 4)).toEqual({ query: 'ski', start: 0, trigger: '@' })
    expect(computeMention('@', 1)).toEqual({ query: '', start: 0, trigger: '@' })
  })
  it('recognizes / at the start of input', () => {
    expect(computeMention('/agent', 6)).toEqual({ query: 'agent', start: 0, trigger: '/' })
  })
  it('deactivates @ and / once the command contains whitespace', () => {
    expect(computeMention('@sk i', 5)).toBeNull()
    expect(computeMention('/a b', 4)).toBeNull()
  })
  it('does not treat mid-input @ or / as a trigger', () => {
    expect(computeMention('hi @x', 5)).toBeNull()
    expect(computeMention('a/b', 3)).toBeNull()
  })
  it('recognizes # at the start and after whitespace', () => {
    expect(computeMention('#a', 2)).toEqual({ query: 'a', start: 0, trigger: '#' })
    expect(computeMention('fix #main', 9)).toEqual({ query: 'main', start: 4, trigger: '#' })
  })
  it('rejects # glued to a word (no preceding whitespace)', () => {
    expect(computeMention('foo#bar', 7)).toBeNull()
  })
  it('deactivates # once the query contains whitespace', () => {
    expect(computeMention('see #a b', 8)).toBeNull()
  })
  it('uses only text up to the cursor', () => {
    expect(computeMention('#abcd', 3)).toEqual({ query: 'ab', start: 0, trigger: '#' })
    expect(computeMention('#abc', 0)).toBeNull() // 光标在触发符之前 = 无活动触发
  })
  it('lets a later # trigger win when the leading @ has gone stale', () => {
    expect(computeMention('@x #y', 5)).toEqual({ query: 'y', start: 3, trigger: '#' })
  })
})
