import type { QuestionDraft } from '@/core/bridge'
import type { Attachment } from '@/composer/paste'

export interface ChatDraft {
  text: string
  attachments: Attachment[]
  retry?: { original: QuestionDraft; images: number[]; saved: ChatDraft }
}
export const emptyDraft = (): ChatDraft => ({ text: '', attachments: [] })
export function editQuestion(draft: ChatDraft, original: QuestionDraft): ChatDraft {
  return { text: original.text, attachments: [], retry: {
    original, images: (original.images ?? []).map((i) => i.index), saved: draft.retry?.saved ?? draft,
  } }
}
export function cancelQuestion(draft: ChatDraft): ChatDraft { return draft.retry?.saved ?? draft }
