import { describe, expect, it } from 'vitest'
import type { Block } from './blocks'
import { rehydrateMailSetupNotes, stampMailSetupRequired, turnNeedsMailSetup } from './mail-setup-note'

const tool = (needsMailSetup: boolean): Block => ({
  kind: 'tool',
  id: 't1',
  tool: {
    toolName: 'send_email',
    type: 'completed',
    data: { status: 'draft_ready', draftId: 'd1', revision: 1, ...(needsMailSetup ? { needsMailSetup: true } : {}) },
  },
})

describe('mail-setup-note', () => {
  it('detects needsMailSetup only in the current turn', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u0', text: '旧', ts: '' },
      tool(true),
      { kind: 'assistant', id: 'a0', text: '旧稿', streaming: false, ts: '' },
      { kind: 'user', id: 'u1', text: '新', ts: '' },
      tool(false),
      { kind: 'assistant', id: 'a1', text: '新稿', streaming: false, ts: '' },
    ]
    expect(turnNeedsMailSetup(blocks)).toBe(false)
    const withSetup: Block[] = [
      { kind: 'user', id: 'u1', text: '新', ts: '' },
      tool(true),
      { kind: 'assistant', id: 'a1', text: '请先设邮箱', streaming: false, ts: '' },
    ]
    expect(turnNeedsMailSetup(withSetup)).toBe(true)
  })

  it('stamps the last completed assistant bubble', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u1', text: '请假', ts: '' },
      tool(true),
      { kind: 'assistant', id: 'a1', text: '草稿好了', streaming: false, ts: '' },
    ]
    const next = stampMailSetupRequired(blocks)
    expect(next[2].kind === 'assistant' && next[2].mailSetupRequired).toBe(true)
    expect(stampMailSetupRequired(next)).toBe(next)
  })

  it('rehydrates across multiple turns', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u0', text: 'a', ts: '' },
      tool(true),
      { kind: 'assistant', id: 'a0', text: 'x', streaming: false, ts: '' },
      { kind: 'user', id: 'u1', text: 'b', ts: '' },
      tool(false),
      { kind: 'assistant', id: 'a1', text: 'y', streaming: false, ts: '' },
    ]
    const next = rehydrateMailSetupNotes(blocks)
    expect(next[2].kind === 'assistant' && next[2].mailSetupRequired).toBe(true)
    expect(next[5].kind === 'assistant' && next[5].mailSetupRequired).toBeFalsy()
  })
})
