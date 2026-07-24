// 输入框按键的判定：同一次 keydown 要同时兼顾输入法组字、mention 候选框、发送与
// 换行，四者的优先级容易写错（漏一个 shiftKey 就把换行吃掉），所以抽成纯函数单测。
import { isComposingKey, type ComposingKeyEvent } from '@/ui/keys'

export type ComposerKey = ComposingKeyEvent & { key: string; shiftKey: boolean }

// ComposerAction 是这次按键该做什么。'default' 表示不拦截，交给 textarea 本来的
// 行为——换行走的就是这条路，我们不自己插 \n。其余动作调用方一律 preventDefault。
export type ComposerAction =
  | { kind: 'default' }
  | { kind: 'movePicker'; delta: number }
  | { kind: 'pickHighlighted' }
  | { kind: 'closePicker' }
  | { kind: 'send' }

// composerKeyAction 决定一次 keydown 的归属。优先级自上而下：
//
//  1. 组字中的按键属于输入法，一概不拦（Enter 是上屏，上下键是翻候选页）。
//  2. 候选框开着时，方向键/Tab/Escape 归候选框；Enter 也归它，但 Shift+Enter 不归
//     ——用户按 Shift 的意图是换行，候选框开着不该改变这一点。
//  3. 剩下的 Enter 发送，Shift+Enter 落到 default 换行。
export function composerKeyAction(e: ComposerKey, pickerOpen: boolean): ComposerAction {
  if (isComposingKey(e)) return { kind: 'default' }

  const submitEnter = e.key === 'Enter' && !e.shiftKey
  if (pickerOpen) {
    if (e.key === 'ArrowDown') return { kind: 'movePicker', delta: 1 }
    if (e.key === 'ArrowUp') return { kind: 'movePicker', delta: -1 }
    if (submitEnter || e.key === 'Tab') return { kind: 'pickHighlighted' }
    if (e.key === 'Escape') return { kind: 'closePicker' }
  }
  return submitEnter ? { kind: 'send' } : { kind: 'default' }
}
