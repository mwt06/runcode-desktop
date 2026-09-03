// 一条对话的状态，以及"按会话 id 存一份"的表。
//
// 为什么要拆出来：引擎本来就是多会话的，每条信封都带 sessionId（core/protocol
// 的 onEnvelope），而这里原先是一份全局状态——所有会话的事件都往同一个地方塞。
// 单会话时看不出问题，多会话时后台会话的输出会记到前台会话头上。
//
// 这个模块是纯的（没有 React、没有 Wails），所以折叠规则可以单独测；use-conversation
// 只负责订阅事件、把信封按 id 派发进来，以及把聚焦会话那一份交给界面渲染。
import { type Block } from '@/chat/blocks'
import { type PlanSnapshot } from '@/core/bridge'

/** ConversationState 是**一条**对话的全部可渲染状态。 */
export interface ConversationState {
  /** blocks 是消息流：用户气泡、助手回复、工具卡片、错误与警告。 */
  blocks: Block[]
  /** harmAllows 把 tool-use id 映到智能模式自动放行的理由，供工具卡标注。 */
  harmAllows: Record<string, string>
  /** busy 表示这条会话有回合在跑。**每会话一份**是并行的前提：后台会话在跑
   *  不该让前台的输入框变成"回合进行中"。 */
  busy: boolean
  /** plan 是最近一次 TodoWrite 快照（顶部进度胶囊），null 表示模型还没记过。 */
  plan: PlanSnapshot | null
  /** ctxTokens / ctxEstimated 是上下文占用；estimated 表示这是恢复会话时的估算值，
   *  等这条会话自己的第一次 context:usage 到达就换成实测。 */
  ctxTokens: number
  ctxEstimated: boolean
  /** compacting 表示正在手动压缩。 */
  compacting: boolean
  /** revertedEdits 是已撤销的编辑快照 id，决定「已编辑」卡片画成灰的还是可操作的。 */
  revertedEdits: Set<string>
}

export const emptyConversation: ConversationState = {
  blocks: [],
  harmAllows: {},
  busy: false,
  plan: null,
  ctxTokens: 0,
  ctxEstimated: false,
  compacting: false,
  revertedEdits: new Set(),
}

/**
 * sessionOf 取一条信封属于哪个会话。
 *
 * 线上的 sessionId 是可选字段：进程级事件（passport:changed 之类）不属于任何一条
 * 对话，那里是空的。归一成空串之后，patchConv 会原样跳过它们——好过在每个处理器
 * 里散落 `?? ''`，也好过让类型检查逼出一堆非空断言。
 */
export function sessionOf(env: { sessionId?: string }): string {
  return env.sessionId ?? ''
}

/** ConvMap 是会话 id → 该会话的状态。 */
export type ConvMap = Record<string, ConversationState>

/**
 * patchConv 用 fn 改一条会话的状态，返回新表（不改入参）。
 *
 * 会话还没有条目时按空状态起一条——事件可能先于「会话已建立」的往返到达，
 * 丢掉它们会让第一个 delta 凭空消失。
 *
 * id 为空串时原样返回：进程级事件（passport:changed 之类）的信封 sessionId 是空的，
 * 它们不属于任何一条对话，不该凭空造出一条。
 */
export function patchConv(
  map: ConvMap,
  id: string,
  fn: (s: ConversationState) => ConversationState,
): ConvMap {
  if (!id) return map
  const cur = map[id] ?? emptyConversation
  const next = fn(cur)
  if (next === cur) return map
  return { ...map, [id]: next }
}

/** convOf 取一条会话的状态；没有就给空状态（同一个对象，便于引用比较）。 */
export function convOf(map: ConvMap, id: string): ConversationState {
  return map[id] ?? emptyConversation
}

/**
 * dropConv 删掉一条会话的状态。会话被关闭或删除时调用——留着就是纯占内存，
 * 而且会让"这条会话曾经在跑"的 busy 永远挂在表里。
 */
export function dropConv(map: ConvMap, id: string): ConvMap {
  if (!id || !(id in map)) return map
  const next = { ...map }
  delete next[id]
  return next
}

/**
 * lastUserText 取一条会话最近一次用户提问，压成单行并截断——**自动标题还没生成
 * 时**，会话列表拿它当临时标题。
 *
 * 为什么不由后端给：自动标题是每个回合结束后才由模型生成的（session:renamed），
 * 而「打开中」那一栏最需要认出哪条是哪条的时刻，恰恰是回合正在跑的时候。这一份
 * 前端本来就有，用户一按发送就在，比新加一个后端字段再想办法及时刷新简单得多。
 *
 * 空文本的用户块（只带附件的那种）跳过，继续往前找：一行空白当不了标题。
 */
export function lastUserText(s: ConversationState, max = 48): string {
  for (let i = s.blocks.length - 1; i >= 0; i--) {
    const b = s.blocks[i]
    if (b.kind !== 'user') continue
    // 先粗切一刀再压空白：粘进来的几万字不必整段扫一遍。
    const line = b.text.slice(0, max * 8).replace(/\s+/g, ' ').trim()
    if (!line) continue
    if (line.length <= max) return line
    // 末尾若剩了代理对的前半个，去掉——否则会渲染成一个 U+FFFD。
    return line.slice(0, max).replace(/[\uD800-\uDBFF]$/, '') + '…'
  }
  return ''
}

/** withReverted 往已撤销集合里加一个快照 id，返回新的状态。 */
export function withReverted(s: ConversationState, snapshotID: string): ConversationState {
  if (s.revertedEdits.has(snapshotID)) return s
  const next = new Set(s.revertedEdits)
  next.add(snapshotID)
  return { ...s, revertedEdits: next }
}
