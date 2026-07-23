// PreviewPanel renders one workspace artifact by type: HTML in a sandboxed iframe
// and images by the loopback static-server URL; Markdown/code/text by fetching the
// text via ReadArtifact and rendering it in React; Office docs (docx/pptx/xlsx) via
// lazy-loaded in-browser viewers; PDF by the WebView's native viewer.
import { useEffect, useState } from 'react'
import { Icon } from '@/ui/icons'
import { Markdown } from '@/ui/markdown'
import { basename } from '@/core/paths'
import { copyText, errText, openExternal, readArtifact, resolveArtifactPath } from '@/core/bridge'
import { artifactKindLabel, classifyPreview, fileColor, kindIcon, previewSrc } from './classify'
import { IconBtn } from './icon-btn'
import { ImperativeDocView, renderDocx } from './viewers/doc-view'
import { PptxView } from './viewers/pptx-view'
import { XlsxView } from './viewers/xlsx-view'

// fencedCode wraps source in a Markdown code fence long enough to survive any run
// of backticks in the source, so the shared Markdown renderer (rehype-highlight)
// highlights it — reusing the existing pipeline instead of a second highlighter.
function fencedCode(text: string, lang?: string): string {
  const longest = (text.match(/`+/g) || []).reduce((m, s) => Math.max(m, s.length), 0)
  const fence = '`'.repeat(Math.max(3, longest + 1))
  return `${fence}${lang ?? ''}\n${text}\n${fence}`
}

export function PreviewPanel({ baseURL, relPath, onClose }: { baseURL: string; relPath: string; onClose: () => void }) {
  const { kind, lang } = classifyPreview(relPath)
  const [bust, setBust] = useState(1)
  const [text, setText] = useState<string | null>(null)
  const [err, setErr] = useState('')
  const name = basename(relPath)
  const textual = kind === 'markdown' || kind === 'code' || kind === 'text'

  useEffect(() => {
    if (!textual) return
    let ignore = false
    setText(null)
    setErr('')
    readArtifact(relPath)
      .then((t) => { if (!ignore) setText(t) })
      .catch((e) => { if (!ignore) setErr(errText(e)) })
    return () => { ignore = true }
  }, [relPath, kind, bust, textual])

  const copyPath = async () => {
    try { await copyText(await resolveArtifactPath(relPath)) } catch { /* non-fatal */ }
  }

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface">
      <div className="flex-none flex items-center gap-1.5 h-[44px] px-2.5 border-b border-line2">
        <span className="flex-none" style={{ color: fileColor(relPath) }}><Icon name={kindIcon(kind)} size={15} /></span>
        <span className="flex-none text-[10.5px] text-faint bg-inset rounded px-1.5 py-0.5 mr-auto">{artifactKindLabel(kind)}</span>
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
        {kind === 'docx' && <ImperativeDocView relPath={relPath} reloadKey={bust} load={renderDocx} busyHint=" Word" />}
        {kind === 'pptx' && <PptxView relPath={relPath} reloadKey={bust} />}
        {kind === 'xlsx' && <XlsxView relPath={relPath} reloadKey={bust} />}
        {kind === 'pdf' && baseURL && (
          <iframe title={name} src={previewSrc(baseURL, relPath, bust)} className="w-full h-full border-0 bg-white" />
        )}
        {kind === 'pdf' && !baseURL && <div className="p-6 text-[13px] text-muted">预览服务不可用。</div>}
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
