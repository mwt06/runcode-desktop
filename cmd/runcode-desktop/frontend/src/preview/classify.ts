export type PreviewKind =
  | 'markdown' | 'image' | 'svg' | 'html' | 'code' | 'text'
  | 'docx' | 'pptx' | 'xlsx' | 'pdf'
  | 'unsupported'

const IMAGE = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'ico'])
const MARKDOWN = new Set(['md', 'markdown'])
const HTML = new Set(['html', 'htm'])
const TEXT = new Set(['txt', 'log', 'csv', 'env'])
// Extension -> highlight.js language for the code viewer.
const CODE: Record<string, string> = {
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'jsx',
  ts: 'typescript', tsx: 'tsx', py: 'python', go: 'go', rs: 'rust',
  java: 'java', c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', cs: 'csharp',
  css: 'css', scss: 'scss', json: 'json', yaml: 'yaml', yml: 'yaml',
  toml: 'toml', sh: 'bash', bash: 'bash', sql: 'sql', rb: 'ruby', php: 'php',
  kt: 'kotlin', swift: 'swift', xml: 'xml',
}

function ext(path: string): string {
  const base = path.replace(/\\/g, '/').split('/').pop() || ''
  const dot = base.lastIndexOf('.')
  return dot > 0 ? base.slice(dot + 1).toLowerCase() : ''
}

export function classifyPreview(path: string): { kind: PreviewKind; lang?: string } {
  const e = ext(path)
  if (MARKDOWN.has(e)) return { kind: 'markdown' }
  if (e === 'svg') return { kind: 'svg' }
  if (IMAGE.has(e)) return { kind: 'image' }
  if (HTML.has(e)) return { kind: 'html' }
  // Office documents and PDF: rendered in-place by lazy-loaded viewers (see
  // PreviewPanel). The legacy .doc/.ppt/.xls binary formats are not OOXML zips and
  // the JS renderers can't read them, so only the modern *x extensions qualify.
  if (e === 'docx') return { kind: 'docx' }
  if (e === 'pptx') return { kind: 'pptx' }
  if (e === 'xlsx') return { kind: 'xlsx' }
  if (e === 'pdf') return { kind: 'pdf' }
  if (CODE[e]) return { kind: 'code', lang: CODE[e] }
  if (TEXT.has(e)) return { kind: 'text' }
  return { kind: 'unsupported' }
}

export function isPreviewable(path: string): boolean {
  return classifyPreview(path).kind !== 'unsupported'
}

export function previewSrc(baseURL: string, relPath: string, bust?: number): string {
  const encoded = relPath
    .replace(/\\/g, '/')
    .replace(/^\/+/, '')
    .split('/')
    .map(encodeURIComponent)
    .join('/')
  const base = baseURL.endsWith('/') ? baseURL : baseURL + '/'
  return base + encoded + (bust ? `?v=${bust}` : '')
}

