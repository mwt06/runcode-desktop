import { describe, it, expect, beforeEach } from 'vitest'
import {
  clearAllInlineFiles, clearInlineFiles, inlineFileStatus, isStaticInlinePath,
  noteInlineFile, shouldSkipAutoPreview, waitForInlineSettled,
} from './inline-preview-status'

beforeEach(() => clearAllInlineFiles())

describe('inline-preview-status', () => {
  it('tracks per-session status and skips only rendered paths', () => {
    noteInlineFile('s1', 'a.svg', 'pending')
    noteInlineFile('s1', 'a.svg', 'rendered')
    noteInlineFile('s1', 'b.html', 'fallback')
    noteInlineFile('s2', 'a.svg', 'pending')
    expect(shouldSkipAutoPreview('s1', 'a.svg')).toBe(true)
    expect(shouldSkipAutoPreview('s1', 'b.html')).toBe(false)
    expect(shouldSkipAutoPreview('s2', 'a.svg')).toBe(false)
    clearInlineFiles('s1')
    expect(inlineFileStatus('s1', 'a.svg')).toBeNull()
    expect(inlineFileStatus('s2', 'a.svg')).toBe('pending')
  })
  it('recognizes static inline extensions and rejects .h5', () => {
    expect(isStaticInlinePath('x/y.SVG')).toBe(true)
    expect(isStaticInlinePath('a.mmd')).toBe(true)
    expect(isStaticInlinePath('a.mermaid')).toBe(true)
    expect(isStaticInlinePath('a.html')).toBe(true)
    expect(isStaticInlinePath('a.h5')).toBe(false)
    expect(isStaticInlinePath('a.png')).toBe(false)
  })
  it('waitForInlineSettled returns when pending flips', async () => {
    noteInlineFile('s', 'a.svg', 'pending')
    setTimeout(() => noteInlineFile('s', 'a.svg', 'rendered'), 30)
    await expect(waitForInlineSettled('s', 'a.svg', 500)).resolves.toBe('rendered')
  })
})
