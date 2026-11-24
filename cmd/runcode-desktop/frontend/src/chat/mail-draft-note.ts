// send_email 草稿就绪后，在助手回复末尾盖「查看草稿」脚注。纯函数，不依赖 React。
import type { Block } from './blocks'
import { parseMailDraftReady } from './mail-draft-actions'

/** 本回合最近一次 draft_ready 的草稿 id；无则 null。 */
export function turnMailDraftId(blocks: Block[]): string | null {
  const lastUser = blocks.map((b) => b.kind).lastIndexOf('user')
  if (lastUser < 0) return null
  let draftId: string | null = null
  for (let i = lastUser + 1; i < blocks.length; i++) {
    const b = blocks[i]
    if (b.kind === 'user') break
    if (b.kind !== 'tool' || b.tool.toolName !== 'send_email') continue
    const ready = parseMailDraftReady(b.tool)
    if (ready?.draftId) draftId = ready.draftId
  }
  return draftId
}

/** 把 mailDraftId 盖到本回合最后一个已完成的助手气泡上。 */
export function stampMailDraftNote(blocks: Block[]): Block[] {
  const draftId = turnMailDraftId(blocks)
  if (!draftId) return blocks
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
  if (target.kind !== 'assistant' || target.mailDraftId === draftId) return blocks
  return blocks.map((b, i) => (i === lastAsst && b.kind === 'assistant' ? { ...b, mailDraftId: draftId } : b))
}

/** 恢复历史后按工具结果重贴「查看草稿」脚注。 */
export function rehydrateMailDraftNotes(blocks: Block[]): Block[] {
  if (blocks.length === 0) return blocks
  let changed = false
  const next = [...blocks]
  let turnStart = -1
  const flush = (endExclusive: number) => {
    let draftId: string | null = null
    for (let i = turnStart + 1; i < endExclusive; i++) {
      const b = next[i]
      if (b.kind === 'tool' && b.tool.toolName === 'send_email') {
        const ready = parseMailDraftReady(b.tool)
        if (ready?.draftId) draftId = ready.draftId
      }
    }
    if (!draftId) return
    for (let i = endExclusive - 1; i > turnStart; i--) {
      const b = next[i]
      if (b.kind === 'assistant' && !b.streaming) {
        if (b.mailDraftId !== draftId) {
          next[i] = { ...b, mailDraftId: draftId }
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
