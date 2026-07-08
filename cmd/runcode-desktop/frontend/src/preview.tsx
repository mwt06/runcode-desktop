import { useEffect, useState, type ReactNode } from 'react'
import { Markdown } from './markdown'
import { readArtifact } from './bridge'
import { classifyPreview, previewSrc, buildFileTree, type FileNode } from './preview'

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
export function PreviewPanel({ baseURL, relPath, onClose }: { baseURL: string; relPath: string; onClose: () => void }) {
  const { kind, lang } = classifyPreview(relPath)
  const [bust, setBust] = useState(1)
  const [text, setText] = useState<string | null>(null)
  const [err, setErr] = useState('')
  const name = relPath.replace(/\\/g, '/').split('/').pop() || relPath
  const textual = kind === 'markdown' || kind === 'code' || kind === 'text'

  useEffect(() => {
    if (!textual) return
    setText(null)
    setErr('')
    readArtifact(relPath)
      .then(setText)
      .catch((e) => setErr(String(e)))
  }, [relPath, kind, bust, textual])

  return (
    <div className="flex flex-col h-full bg-surface">
      <div className="flex-none flex items-center gap-2 h-[52px] px-3 border-b border-line2">
        <span className="text-[13px] font-medium text-ink truncate flex-1 min-w-0" title={relPath}>{name}</span>
        <span className="text-[11px] text-faint">{kind}</span>
        <button className="text-muted hover:text-ink px-1.5" title="刷新" onClick={() => setBust((v) => v + 1)}>↻</button>
        {baseURL && <button className="text-muted hover:text-ink px-1.5" title="用系统程序打开" onClick={() => window.runtime.BrowserOpenURL(previewSrc(baseURL, relPath))}>↗</button>}
        <button className="text-muted hover:text-ink px-1.5" title="关闭" onClick={onClose}>✕</button>
      </div>
      <div className="flex-1 min-h-0 overflow-auto">
        {kind === 'html' && baseURL && (
          <iframe title={name} src={previewSrc(baseURL, relPath, bust)} className="w-full h-full border-0 bg-white" sandbox="allow-scripts allow-forms allow-popups allow-modals" />
        )}
        {(kind === 'image' || kind === 'svg') && baseURL && (
          <div className="p-4"><img src={previewSrc(baseURL, relPath, bust)} alt={name} className="max-w-full" /></div>
        )}
        {kind === 'markdown' && text != null && <div className="p-4"><Markdown>{text}</Markdown></div>}
        {kind === 'code' && text != null && <div className="p-4"><Markdown>{fencedCode(text, lang)}</Markdown></div>}
        {kind === 'text' && text != null && (
          <pre className="m-0 p-4 font-mono text-[12.5px] leading-[1.6] whitespace-pre-wrap break-words">{text}</pre>
        )}
        {kind === 'unsupported' && (
          <div className="p-6 text-[13px] text-muted">该文件类型暂不支持预览。{baseURL && <button className="text-primaryink underline ml-1" onClick={() => window.runtime.BrowserOpenURL(previewSrc(baseURL, relPath))}>用系统程序打开</button>}</div>
        )}
        {(kind === 'html' || kind === 'image' || kind === 'svg') && !baseURL && (
          <div className="p-6 text-[13px] text-muted">预览服务不可用。</div>
        )}
        {err && <div className="p-6 text-[13px] text-red">{err}</div>}
      </div>
    </div>
  )
}

// FileBrowser lists the workspace as a shallow read-only tree; clicking a file
// asks to preview it (the parent decides if it is previewable).
export function FileBrowser({ files, onPick }: { files: string[]; onPick: (path: string) => void }) {
  const tree = buildFileTree(files)
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
  return <div className="text-[12.5px] py-1 overflow-auto h-full">{render(tree, 0)}</div>
}
