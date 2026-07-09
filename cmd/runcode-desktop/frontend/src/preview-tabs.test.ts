import { describe, it, expect } from 'vitest'
import { openTab, closeTab, tabKey, type PreviewTab } from './preview-tabs'

const file = (relPath: string): PreviewTab => ({ kind: 'file', relPath })
const diff = (snapshotId: string, relPath: string): PreviewTab => ({ kind: 'diff', snapshotId, relPath })

describe('preview-tabs union', () => {
  it('opens a file tab and focuses it', () => {
    const r = openTab([], null, file('a.md'))
    expect(r.tabs).toHaveLength(1)
    expect(r.active).toBe('a.md')
  })
  it('opens a diff tab keyed by snapshotId, distinct from the file tab', () => {
    const r1 = openTab([], null, file('a.md'))
    const r2 = openTab(r1.tabs, r1.active, diff('7', 'a.md'))
    expect(r2.tabs).toHaveLength(2)
    expect(r2.active).toBe('diff:7')
  })
  it('does not duplicate an already-open diff tab', () => {
    const r1 = openTab([], null, diff('7', 'a.md'))
    const r2 = openTab(r1.tabs, r1.active, diff('7', 'a.md'))
    expect(r2.tabs).toHaveLength(1)
  })
  it('closes by key and moves focus', () => {
    const t = [file('a.md'), diff('7', 'a.md')]
    const r = closeTab(t, 'diff:7', 'diff:7')
    expect(r.tabs).toHaveLength(1)
    expect(r.active).toBe('a.md')
  })
  it('tabKey distinguishes file vs diff', () => {
    expect(tabKey(file('a.md'))).toBe('a.md')
    expect(tabKey(diff('7', 'a.md'))).toBe('diff:7')
  })
})
