import { describe, expect, it } from 'vitest'
import { type Block } from '@/chat/blocks'
import {
  convOf, dropConv, emptyConversation, lastUserText, patchConv, withReverted,
  type ConvMap,
} from './conversation-state'

const busy = (b: boolean) => (s: typeof emptyConversation) => ({ ...s, busy: b })

describe('patchConv', () => {
  it('只改目标会话，别人原样不动', () => {
    const before: ConvMap = {
      a: { ...emptyConversation, ctxTokens: 10 },
      b: { ...emptyConversation, ctxTokens: 20 },
    }
    const after = patchConv(before, 'a', busy(true))
    expect(after.a.busy).toBe(true)
    expect(after.b).toBe(before.b) // 同一个对象引用：React 能据此跳过重渲染
    expect(before.a.busy).toBe(false) // 入参不被改动
  })

  it('会话还没有条目时按空状态起一条', () => {
    // 事件可能先于「会话已建立」的往返到达，丢掉它们会让第一个 delta 凭空消失。
    const after = patchConv({}, 'fresh', busy(true))
    expect(after.fresh.busy).toBe(true)
    expect(after.fresh.blocks).toEqual([])
  })

  it('空 id 原样返回——进程级事件不属于任何一条对话', () => {
    const before: ConvMap = { a: emptyConversation }
    expect(patchConv(before, '', busy(true))).toBe(before)
  })

  it('fn 原样返回时不产生新表，避免无谓重渲染', () => {
    const before: ConvMap = { a: emptyConversation }
    expect(patchConv(before, 'a', (s) => s)).toBe(before)
  })
})

describe('convOf', () => {
  it('没有条目时给同一个空状态对象', () => {
    expect(convOf({}, 'nope')).toBe(emptyConversation)
    expect(convOf({}, '')).toBe(emptyConversation)
  })

  it('有条目时给那一条', () => {
    const a = { ...emptyConversation, ctxTokens: 7 }
    expect(convOf({ a }, 'a')).toBe(a)
  })
})

describe('dropConv', () => {
  it('删掉指定会话，别人不动', () => {
    const before: ConvMap = { a: emptyConversation, b: emptyConversation }
    const after = dropConv(before, 'a')
    expect('a' in after).toBe(false)
    expect(after.b).toBe(before.b)
    expect('a' in before).toBe(true) // 入参不被改动
  })

  it('删不存在的会话时原样返回', () => {
    const before: ConvMap = { a: emptyConversation }
    expect(dropConv(before, 'nope')).toBe(before)
    expect(dropConv(before, '')).toBe(before)
  })
})

describe('lastUserText', () => {
  const withBlocks = (...blocks: Block[]) => ({ ...emptyConversation, blocks })
  const user = (text: string): Block => ({ kind: 'user', id: text, text, ts: '' })

  it('取最近一次提问，不是最早那次', () => {
    const s = withBlocks(user('第一个问题'), user('第二个问题'))
    expect(lastUserText(s)).toBe('第二个问题')
  })

  it('跳过助手与工具块', () => {
    const s = withBlocks(
      user('改一下配置'),
      { kind: 'assistant', id: 'a', text: '好的', streaming: false, ts: '' },
      { kind: 'error', id: 'e', text: '出错了' },
    )
    expect(lastUserText(s)).toBe('改一下配置')
  })

  it('空文本的用户块跳过，继续往前找', () => {
    // 只带附件的消息文本是空的，一行空白当不了标题。
    const s = withBlocks(user('看看这个文件'), user('   '))
    expect(lastUserText(s)).toBe('看看这个文件')
  })

  it('压成单行', () => {
    expect(lastUserText(withBlocks(user('  第一行\n\n第二行  ')))).toBe('第一行 第二行')
  })

  it('超长截断并加省略号', () => {
    const got = lastUserText(withBlocks(user('问'.repeat(100))), 10)
    expect(got).toBe('问'.repeat(10) + '…')
  })

  it('截断不会把 emoji 劈成半个', () => {
    // 切点正好落在代理对中间：宁可少一个字，也不要一个 U+FFFD。
    const got = lastUserText(withBlocks(user('ab😀cd')), 3)
    expect(got).toBe('ab…')
  })

  it('没有用户块时给空串——调用方据此回落到「新对话」', () => {
    expect(lastUserText(emptyConversation)).toBe('')
  })
})

describe('withReverted', () => {
  it('加进已撤销集合，返回新对象', () => {
    const s = withReverted(emptyConversation, 'snap1')
    expect(s.revertedEdits.has('snap1')).toBe(true)
    expect(emptyConversation.revertedEdits.has('snap1')).toBe(false)
  })

  it('已经在里面时原样返回，不制造新对象', () => {
    const s = withReverted(emptyConversation, 'snap1')
    expect(withReverted(s, 'snap1')).toBe(s)
  })
})
