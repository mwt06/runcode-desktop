import { describe, expect, it } from 'vitest'
import type { Block } from './blocks'
import { rehydrateMailDraftNotes, stampMailDraftNote, turnMailDraftId } from './mail-draft-note'

const tool = (draftId: string): Block => ({
  kind: 'tool',
  id: `t-${draftId}`,
  tool: {
    toolName: 'send_email',
    type: 'completed',
    data: { status: 'draft_ready', draftId, revision: 1 },
  },
})

describe('mail-draft-note', () => {
  it('finds draft id only in the current turn', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u0', text: '旧', ts: '' },
      tool('old-draft'),
      { kind: 'assistant', id: 'a0', text: '旧回复', streaming: false, ts: '' },
      { kind: 'user', id: 'u1', text: '发邮件', ts: '' },
      tool('new-draft'),
      { kind: 'assistant', id: 'a1', text: '已准备', streaming: false, ts: '' },
    ]
    expect(turnMailDraftId(blocks)).toBe('new-draft')
  })

  it('stamps mailDraftId on the last completed assistant bubble', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u1', text: '发', ts: '' },
      tool('d1'),
      { kind: 'assistant', id: 'a1', text: '好了', streaming: false, ts: '' },
    ]
    const next = stampMailDraftNote(blocks)
    expect(next[2].kind === 'assistant' && next[2].mailDraftId).toBe('d1')
    expect(stampMailDraftNote(next)).toBe(next)
  })

  it('rehydrates draft footnotes across turns', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u1', text: '一', ts: '' },
      tool('d1'),
      { kind: 'assistant', id: 'a1', text: '一', streaming: false, ts: '' },
      { kind: 'user', id: 'u2', text: '二', ts: '' },
      { kind: 'assistant', id: 'a2', text: '二', streaming: false, ts: '' },
    ]
    const next = rehydrateMailDraftNotes(blocks)
    expect(next[2].kind === 'assistant' && next[2].mailDraftId).toBe('d1')
    expect(next[4].kind === 'assistant' && next[4].mailDraftId).toBeFalsy()
  })
})
