// 本回合内联预览状态：供自动侧栏在回合结束时跳过「已经在正文里画出来」的同一路径。
// 状态只活在内存里，按会话隔离；不持久化。

export type InlineFileStatus = 'pending' | 'rendered' | 'fallback'

type Entry = { status: InlineFileStatus; at: number }

const bySession = new Map<string, Map<string, Entry>>()

function bag(sessionId: string): Map<string, Entry> {
  let m = bySession.get(sessionId)
  if (!m) {
    m = new Map()
    bySession.set(sessionId, m)
  }
  return m
}

export function noteInlineFile(sessionId: string, relPath: string, status: InlineFileStatus) {
  if (!sessionId || !relPath) return
  bag(sessionId).set(relPath.replace(/\\/g, '/'), { status, at: Date.now() })
}

export function inlineFileStatus(sessionId: string, relPath: string): InlineFileStatus | null {
  return bag(sessionId).get(relPath.replace(/\\/g, '/'))?.status ?? null
}

export function clearInlineFiles(sessionId: string) {
  bySession.delete(sessionId)
}

export function clearAllInlineFiles() {
  bySession.clear()
}

// shouldSkipAutoPreview：回合结束时，已成功内联的路径不再自动打开侧栏。
export function shouldSkipAutoPreview(sessionId: string, relPath: string): boolean {
  return inlineFileStatus(sessionId, relPath) === 'rendered'
}

// waitForInlineSettled 短暂等待 pending 变为 rendered/fallback（方案建议 ≤2s）。
export async function waitForInlineSettled(sessionId: string, relPath: string, ms = 2000): Promise<InlineFileStatus | null> {
  const norm = relPath.replace(/\\/g, '/')
  const start = Date.now()
  while (Date.now() - start < ms) {
    const s = bag(sessionId).get(norm)?.status ?? null
    if (s !== 'pending') return s
    await new Promise((r) => setTimeout(r, 50))
  }
  return bag(sessionId).get(norm)?.status ?? null
}

export function isStaticInlinePath(path: string): boolean {
  const e = path.replace(/\\/g, '/').split('/').pop()?.split('.').pop()?.toLowerCase() ?? ''
  return e === 'svg' || e === 'html' || e === 'htm' || e === 'mmd' || e === 'mermaid'
}
