// PreviewPanel renders one workspace artifact by type: HTML in a sandboxed iframe
// and images by the loopback static-server URL; Markdown/code/text by fetching the
// text via ReadArtifact and rendering it in React; Office docs (docx/pptx/xlsx) via
// lazy-loaded in-browser viewers; PDF by the WebView's native viewer.
import { useEffect, useRef, useState, type RefObject } from 'react'
import { Icon } from '@/ui/icons'
import { Markdown } from '@/ui/markdown'
import { basename } from '@/core/paths'
import { copyText, errCode, errText, openExternal, readArtifact, renderOfficePDF, resolveArtifactPath } from '@/core/bridge'
import { artifactKindLabel, classifyPreview, fileColor, kindIcon, previewSrc, type PreviewKind } from './classify'
import { IconBtn } from './icon-btn'
import { ImperativeDocView, renderDocx } from './viewers/doc-view'
import { PptxView } from './viewers/pptx-view'
import { XlsxView } from './viewers/xlsx-view'
import { InlineError } from '@/ui/feedback'
import { PreviewLoading, PreviewSkeleton } from './loading'

// OfficePDFState 是 Office 文档的三态：转换中、拿到 PDF、退回内嵌渲染器。
type OfficePDFState = { state: 'converting'; pdfRel: '' } | { state: 'ready'; pdfRel: string } | { state: 'fallback'; pdfRel: '' }

// 内嵌文档框（html / 转出的 PDF / 原生 PDF）共用：撑满、无边框，并显式给白底——
// 预览的是文档，不能让应用的浅灰背景透过来当纸面。
const FRAME = 'w-full h-full border-0 bg-white'

const isOfficeKind = (kind: PreviewKind) => kind === 'docx' || kind === 'pptx' || kind === 'xlsx'

