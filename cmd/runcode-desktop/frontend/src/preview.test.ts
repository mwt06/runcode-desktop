import { describe, it, expect } from 'vitest'
import { classifyPreview, isPreviewable, previewSrc, toWorkspaceRel } from './preview'

describe('classifyPreview', () => {
  it('maps by extension, case-insensitively', () => {
    expect(classifyPreview('README.md').kind).toBe('markdown')
    expect(classifyPreview('a/b/index.HTML').kind).toBe('html')
    expect(classifyPreview('logo.PNG').kind).toBe('image')
    expect(classifyPreview('icon.svg').kind).toBe('svg')
    expect(classifyPreview('notes.txt').kind).toBe('text')
  })
  it('classifies code with a highlight language', () => {
    expect(classifyPreview('main.go')).toEqual({ kind: 'code', lang: 'go' })
    expect(classifyPreview('app.tsx')).toEqual({ kind: 'code', lang: 'tsx' })
  })
  it('returns unsupported for unknown/binary', () => {
    expect(classifyPreview('archive.zip').kind).toBe('unsupported')
    expect(classifyPreview('noext').kind).toBe('unsupported')
  })
})

describe('isPreviewable', () => {
  it('is false for unsupported', () => {
    expect(isPreviewable('a.md')).toBe(true)
    expect(isPreviewable('a.zip')).toBe(false)
  })
})

describe('previewSrc', () => {
  it('joins base and path and adds a cache-buster', () => {
    expect(previewSrc('http://127.0.0.1:9/', 'a/b.html')).toBe('http://127.0.0.1:9/a/b.html')
    expect(previewSrc('http://127.0.0.1:9/', 'a b.png', 7)).toBe('http://127.0.0.1:9/a%20b.png?v=7')
  })
  it('does not double the slash when relPath has a leading slash', () => {
    expect(previewSrc('http://127.0.0.1:9/', '/a/b.png')).toBe('http://127.0.0.1:9/a/b.png')
  })
})

describe('toWorkspaceRel', () => {
  it('strips the workspace prefix from an absolute path', () => {
    expect(toWorkspaceRel('D:\\ws\\src\\a.ts', 'D:\\ws')).toBe('src/a.ts')
    expect(toWorkspaceRel('/home/u/ws/a.md', '/home/u/ws')).toBe('a.md')
  })
  it('leaves an already-relative path alone (normalizing slashes)', () => {
    expect(toWorkspaceRel('src\\a.ts', 'D:\\ws')).toBe('src/a.ts')
    expect(toWorkspaceRel('a.md', '/home/u/ws')).toBe('a.md')
  })
  it('matches the workspace prefix case-insensitively (Windows drive letter)', () => {
    expect(toWorkspaceRel('d:\\ws\\src\\a.ts', 'D:\\ws')).toBe('src/a.ts')
    expect(toWorkspaceRel('D:\\WS\\a.md', 'd:\\ws')).toBe('a.md')
  })
  it('leaves an absolute path that is not under cwd unchanged', () => {
    expect(toWorkspaceRel('/etc/passwd', '/home/u/ws')).toBe('/etc/passwd')
    expect(toWorkspaceRel('C:\\other\\a.ts', 'D:\\ws')).toBe('C:/other/a.ts')
  })
})
