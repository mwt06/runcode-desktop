import { useEffect, useState, type ReactNode } from 'react'
import { Markdown } from './markdown'
import { readArtifact, openExternal, resolveArtifactPath, copyText } from './bridge'
import { Icon } from './icons'
import { classifyPreview, previewSrc, artifactKindLabel, kindIcon, kindAccent, filterFiles, buildFileTree, type FileNode } from './preview'
import type { PreviewTab } from './preview-tabs'

// fencedCode wraps source in a Markdown code fence long enough to survive any run
// of backticks in the source, so the shared Markdown renderer (rehype-highlight)
// highlights it — reusing the existing pipeline instead of a second highlighter.
function fencedCode(text: string, lang?: string): string {
  const longest = (text.match(/`+/g) || []).reduce((m, s) => Math.max(m, s.length), 0)
  const fence = '`'.repeat(Math.max(3, longest + 1))
  return `${fence}${lang ?? ''}\n${text}\n${fence}`
}

// PreviewPanel renders one workspace artifact by type: HTML in a sandboxed iframe
// and images by the loopback static-server URL; Markdown/code/text by fetching the
// text via ReadArtifact and rendering it in React.
function IconBtn({ name, title, onClick }: { name: string; title: string; onClick: () => void }) {
  return (
    <button title={title} onClick={onClick} className="flex-none w-7 h-7 flex items-center justify-center rounded-md text-muted hover:text-ink hover:bg-surface2">
      <Icon name={name} size={14} />
    </button>
  )
}

export function PreviewPanel({ baseURL, relPath, onClose }: { baseURL: string; relPath: string; onClose: () => void }) {
  const { kind, lang } = classifyPreview(relPath)
  const accent = kindAccent(kind)
  const [bust, setBust] = useState(1)
  const [text, setText] = useState<string | null>(null)
  const [err, setErr] = useState('')
  const name = relPath.replace(/\\/g, '/').split('/').pop() || relPath
  const textual = kind === 'markdown' || kind === 'code' || kind === 'text'

  useEffect(() => {
    if (!textual) return
    let ignore = false
    setText(null)
    setErr('')
    readArtifact(relPath)
      .then((t) => { if (!ignore) setText(t) })
      .catch((e) => { if (!ignore) setErr(String(e)) })
    return () => { ignore = true }
  }, [relPath, kind, bust, textual])

  const copyPath = async () => {
    try { await copyText(await resolveArtifactPath(relPath)) } catch { /* non-fatal */ }
  }

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface">
      <div className="flex-none flex items-center gap-1.5 h-[44px] px-2.5 border-b border-line2">
        <span className="flex-none" style={{ color: accent }}><Icon name={kindIcon(kind)} size={15} /></span>
        <span className="flex-none text-[10.5px] font-medium rounded px-1.5 py-0.5 mr-auto" style={{ color: accent, background: accent + '1a' }}>{artifactKindLabel(kind)}</span>
        <IconBtn name="refresh" title="刷新" onClick={() => setBust((v) => v + 1)} />
        <IconBtn name="external-link" title="用系统默认程序打开" onClick={() => { openExternal(relPath).catch(() => {}) }} />
        <IconBtn name="copy" title="复制路径" onClick={copyPath} />
        <IconBtn name="win-close" title="关闭" onClick={onClose} />
      </div>
      <div className="flex-1 min-h-0 overflow-auto">
        {kind === 'html' && baseURL && (
          <iframe title={name} src={previewSrc(baseURL, relPath, bust)} className="w-full h-full border-0 bg-white" sandbox="allow-scripts allow-forms allow-popups allow-modals" />
        )}
        {(kind === 'image' || kind === 'svg') && baseURL && (
          <div className="p-4 flex items-center justify-center min-h-full bg-inset/30"><img src={previewSrc(baseURL, relPath, bust)} alt={name} className="max-w-full" /></div>
        )}
        {kind === 'markdown' && text != null && <div className="p-4"><Markdown>{text}</Markdown></div>}
        {kind === 'code' && text != null && <div className="p-4"><Markdown>{fencedCode(text, lang)}</Markdown></div>}
        {kind === 'text' && text != null && (
          <pre className="m-0 p-4 font-mono text-[12.5px] leading-[1.6] whitespace-pre-wrap break-words">{text}</pre>
        )}
        {kind === 'unsupported' && (
          <div className="p-6 text-[13px] text-muted">该文件类型暂不支持预览。<button className="text-primaryink underline ml-1" onClick={() => openExternal(relPath).catch(() => {})}>用系统程序打开</button></div>
        )}
        {(kind === 'html' || kind === 'image' || kind === 'svg') && !baseURL && (
          <div className="p-6 text-[13px] text-muted">预览服务不可用。</div>
        )}
        {err && <div className="p-6 text-[13px] text-red">{err}</div>}
      </div>
    </div>
  )
}

// PreviewTabs is the tab strip above the preview: one tab per open file, active
// highlighted, each closable.
export function PreviewTabs({ tabs, active, onSelect, onClose }: { tabs: PreviewTab[]; active: string | null; onSelect: (relPath: string) => void; onClose: (relPath: string) => void }) {
  return (
    <div className="flex-none flex items-stretch h-[36px] border-b border-line2 overflow-x-auto bg-surface">
      {tabs.map((t) => {
        const name = t.relPath.replace(/\\/g, '/').split('/').pop() || t.relPath
        const on = t.relPath === active
        return (
          <div
            key={t.relPath}
            onClick={() => onSelect(t.relPath)}
            title={t.relPath}
            style={on ? { boxShadow: `inset 2px 0 0 ${kindAccent(classifyPreview(t.relPath).kind)}` } : undefined}
            className={`group flex items-center gap-1.5 pl-3 pr-2 max-w-[180px] flex-none cursor-pointer border-r border-line2 text-[12.5px] ${on ? 'bg-surface2 text-ink' : 'text-muted hover:bg-surface2/60'}`}
          >
            <span className="truncate">{name}</span>
            <button
              className="flex-none w-4 h-4 flex items-center justify-center rounded opacity-0 group-hover:opacity-100 hover:bg-line2 hover:text-ink"
              onClick={(e) => { e.stopPropagation(); onClose(t.relPath) }}
            >
              <Icon name="win-close" size={10} />
            </button>
          </div>
        )
      })}
    </div>
  )
}

// PreviewPane composes the tab strip with the active file's PreviewPanel.
export function PreviewPane({ tabs, active, baseURL, onSelect, onClose, onCloseTab }: { tabs: PreviewTab[]; active: string | null; baseURL: string; onSelect: (relPath: string) => void; onClose: () => void; onCloseTab: (relPath: string) => void }) {
  return (
    <div className="flex flex-col h-full min-h-0">
      <PreviewTabs tabs={tabs} active={active} onSelect={onSelect} onClose={onCloseTab} />
      <div className="flex-1 min-h-0">
        {active ? <PreviewPanel key={active} baseURL={baseURL} relPath={active} onClose={onClose} /> : null}
      </div>
    </div>
  )
}

// FileBrowser lists the workspace as a shallow read-only tree; clicking a file
// asks to preview it (the parent decides if it is previewable).
export function FileBrowser({ files, onPick }: { files: string[]; onPick: (relPath: string) => void }) {
  const [query, setQuery] = useState('')
  const tree = buildFileTree(filterFiles(files, query))
  const render = (nodes: FileNode[], depth: number): ReactNode =>
    nodes.map((n) =>
      n.dir ? (
        <div key={n.path}>
          <div className="px-2 py-1 text-[12.5px] text-muted font-medium" style={{ paddingLeft: 8 + depth * 12 }}>{n.name}/</div>
          {n.children && render(n.children, depth + 1)}
        </div>
      ) : (
        <div key={n.path} onClick={() => onPick(n.path)} className="px-2 py-1 text-[12.5px] text-ink hover:bg-surface2 cursor-pointer truncate" style={{ paddingLeft: 8 + depth * 12 }} title={n.path}>{n.name}</div>
      ),
    )
  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="flex-none p-2 border-b border-line2">
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="筛选文件…"
          className="w-full text-[12.5px] bg-inset rounded-md px-2.5 py-1.5 outline-none text-ink placeholder:text-faint"
        />
      </div>
      <div className="flex-1 min-h-0 text-[12.5px] py-1 overflow-auto">{render(tree, 0)}</div>
    </div>
  )
}
