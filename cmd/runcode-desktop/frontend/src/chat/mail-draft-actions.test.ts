import { describe, expect, it } from 'vitest'
import { parseMailDraftReady } from './mail-draft-actions'

describe('parseMailDraftReady', () => {
  it('reads structured data', () => {
    expect(parseMailDraftReady({ data: { status: 'draft_ready', draftId: 'd1', revision: 2 } })).toEqual({
      draftId: 'd1',
      revision: 2,
      status: 'draft_ready',
      needsMailSetup: false,
    })
  })

  it('reads needsMailSetup', () => {
    expect(parseMailDraftReady({
      data: { status: 'draft_ready', draftId: 'd1', revision: 1, needsMailSetup: true },
    })).toMatchObject({ draftId: 'd1', needsMailSetup: true })
  })

  it('parses JSON line from output text', () => {
    expect(parseMailDraftReady({
      output: [{ text: '{"status":"draft_ready","draftId":"dx","revision":3,"needsMailSetup":true}\n草稿已创建' }],
    })).toMatchObject({ draftId: 'dx', needsMailSetup: true })
  })
})