// useOfficePDF 请求本机 Office 把文档转成 PDF。非 Office 文档直接落 fallback（调用方
// 本来就不会用到它）。任何失败都落 fallback，只有"文件不存在"特殊对待——那是陈旧的
// 卡片指向了被移走的文件，静默关掉标签页，和文本预览一个规矩。
function useOfficePDF(relPath: string, kind: PreviewKind, reloadKey: number, onMissing: RefObject<() => void>): OfficePDFState {
  const isOffice = isOfficeKind(kind)
  // 初值跟着类型走：非 Office 文档若从 'converting' 起步，会在 effect 跑之前先闪一行
  // "正在生成高保真预览"——那是它永远不会用到的状态。
  const [state, setState] = useState<OfficePDFState>(isOffice ? { state: 'converting', pdfRel: '' } : { state: 'fallback', pdfRel: '' })
  useEffect(() => {
    if (!isOffice) { setState({ state: 'fallback', pdfRel: '' }); return }
    let ignore = false
    setState({ state: 'converting', pdfRel: '' })
    renderOfficePDF(relPath)
      .then((pdfRel) => { if (!ignore) setState({ state: 'ready', pdfRel }) })
      .catch((e) => {
        if (ignore) return
        if (errCode(e) === 'not_found') { onMissing.current?.(); return }
        setState({ state: 'fallback', pdfRel: '' })
      })
    return () => { ignore = true }
  }, [relPath, isOffice, reloadKey, onMissing])
  return state
}

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
  // onClose is an escape hatch, not effect data: keep it in a ref so a re-render
  // with a fresh arrow never re-triggers the read below.
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose
  const textual = kind === 'markdown' || kind === 'code' || kind === 'text'

  useEffect(() => {
    if (!textual) return
    let ignore = false
    setText(null)
    setErr('')
    readArtifact(relPath)
      .then((t) => { if (!ignore) setText(t) })
      .catch((e) => {
        if (ignore) return
        // The file is gone (a card in the conversation still points at where it used
        // to be, or a later step moved it): close the tab instead of parking a red
        // error where a preview should be. There is nothing for the user to do about
        // it, and "预览" that only ever shows an error is worse than not opening.
        if (errCode(e) === 'not_found') { onCloseRef.current(); return }
        setErr(errText(e))
      })
    return () => { ignore = true }
  }, [relPath, kind, bust, textual])

  // Office 文档优先让本机装的 Office 转成 PDF 再看:内嵌的 JS 渲染器在文字度量与
  // 字体回退上和真 Office 差得明显,而用户对照的正是 WPS/PowerPoint 里的样子。
  // 转不了(没装 Office、非 Windows、调用失败)就退回内嵌渲染器——降级方向是安全的。
  const office = useOfficePDF(relPath, kind, bust, onCloseRef)

  const copyPath = async () => {
    try { await copyText(await resolveArtifactPath(relPath)) } catch { /* non-fatal */ }
  }

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface">
      <div className="flex-none flex items-center gap-1.5 h-[44px] px-2.5 border-b border-line2">
        <span className="flex-none" style={{ color: fileColor(relPath) }}><Icon name={kindIcon(kind)} size={15} /></span>
        <span className="flex-none text-[11px] text-faint bg-inset rounded px-1.5 py-0.5 mr-auto">{artifactKindLabel(kind)}</span>
        <IconBtn name="refresh" title="刷新" onClick={() => setBust((v) => v + 1)} />
        <IconBtn name="external-link" title="用系统默认程序打开" onClick={() => { openExternal(relPath).catch(() => {}) }} />
        <IconBtn name="copy" title="复制路径" onClick={copyPath} />
        <IconBtn name="win-close" title="关闭" onClick={onClose} />
      </div>
      <div className="flex-1 min-h-0 overflow-auto">
        {kind === 'html' && baseURL && (
          <iframe title={name} src={previewSrc(baseURL, relPath, bust)} className={FRAME} sandbox="allow-scripts allow-forms allow-popups allow-modals" />
        )}
        {(kind === 'image' || kind === 'svg') && baseURL && (
          <div className="p-4 flex items-center justify-center min-h-full bg-inset/30"><img src={previewSrc(baseURL, relPath, bust)} alt={name} className="max-w-full" /></div>
        )}
        {kind === 'markdown' && text != null && <div className="p-4"><Markdown>{text}</Markdown></div>}
        {kind === 'code' && text != null && <div className="p-4"><Markdown>{fencedCode(text, lang)}</Markdown></div>}
        {kind === 'text' && text != null && (
          <pre className="m-0 p-4 font-mono text-[13px] leading-[1.6] whitespace-pre-wrap break-words">{text}</pre>
        )}
        {/* 读取中（且没出错）：先摆骨架，别把面板留成空白——空白分不清是在加载还是
            文件本身就是空的。err 时不画，那边已经有错误在说话了。 */}
        {textual && text == null && !err && <PreviewSkeleton />}
        {isOfficeKind(kind) && office.state === 'converting' && (
          <PreviewLoading hint="正在用本机 Office 生成高保真预览…" />
        )}
        {/* 转出来的 PDF 就放在工作区的 .runcode 下，走的仍是既有的 PDF 预览通路。 */}
        {isOfficeKind(kind) && office.state === 'ready' && baseURL && (
          <iframe title={name} src={previewSrc(baseURL, office.pdfRel, bust)} className={FRAME} />
        )}
        {isOfficeKind(kind) && office.state === 'ready' && !baseURL && <div className="p-6 text-[13px] text-muted">预览服务不可用。</div>}
        {office.state === 'fallback' && kind === 'docx' && <ImperativeDocView relPath={relPath} reloadKey={bust} load={renderDocx} busyHint=" Word" onMissing={onClose} />}
        {office.state === 'fallback' && kind === 'pptx' && <PptxView relPath={relPath} reloadKey={bust} onMissing={onClose} />}
        {office.state === 'fallback' && kind === 'xlsx' && <XlsxView relPath={relPath} reloadKey={bust} onMissing={onClose} />}
        {kind === 'pdf' && baseURL && (
          <iframe title={name} src={previewSrc(baseURL, relPath, bust)} className={FRAME} />
        )}
        {kind === 'pdf' && !baseURL && <div className="p-6 text-[13px] text-muted">预览服务不可用。</div>}
        {kind === 'unsupported' && (
          <div className="p-6 text-[13px] text-muted">该文件类型暂不支持预览。<button className="text-primaryink underline ml-1" onClick={() => openExternal(relPath).catch(() => {})}>用系统程序打开</button></div>
        )}
        {(kind === 'html' || kind === 'image' || kind === 'svg') && !baseURL && (
          <div className="p-6 text-[13px] text-muted">预览服务不可用。</div>
        )}
        {err && <InlineError variant="text" className="p-6">{err}</InlineError>}
      </div>
    </div>
  )
}
