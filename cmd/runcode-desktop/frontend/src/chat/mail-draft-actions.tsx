import { createContext, useContext } from 'react'

// 聊天工具卡「编辑/查看草稿」→ 打开邮件页指定草稿；未接入邮箱时打开设置。由 App 注入。
export type MailDraftActions = {
  openDraft: (draftId: string) => void
  openMailSettings: () => void
}

const noop: MailDraftActions = { openDraft: () => {}, openMailSettings: () => {} }

export const MailDraftActionsContext = createContext<MailDraftActions>(noop)

export function useMailDraftActions(): MailDraftActions {
  return useContext(MailDraftActionsContext)
}

export type MailDraftReady = {
  draftId: string
  revision?: number
  status?: string
  needsMailSetup?: boolean
}

/** 从工具结果/进度 data 或输出文本解析 draftId。 */
export function parseMailDraftReady(tool: { data?: unknown; output?: { text?: string }[] | null }): MailDraftReady | null {
  const data = tool.data && typeof tool.data === 'object' ? tool.data as Record<string, unknown> : null
  if (data && (data.status === 'draft_ready' || typeof data.draftId === 'string')) {
    const draftId = String(data.draftId ?? '')
    if (draftId) {
      return {
        draftId,
        revision: typeof data.revision === 'number' ? data.revision : undefined,
        status: typeof data.status === 'string' ? data.status : 'draft_ready',
        needsMailSetup: data.needsMailSetup === true,
      }
    }
  }
  for (const line of tool.output ?? []) {
    const text = line.text ?? ''
    const m = text.match(/"draftId"\s*:\s*"([^"]+)"/)
    if (m) {
      const rev = text.match(/"revision"\s*:\s*(\d+)/)
      return {
        draftId: m[1],
        revision: rev ? Number(rev[1]) : undefined,
        status: 'draft_ready',
        needsMailSetup: /"needsMailSetup"\s*:\s*true/.test(text),
      }
    }
  }
  return null
}
