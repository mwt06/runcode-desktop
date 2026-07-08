import { describe, it, expect } from 'vitest'
import { openTab, closeTab, setActive, type PreviewTab } from './preview-tabs'

const tabs = (...p: string[]): PreviewTab[] => p.map((relPath) => ({ relPath }))

describe('openTab', () => {
  it('appends a new tab and focuses it', () => {
    expect(openTab(tabs('a'), 'a', 'b')).toEqual({ tabs: tabs('a', 'b'), active: 'b' })
  })
  it('focuses an existing tab without duplicating', () => {
    expect(openTab(tabs('a', 'b'), 'a', 'b')).toEqual({ tabs: tabs('a', 'b'), active: 'b' })
  })
})

describe('closeTab', () => {
  it('closing the active tab focuses the right neighbor', () => {
    expect(closeTab(tabs('a', 'b', 'c'), 'b', 'b')).toEqual({ tabs: tabs('a', 'c'), active: 'c' })
  })
  it('closing the active last tab focuses the left neighbor', () => {
    expect(closeTab(tabs('a', 'b'), 'b', 'b')).toEqual({ tabs: tabs('a'), active: 'a' })
  })
  it('closing the only tab yields null active', () => {
    expect(closeTab(tabs('a'), 'a', 'a')).toEqual({ tabs: [], active: null })
  })
  it('closing a non-active tab keeps active', () => {
    expect(closeTab(tabs('a', 'b'), 'a', 'b')).toEqual({ tabs: tabs('a'), active: 'a' })
  })
})

describe('setActive', () => {
  it('activates an existing tab, ignores unknown', () => {
    expect(setActive(tabs('a', 'b'), 'a', 'b')).toBe('b')
    expect(setActive(tabs('a', 'b'), 'a', 'zzz')).toBe('a')
  })
})
