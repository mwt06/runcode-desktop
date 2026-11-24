import { describe, expect, it } from 'vitest'
import { openTab, tabKey, type PreviewTab } from './tabs'

const inlineTab = (source: string): PreviewTab => ({
  kind: 'inline-static',
  ownerKey: 'inline:S:m1:0:10',
  title: 'flow.mmd',
  sourceKind: 'mermaid',
  source,
  messageID: 'm1',
  sessionID: 'S',
})

describe('inline-static preview tabs', () => {
  it('uses the stable owner key and replaces content when reopened', () => {
    const first = openTab([], null, inlineTab('graph TD;A-->B'))
    expect(first.active).toBe('inline:S:m1:0:10')
    expect(tabKey(first.tabs[0])).toBe('inline:S:m1:0:10')

    const second = openTab(first.tabs, first.active, inlineTab('graph TD;A-->C'))
    expect(second.tabs).toHaveLength(1)
    expect(second.tabs[0]).toMatchObject({
      kind: 'inline-static',
      source: 'graph TD;A-->C',
    })
  })
})
