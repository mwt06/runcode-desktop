import type { StaticPreviewKind } from './static-detect'

export type CodePreviewTarget =
  | {
      kind: 'file'
      relPath: string
      ownerKey: string
    }
  | {
      kind: 'inline-static'
      ownerKey: string
      title: string
      sourceKind: StaticPreviewKind
      source: string
      messageId: string
    }

// 代码渲染图只选择文件预览或内存静态预览，不能落到外部生图或标注链路。
export function codePreviewTarget(input: {
  kind: StaticPreviewKind
  source: string
  title: string
  sessionId?: string
  messageId?: string
  previewId?: string
  relatedPaths?: string[]
}): CodePreviewTarget {
  const sessionID = input.sessionId || 'local'
  const messageID = input.messageId || 'message'
  const previewID = input.previewId || 'preview'
  const paths = input.relatedPaths ?? []
  const file = input.kind !== 'mermaid' && paths.length === 1 ? paths[0] : ''
  if (file) {
    return {
      kind: 'file',
      relPath: file,
      ownerKey: `file:${sessionID}:${file}`,
    }
  }
  return {
    kind: 'inline-static',
    ownerKey: `inline:${sessionID}:${messageID}:${previewID}`,
    title: input.title,
    sourceKind: input.kind,
    source: input.source,
    messageId: messageID,
  }
}