// toWorkspaceRel normalizes a tool-reported path to a forward-slash workspace-
// relative path. Tool events may carry either an absolute path under cwd or an
// already-relative one; both must become relative before ReadArtifact/previewSrc.
// The prefix match is case-insensitive to mirror the Go backend's filepath.Rel on
// Windows, where the user-typed cwd and a tool-reported path can differ in casing.
export function toWorkspaceRel(path: string, cwd: string): string {
  const p = path.replace(/\\/g, '/')
  const root = cwd.replace(/\\/g, '/').replace(/\/+$/, '')
  if (root) {
    const pl = p.toLowerCase()
    const rl = root.toLowerCase()
    if (pl === rl) return ''
    if (pl.startsWith(rl + '/')) return p.slice(root.length + 1) // slice original-case p
  }
  // Not under cwd: strip only a literal "./" prefix; leave any other absolute or
  // relative path untouched so a non-workspace path isn't mislabeled as relative.
  return p.replace(/^\.\//, '')
}

export type FileNode = { name: string; path: string; dir: boolean; children?: FileNode[] }

export function buildFileTree(paths: string[]): FileNode[] {
  type Dir = { node: FileNode; dirs: Map<string, Dir>; files: FileNode[] }
  const root: Dir = { node: { name: '', path: '', dir: true, children: [] }, dirs: new Map(), files: [] }
  for (const raw of paths) {
    const parts = raw.replace(/\\/g, '/').split('/').filter((s) => s && s !== '.')
    let cur = root
    for (let i = 0; i < parts.length; i++) {
      const isFile = i === parts.length - 1
      const path = parts.slice(0, i + 1).join('/')
      if (isFile) {
        cur.files.push({ name: parts[i], path, dir: false })
      } else {
        let child = cur.dirs.get(parts[i])
        if (!child) {
          child = { node: { name: parts[i], path, dir: true, children: [] }, dirs: new Map(), files: [] }
          cur.dirs.set(parts[i], child)
        }
        cur = child
      }
    }
  }
  const collect = (d: Dir): FileNode[] => {
    const dirs = [...d.dirs.values()].sort((a, b) => a.node.name.localeCompare(b.node.name))
    for (const sub of dirs) sub.node.children = collect(sub)
    const files = d.files.sort((a, b) => a.name.localeCompare(b.name))
    return [...dirs.map((x) => x.node), ...files]
  }
  return collect(root)
}

// artifactKindLabel is the Chinese type subtitle shown on artifact cards and the
// preview header.
export function artifactKindLabel(kind: PreviewKind): string {
  switch (kind) {
    case 'markdown': return 'Markdown 文档'
    case 'html': return 'HTML 页面'
    case 'image': return '图像'
    case 'svg': return 'SVG 矢量图'
    case 'code': return '代码'
    case 'text': return '文本'
    case 'docx': return 'Word 文档'
    case 'pptx': return 'PowerPoint 演示'
    case 'xlsx': return 'Excel 表格'
    case 'pdf': return 'PDF 文档'
    default: return '文件'
  }
}

// kindIcon maps a preview kind to a file-type Icon name (see icons.tsx).
export function kindIcon(kind: PreviewKind): string {
  switch (kind) {
    case 'html': return 'file-html'
    case 'markdown': return 'file-md'
    case 'code': return 'file-code'
    case 'image': case 'svg': return 'file-image'
    case 'docx': return 'file-doc'
    case 'pptx': return 'file-ppt'
    case 'xlsx': return 'file-xls'
    case 'pdf': return 'file-pdf'
    default: return 'file-text'
  }
}

// fileColor gives a file a soft, VSCode-Seti-style accent for its icon in the
// preview panel (tabs, header, file browser). Muted tones tuned for the light
// theme; an unknown extension falls back to the neutral --color-faint grey. Used
// only in the preview area — conversation artifact cards stay neutral by design.
const FILE_COLOR: Record<string, string> = {
  md: '#5b8fc9', markdown: '#5b8fc9',
  html: '#d9895b', htm: '#d9895b',
  css: '#5b9fd9', scss: '#c96b9f',
  js: '#d9b24a', mjs: '#d9b24a', cjs: '#d9b24a', jsx: '#d9b24a',
  ts: '#4f86c6', tsx: '#4f86c6',
  json: '#c9a94a',
  py: '#4f95bf', go: '#4fb0c6', rs: '#c98a5b',
  java: '#c46b5b', c: '#6b8fc9', h: '#6b8fc9', cpp: '#6b8fc9', cc: '#6b8fc9',
  cs: '#7aa86b', kt: '#a06bc6', swift: '#d9895b', rb: '#c45b5b', php: '#8a7ac6',
  yaml: '#c96b7a', yml: '#c96b7a', toml: '#c96b7a', sql: '#c99a5b',
  sh: '#7aa86b', bash: '#7aa86b', xml: '#8a9b6b',
  svg: '#9a7ac6', png: '#9a7ac6', jpg: '#9a7ac6', jpeg: '#9a7ac6',
  gif: '#9a7ac6', webp: '#9a7ac6', bmp: '#9a7ac6', ico: '#9a7ac6',
  docx: '#2b7cd3', pptx: '#d24726', xlsx: '#1d7a44', pdf: '#d64b4b',
}

export function fileColor(path: string): string {
  return FILE_COLOR[ext(path)] ?? '#8a92a3'
}

// filterFiles keeps workspace-relative paths that contain query (case-insensitive);
// an empty/whitespace query returns the list unchanged.
export function filterFiles(files: string[], query: string): string[] {
  const q = query.trim().toLowerCase()
  if (!q) return files
  return files.filter((f) => f.toLowerCase().includes(q))
}

// clampPreviewWidth keeps a persisted preview-pane width within a sane range for the
// current window, so a stale/oversized value can't start the pane wider than the
// screen (which would collapse the chat column). With no valid stored value it
// defaults the pane to half the window — 1:1 with the chat column — capped to the
// 60% max. A stored width from an explicit drag (in range) is always respected.
export function clampPreviewWidth(stored: number, windowWidth: number): number {
  const max = Math.floor(windowWidth * 0.6)
  return stored >= 360 && stored <= max ? stored : Math.min(Math.floor(windowWidth * 0.5), max)
}

// lastPreviewablePath returns the last (most-recently-written) previewable path from
// an ordered list of workspace file paths, or null if none is previewable/empty.
export function lastPreviewablePath(paths: string[]): string | null {
  for (let i = paths.length - 1; i >= 0; i--) {
    if (isPreviewable(paths[i])) return paths[i]
  }
  return null
}

// extractFilePaths pulls file-path-like tokens out of prose: word/path chars ending
// in a short extension. Permissive by design — matchWorkspaceFiles is the real gate
// (the path must exist in the workspace). Trailing ASCII/Chinese punctuation is
// trimmed; a leading "./" is dropped by the first-char class.
export function extractFilePaths(text: string): string[] {
  const re = /[A-Za-z0-9_@][\w./\\@+-]*\.[A-Za-z0-9]{1,12}/g
  const seen = new Set<string>()
  const out: string[] = []
  for (const m of text.matchAll(re)) {
    const tok = m[0].replace(/[)\]}.,;:!?，。、）】]+$/, '')
    if (tok && !seen.has(tok)) {
      seen.add(tok)
      out.push(tok)
    }
  }
  return out
}

