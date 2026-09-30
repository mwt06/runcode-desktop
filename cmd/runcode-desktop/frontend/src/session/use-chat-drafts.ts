import { useCallback, useRef, useState, type SetStateAction } from 'react'
import type { Attachment } from '@/composer/paste'
import { emptyDraft, type ChatDraft } from './question-draft'

// Text, file paths, and the previous unsent draft all belong to one session.
export function useChatDrafts(sessionId: string) {
  const [all, setAll] = useState<Record<string, ChatDraft>>({})
  const latest = useRef(all)
  const read = useCallback((id: string) => latest.current[id] ?? emptyDraft(), [])
  const update = useCallback((id: string, fn: (d: ChatDraft) => ChatDraft) => {
    const next = { ...latest.current, [id]: fn(read(id)) }
    latest.current = next
    setAll(next)
  }, [read])
  const draft = all[sessionId] ?? emptyDraft()
  const setInput = (v: SetStateAction<string>) => update(sessionId, (d) => ({ ...d, text: typeof v === 'function' ? v(d.text) : v }))
  const setAttachments = (v: SetStateAction<Attachment[]>) => update(sessionId, (d) => ({ ...d, attachments: typeof v === 'function' ? v(d.attachments) : v }))
  return { draft, input: draft.text, attachments: draft.attachments, setInput, setAttachments, read, update }
}
