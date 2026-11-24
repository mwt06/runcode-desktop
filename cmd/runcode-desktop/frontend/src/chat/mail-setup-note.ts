// 未接入邮箱时，在助手回复脚注盖「前往邮箱设置」。纯函数，不依赖 React。
import type { Block } from './blocks'
import { parseMailDraftReady } from './mail-draft-actions'

/** 本回合是否有 send_email 返回 needsMailSetup。 */
export function turnNeedsMailSetup(blocks: Block[]): boolean {
  const lastUser = blocks.map((b) => b.kind).lastIndexOf('user')
  if (lastUser < 0) return false
  for (let i = lastUser + 1; i < blocks.length; i++) {
    const b = blocks[i]
    if (b.kind === 'user') break
    if (b.kind !== 'tool' || b.tool.toolName !== 'send_email') continue
    const ready = parseMailDraftReady(b.tool)
    if (ready?.needsMailSetup) return true
  }
  return false
}

/** 把 mailSetupRequired 盖到本回合最后一个已完成的助手气泡上。 */
export function stampMailSetupRequired(blocks: Block[]): Block[] {
  if (!turnNeedsMailSetup(blocks)) return blocks
  let lastAsst = -1
  const lastUser = blocks.map((b) => b.kind).lastIndexOf('user')
  for (let i = blocks.length - 1; i > lastUser; i--) {
    const b = blocks[i]
    if (b.kind === 'assistant' && !b.streaming) {
      lastAsst = i
      break
    }
  }
  if (lastAsst < 0) return blocks
  const target = blocks[lastAsst]
  if (target.kind !== 'assistant' || target.mailSetupRequired) return blocks
  return blocks.map((b, i) => (i === lastAsst && b.kind === 'assistant' ? { ...b, mailSetupRequired: true } : b))
}

/** 恢复历史后按工具结果重贴脚注（每个含 needsMailSetup 的回合贴一次）。 */
export function rehydrateMailSetupNotes(blocks: Block[]): Block[] {
  if (blocks.length === 0) return blocks
  let changed = false
  const next = [...blocks]
  let turnStart = -1
  const flush = (endExclusive: number) => {
    let needs = false
    for (let i = turnStart + 1; i < endExclusive; i++) {
      const b = next[i]
      if (b.kind === 'tool' && b.tool.toolName === 'send_email' && parseMailDraftReady(b.tool)?.needsMailSetup) {
        needs = true
        break
      }
    }
    if (!needs) return
    for (let i = endExclusive - 1; i > turnStart; i--) {
      const b = next[i]
      if (b.kind === 'assistant' && !b.streaming) {
        if (!b.mailSetupRequired) {
          next[i] = { ...b, mailSetupRequired: true }
          changed = true
        }
        return
      }
    }
  }
  for (let i = 0; i < next.length; i++) {
    if (next[i].kind === 'user') {
      flush(i)
      turnStart = i
    }
  }
  flush(next.length)
  return changed ? next : blocks
}
