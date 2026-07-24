import { describe, expect, it } from 'vitest'
import { composerKeyAction, type ComposerKey } from './keymap'

const press = (key: string, over: Partial<ComposerKey> = {}): ComposerKey => ({
  key,
  shiftKey: false,
  keyCode: undefined,
  nativeEvent: {},
  ...over,
})

describe('composerKeyAction 换行与发送', () => {
  it('Enter 发送，Shift+Enter 交给 textarea 换行', () => {
    expect(composerKeyAction(press('Enter'), false)).toEqual({ kind: 'send' })
    expect(composerKeyAction(press('Enter', { shiftKey: true }), false)).toEqual({ kind: 'default' })
  })

  it('候选框开着时 Shift+Enter 仍然换行，不选候选项', () => {
    // 这是修掉的缺陷：# / @ / 斜杠候选框开着时，Enter 分支原先没判 shiftKey，
    // 于是 Shift+Enter 会把高亮候选项插进来，换行没了。
    expect(composerKeyAction(press('Enter', { shiftKey: true }), true)).toEqual({ kind: 'default' })
    expect(composerKeyAction(press('Enter'), true)).toEqual({ kind: 'pickHighlighted' })
  })

  it('普通字符一律不拦', () => {
    expect(composerKeyAction(press('a'), false)).toEqual({ kind: 'default' })
    expect(composerKeyAction(press('a'), true)).toEqual({ kind: 'default' })
  })
})

describe('composerKeyAction 候选框', () => {
  it('方向键只在候选框开着时归候选框', () => {
    expect(composerKeyAction(press('ArrowDown'), true)).toEqual({ kind: 'movePicker', delta: 1 })
    expect(composerKeyAction(press('ArrowUp'), true)).toEqual({ kind: 'movePicker', delta: -1 })
    expect(composerKeyAction(press('ArrowDown'), false)).toEqual({ kind: 'default' })
    expect(composerKeyAction(press('ArrowUp'), false)).toEqual({ kind: 'default' })
  })

  it('Tab 选中、Escape 关闭，都只在候选框开着时生效', () => {
    expect(composerKeyAction(press('Tab'), true)).toEqual({ kind: 'pickHighlighted' })
    expect(composerKeyAction(press('Escape'), true)).toEqual({ kind: 'closePicker' })
    expect(composerKeyAction(press('Tab'), false)).toEqual({ kind: 'default' })
    expect(composerKeyAction(press('Escape'), false)).toEqual({ kind: 'default' })
  })
})

describe('composerKeyAction 输入法组字', () => {
  const composing = { nativeEvent: { isComposing: true } }
  const webkitCommit = { keyCode: 229 }

  it('组字中的 Enter 既不发送也不选候选项', () => {
    expect(composerKeyAction(press('Enter', composing), false)).toEqual({ kind: 'default' })
    expect(composerKeyAction(press('Enter', composing), true)).toEqual({ kind: 'default' })
    // WebKit（mac）上屏的那次 Enter：isComposing 已是 false，只剩 keyCode 229。
    expect(composerKeyAction(press('Enter', webkitCommit), false)).toEqual({ kind: 'default' })
    expect(composerKeyAction(press('Enter', webkitCommit), true)).toEqual({ kind: 'default' })
  })

  it('组字中的上下键留给输入法翻候选页', () => {
    expect(composerKeyAction(press('ArrowDown', composing), true)).toEqual({ kind: 'default' })
    expect(composerKeyAction(press('ArrowUp', composing), true)).toEqual({ kind: 'default' })
  })
})
