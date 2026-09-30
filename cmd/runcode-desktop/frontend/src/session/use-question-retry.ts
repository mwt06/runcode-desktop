import { useRef, useState, type RefObject } from 'react'
import { errText, forkQuestion, getQuestion, type QuestionDraft } from '@/core/bridge'
import { basename } from '@/core/paths'
import type { Conversation } from './use-conversation'
import type { Session } from './use-session'
import type { useChatDrafts } from './use-chat-drafts'
import { cancelQuestion, editQuestion, emptyDraft } from './question-draft'
import { useActionScope } from './use-action-scope'

export function useQuestionRetry({ sessionId, blocked, drafts, conversation, session, textarea, notify }: {
  sessionId: string
  blocked: boolean
  drafts: ReturnType<typeof useChatDrafts>
  conversation: Conversation
  session: Session
  textarea: RefObject<HTMLTextAreaElement>
  notify: (text: string) => void
}) {
  const [pending, setPending] = useState(false)
  const pendingRef = useRef(false)
  const scope = useActionScope(sessionId)
  const disabled = blocked || pending
  const latestBlocked = useRef(blocked)
  latestBlocked.current = blocked

  async function exclusive(fn: (current: () => boolean) => Promise<boolean>) {
    if (latestBlocked.current || pendingRef.current) return false
    pendingRef.current = true
    setPending(true)
    const current = scope.capture()
    try { return await fn(current) }
    catch (e) { if (current()) notify(`重新生成失败：${errText(e)}`); return false }
    finally { pendingRef.current = false; setPending(false); void session.refreshOpen() }
  }

  async function generate(original: QuestionDraft, text: string, paths: string[], images: number[], current: () => boolean) {
    if (!current() || latestBlocked.current) return false
    const unchanged = session.captureSwitch()
    const r = await forkQuestion(original.source)
    if (!unchanged() || !current()) {
      await conversation.applyResumed(r, () => false)
      return false
    }
    // Register even an abandoned branch so later focusing it does not look empty.
    const branchDraft = editQuestion(emptyDraft(), original)
    branchDraft.text = text
    branchDraft.attachments = paths.map((path) => ({ path, image: true }))
    branchDraft.retry!.images = images
    const accepted = await session.runQuestionBranch(r, current, async (id) => {
      drafts.update(id, () => branchDraft)
      return conversation.submitFor({
        sessionId: id, text, imagePaths: paths, source: original.source,
        originalImages: images, allowSteering: false,
      }, undefined, [...images.map((i) => (original.images ?? []).find((im) => im.index === i)?.name ?? '图片'), ...paths.map(basename)])
    })
    if (accepted) drafts.update(r.info.sessionId, (d) => d === branchDraft ? emptyDraft() : d)
    return accepted
  }

  const edit = (questionId: string) => exclusive(async (current) => {
    const original = await getQuestion({ sessionId, questionId })
    if (!current() || latestBlocked.current) return false
    drafts.update(sessionId, (d) => editQuestion(d, original))
    requestAnimationFrame(() => { if (current()) textarea.current?.focus() })
    return true
  })

  const retry = (questionId: string) => exclusive(async (current) => {
    const original = await getQuestion({ sessionId, questionId })
    if (!current() || latestBlocked.current) return false
    // Failure leaves an editable input, rather than losing the original content.
    drafts.update(sessionId, (d) => editQuestion(d, original))
    const accepted = await generate(original, original.text, [], (original.images ?? []).map((i) => i.index), current)
    if (accepted) drafts.update(sessionId, cancelQuestion)
    return accepted
  })

  async function send(text: string, paths: string[], display?: string) {
    const draft = drafts.read(sessionId)
    if (!draft.retry) {
      const accepted = await conversation.send(text, paths, display)
      if (accepted) drafts.update(sessionId, (d) => d === draft ? emptyDraft() : d)
      return accepted
    }
    const { original, images } = draft.retry
    return exclusive(async (current) => {
      const accepted = await generate(original, text, paths, images, current)
      if (accepted) drafts.update(sessionId, cancelQuestion)
      return accepted
    })
  }

  const cancel = () => { if (!pendingRef.current) drafts.update(sessionId, cancelQuestion) }
  const removeImage = (index: number) => drafts.update(sessionId, (d) => d.retry
    ? { ...d, retry: { ...d.retry, images: d.retry.images.filter((i) => i !== index) } } : d)
  return { edit, retry, send, cancel, removeImage, disabled, pending, editing: drafts.draft.retry }
}
