import { describe, it, expect } from 'vitest'
import { classifyPreview, isPreviewable, previewSrc, toWorkspaceRel, buildFileTree, artifactKindLabel, kindIcon, kindAccent, filterFiles, clampPreviewWidth, lastPreviewablePath, extractFilePaths, matchWorkspaceFiles } from './preview'

describe('clampPreviewWidth', () => {
  it('keeps an in-range stored value', () => {
    expect(clampPreviewWidth(560, 1280)).toBe(560)
    expect(clampPreviewWidth(700, 1280)).toBe(700) // 700 <= floor(1280*0.6)=768
  })
  it('resets an oversized value to the default, capped to the window', () => {
    expect(clampPreviewWidth(2000, 1280)).toBe(560) // default 560 <= 768 max
    expect(clampPreviewWidth(2000, 800)).toBe(480) // default 560 > 480 max → 480
  })
  it('resets a too-small or NaN value to the default', () => {
    expect(clampPreviewWidth(100, 1280)).toBe(560)
    expect(clampPreviewWidth(NaN, 1280)).toBe(560)
  })
})

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

describe('buildFileTree', () => {
  it('nests by directory, dirs before files, sorted', () => {
    const tree = buildFileTree(['src/b.ts', 'src/a.ts', 'README.md', 'src/ui/x.css'])
    // top level: dir "src" before file "README.md"
    expect(tree.map((n) => n.name)).toEqual(['src', 'README.md'])
    const src = tree[0]
    expect(src.dir).toBe(true)
    // inside src: subdir "ui" before files a.ts, b.ts
    expect(src.children!.map((n) => n.name)).toEqual(['ui', 'a.ts', 'b.ts'])
    expect(src.children![1].path).toBe('src/a.ts')
  })
  it('normalizes a leading ./ and backslashes without a spurious "." node', () => {
    const tree = buildFileTree(['./src\\a.ts', 'b.md'])
    expect(tree.map((n) => n.name)).toEqual(['src', 'b.md'])
    expect(tree[0].children!.map((n) => n.path)).toEqual(['src/a.ts'])
  })
})

describe('artifactKindLabel', () => {
  it('maps kinds to Chinese subtitles', () => {
    expect(artifactKindLabel('markdown')).toBe('Markdown 文档')
    expect(artifactKindLabel('image')).toBe('图像')
    expect(artifactKindLabel('code')).toBe('代码')
    expect(artifactKindLabel('unsupported')).toBe('文件')
  })
})

describe('kindIcon', () => {
  it('maps kinds to file-type icon names', () => {
    expect(kindIcon('html')).toBe('file-html')
    expect(kindIcon('markdown')).toBe('file-md')
    expect(kindIcon('code')).toBe('file-code')
    expect(kindIcon('image')).toBe('file-image')
    expect(kindIcon('text')).toBe('file-text')
    expect(kindIcon('unsupported')).toBe('file-text')
  })
})

describe('kindAccent', () => {
  it('maps kinds to accent colors, slate for unknown', () => {
    expect(kindAccent('html')).toBe('#E39A3B')
    expect(kindAccent('markdown')).toBe('#4C82F7')
    expect(kindAccent('code')).toBe('#2FAE6A')
    expect(kindAccent('image')).toBe('#E0679B')
    expect(kindAccent('svg')).toBe('#E0679B')
    expect(kindAccent('unsupported')).toBe('#8A94A6')
  })
})

describe('filterFiles', () => {
  it('filters case-insensitively; empty query returns all', () => {
    const fs = ['src/App.tsx', 'README.md', 'src/preview.ts']
    expect(filterFiles(fs, '')).toEqual(fs)
    expect(filterFiles(fs, 'app')).toEqual(['src/App.tsx'])
    expect(filterFiles(fs, 'SRC/')).toEqual(['src/App.tsx', 'src/preview.ts'])
  })
})

describe('lastPreviewablePath', () => {
  it('returns the last previewable path (most recently written)', () => {
    expect(lastPreviewablePath(['a.go', 'b.md', 'a.go'])).toBe('a.go') // last occurrence wins
    expect(lastPreviewablePath(['a.md', 'b.zip'])).toBe('a.md') // skips a non-previewable last entry
  })
  it('returns null when none is previewable or the list is empty', () => {
    expect(lastPreviewablePath(['x.zip', 'y.bin'])).toBe(null)
    expect(lastPreviewablePath([])).toBe(null)
  })
})

describe('extractFilePaths', () => {
  it('pulls path-like tokens with a known-ish extension', () => {
    expect(extractFilePaths('我建了 cat.html 和 src/app.py 两个文件')).toEqual(['cat.html', 'src/app.py'])
  })
  it('trims trailing punctuation and leading ./', () => {
    expect(extractFilePaths('详见 ./README.md。')).toEqual(['README.md'])
    expect(extractFilePaths('见 report.md, notes.txt.')).toEqual(['report.md', 'notes.txt'])
  })
  it('ignores prose without a file extension', () => {
    expect(extractFilePaths('这是一段没有文件的普通文字')).toEqual([])
  })
})

describe('matchWorkspaceFiles', () => {
  const files = ['README.md', 'src/app.py', 'src/ui/index.html']
  it('keeps candidates that exist (full path or basename/suffix), workspace-relative', () => {
    expect(matchWorkspaceFiles(['app.py', 'README.md'], files)).toEqual(['src/app.py', 'README.md'])
    expect(matchWorkspaceFiles(['src/ui/index.html'], files)).toEqual(['src/ui/index.html'])
  })
  it('drops candidates that do not exist, dedups', () => {
    expect(matchWorkspaceFiles(['nope.md', 'README.md', 'README.md'], files)).toEqual(['README.md'])
  })
})