// normalizeSheetGrid squares up SheetJS sheet_to_json(header:1) output into a
// uniform text grid: every row padded to the widest row, every cell coerced to a
// plain string. Workbook content is untrusted — cells must stay plain text for the
// React renderer to escape, never library-generated HTML injected raw.
export function normalizeSheetGrid(rows: unknown[][]): string[][] {
  const width = rows.reduce((m, r) => Math.max(m, r.length), 0)
  return rows.map((r) => Array.from({ length: width }, (_, i) => (r[i] == null ? '' : String(r[i]))))
}

// matchWorkspaceFiles keeps candidates that correspond to a real workspace file,
// returning the actual workspace-relative paths (forward-slash), deduped, in order.
// A candidate matches if — normalized — it equals a file path or a file path ends
// with "/" + the candidate (basename/suffix match).
export function matchWorkspaceFiles(candidates: string[], files: string[]): string[] {
  const norm = (s: string) => s.replace(/\\/g, '/').replace(/^\.\//, '').replace(/^\/+/, '')
  const fileset = files.map(norm)
  const seen = new Set<string>()
  const out: string[] = []
  for (const c of candidates) {
    const cn = norm(c)
    if (!cn) continue
    const hit = fileset.find((f) => f === cn || f.endsWith('/' + cn))
    if (hit && !seen.has(hit)) {
      seen.add(hit)
      out.push(hit)
    }
  }
  return out
}
