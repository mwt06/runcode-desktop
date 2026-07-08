export type PreviewKind = 'markdown' | 'image' | 'svg' | 'html' | 'code' | 'text' | 'unsupported'

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
