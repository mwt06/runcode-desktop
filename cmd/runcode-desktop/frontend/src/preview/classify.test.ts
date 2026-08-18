import { describe, it, expect } from 'vitest'
import { classifyPreview, isPreviewable, previewSrc, toWorkspaceRel, buildFileTree, artifactKindLabel, kindIcon, filterFiles, clampPreviewWidth, pickAutoPreview, rankAutoPreview, extractFilePaths, matchWorkspaceFiles, normalizeSheetGrid } from './classify'

describe('normalizeSheetGrid', () => {
  it('pads ragged rows to the widest row and coerces cells to strings', () => {
    expect(normalizeSheetGrid([['a', 'b', 'c'], [1], []])).toEqual([
      ['a', 'b', 'c'],
      ['1', '', ''],
      ['', '', ''],
    ])
  })
  it('turns null/undefined cells into empty strings', () => {
    expect(normalizeSheetGrid([[null, undefined, 0, false]])).toEqual([['', '', '0', 'false']])
  })
  it('keeps HTML-looking cell content as inert text, never markup', () => {
    const payload = '<img src=x onerror=alert(1)>'
    const grid = normalizeSheetGrid([[payload, '</td><script>evil()</script>']])
    expect(grid).toEqual([[payload, '</td><script>evil()</script>']])
  })
  it('returns an empty grid for an empty sheet', () => {
    expect(normalizeSheetGrid([])).toEqual([])
  })
})

describe('clampPreviewWidth', () => {
  it('keeps an in-range stored value', () => {
    expect(clampPreviewWidth(560, 1280)).toBe(560)
    expect(clampPreviewWidth(700, 1280)).toBe(700) // 700 <= floor(1280*0.6)=768
  })
  it('resets an oversized value to the 1:1 default, capped to the window', () => {
    expect(clampPreviewWidth(2000, 1280)).toBe(640) // default floor(1280*0.5)=640 <= 768 max
    expect(clampPreviewWidth(2000, 800)).toBe(400) // default floor(800*0.5)=400 <= 480 max
  })
  it('resets a too-small or NaN value to the 1:1 default', () => {
    expect(clampPreviewWidth(100, 1280)).toBe(640)
    expect(clampPreviewWidth(NaN, 1280)).toBe(640)
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
  it('classifies Office documents and PDF', () => {
    expect(classifyPreview('report.docx').kind).toBe('docx')
    expect(classifyPreview('deck.PPTX').kind).toBe('pptx')
    expect(classifyPreview('data.xlsx').kind).toBe('xlsx')
    expect(classifyPreview('paper.pdf').kind).toBe('pdf')
  })
  it('does not classify the legacy binary Office formats (not OOXML)', () => {
    expect(classifyPreview('old.doc').kind).toBe('unsupported')
    expect(classifyPreview('old.ppt').kind).toBe('unsupported')
    expect(classifyPreview('old.xls').kind).toBe('unsupported')
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

describe('filterFiles', () => {
  it('filters case-insensitively; empty query returns all', () => {
    const fs = ['src/App.tsx', 'README.md', 'src/preview.ts']
    expect(filterFiles(fs, '')).toEqual(fs)
    expect(filterFiles(fs, 'app')).toEqual(['src/App.tsx'])
    expect(filterFiles(fs, 'SRC/')).toEqual(['src/App.tsx', 'src/preview.ts'])
  })
})

describe('pickAutoPreview', () => {
  it('按产物价值排：文档 > h5 > md > 代码，与写入先后无关', () => {
    // 做一套 PPT 的回合：先出 .pptx，再写生成脚本和说明。按时间取会开脚本。
    expect(pickAutoPreview(['deck.pptx', 'build.py', 'README.md'])).toBe('deck.pptx')
    expect(pickAutoPreview(['index.html', 'app.ts'])).toBe('index.html')
    expect(pickAutoPreview(['notes.md', 'main.go'])).toBe('notes.md')
    expect(pickAutoPreview(['main.go'])).toBe('main.go')
  })
  it('同级取写得更晚的那个（同一份文档改两次开新的）', () => {
    expect(pickAutoPreview(['a.docx', 'b.pptx'])).toBe('b.pptx')
    expect(pickAutoPreview(['a.go', 'b.md', 'a.go'])).toBe('b.md')
  })
  it('图片与纯文本排在代码之后——它们通常是过程产物,不是这一轮的交付物', () => {
    expect(pickAutoPreview(['chart.png', 'main.go'])).toBe('main.go')
    expect(pickAutoPreview(['out.log', 'chart.png'])).toBe('chart.png')
  })
  it('没有可预览的就返回 null', () => {
    expect(pickAutoPreview(['x.zip', 'y.bin'])).toBe(null)
    expect(pickAutoPreview([])).toBe(null)
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

describe('toWorkspaceRel 与行内路径可点击', () => {
  // 模型在正文里几乎总写绝对路径（"已生成 D:\演示\projects\…\deck.pptx"），而文件
  // 清单里存的是工作区相对路径。这一步换算是那行字能不能点开预览的分水岭。
  it('把工作区下的绝对路径换算成清单里的相对路径（含中文目录、反斜杠）', () => {
    const cwd = 'D:\\演示'
    const abs = 'D:\\演示\\projects\\page-design-ppt_ppt169_20260731\\exports\\deck.pptx'
    expect(toWorkspaceRel(abs, cwd)).toBe('projects/page-design-ppt_ppt169_20260731/exports/deck.pptx')
  })

  it('工作区之外的绝对路径原样保留，不会被误当成相对路径', () => {
    expect(toWorkspaceRel('E:\\别处\\x.md', 'D:\\演示')).toBe('E:/别处/x.md')
  })
})

describe('rankAutoPreview', () => {
  it('返回完整顺序，供调用方逐个验证存在性', () => {
    expect(rankAutoPreview(['build.py', 'README.md', 'deck.pptx']))
      .toEqual(['deck.pptx', 'README.md', 'build.py'])
  })

  it('真实场景：Bash 生成的 pptx 排在 Write 写的讲稿 md 之前', () => {
    // 做 PPT 的回合里 .pptx 由脚本产出、.md 是旁边的讲稿；此前只收集 Write/Edit 的
    // 产物，pptx 根本进不了候选，于是总在开 md。
    const ranked = rankAutoPreview([
      'projects/deck/notes/01_封面.md',
      'projects/deck/notes/02_正文.md',
      'projects/deck/exports/page_design_20260804.pptx',
    ])
    expect(ranked[0]).toBe('projects/deck/exports/page_design_20260804.pptx')
  })

  it('丢掉不可预览的类型', () => {
    expect(rankAutoPreview(['a.bin', 'b.md', 'c.exe'])).toEqual(['b.md'])
    expect(rankAutoPreview(['a.bin'])).toEqual([])
  })

  it('同一个文件写多次只出现一次，并按最后一次的先后算', () => {
    expect(rankAutoPreview(['a.md', 'b.md', 'a.md'])).toEqual(['a.md', 'b.md'])
  })

  it('与 pickAutoPreview 首选一致', () => {
    const paths = ['x.go', 'y.pptx', 'z.md']
    expect(rankAutoPreview(paths)[0]).toBe(pickAutoPreview(paths))
  })
})
