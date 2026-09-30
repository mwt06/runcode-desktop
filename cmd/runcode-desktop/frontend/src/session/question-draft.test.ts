import { describe, expect, it } from 'vitest'
import { cancelQuestion, editQuestion, emptyDraft } from './question-draft'
import type { QuestionDraft } from '@/core/bridge'

const original: QuestionDraft = {
  source: { sessionId: 'source', questionId: 'question' },
  text: 'Complete original prompt with D:/work/source.docx',
  images: [{ index: 0, name: '图片 1', mediaType: 'image/png' }],
}
describe('question edit drafts', () => {
  it('loads full original content and source-bound images, not display text', () => {
    const draft = editQuestion(emptyDraft(), original)
    expect(draft.text).toBe(original.text)
    expect(draft.attachments).toEqual([])
    expect(draft.retry?.images).toEqual([0])
    expect(draft.retry?.original.source).toEqual(original.source)
  })
  it('restores unsent text and attachments after cancellation', () => {
    const saved = { text: 'half written', attachments: [{ path: 'draft.png', image: true }] }
    const edited = editQuestion(saved, original)
    edited.text = 'changed question'
    edited.retry!.images = []
    expect(cancelQuestion(edited)).toBe(saved)
  })
  it('switching edited questions retains the initial draft exactly once', () => {
    const saved = { text: 'keep me', attachments: [] }
    const draft = editQuestion(editQuestion(saved, original), { ...original, text: 'different' })
    expect(cancelQuestion(draft)).toBe(saved)
  })
  it('supports pure-image questions and independent session drafts', () => {
    const saved = emptyDraft()
    const imageOnly = editQuestion(saved, { ...original, text: '' })
    expect(imageOnly.text).toBe('')
    expect(imageOnly.retry?.images).toEqual([0])
    expect(saved).toEqual(emptyDraft())
  })
})
