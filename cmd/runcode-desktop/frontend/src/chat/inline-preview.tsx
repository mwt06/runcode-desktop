// InlinePreview 把一段已判定可预览的内容画进助手消息：成功时显示图形，失败时
// 退回传入的 defaultNode（原代码块）并附带可读原因。代码和预览共用同一份解析结果。
//
// HTML 另受 InlinePreviewBudgetProvider 名额约束（liveFrames）：有名额且靠近视口才挂
// iframe；等待名额显示占位（不当失败）；离屏卸载保留已测高度。SVG／Mermaid 不占名额。
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { sanitizeSvg } from '@/preview/static-svg'
import { prepareHtmlPreview } from '@/preview/html-document'
import { renderMermaid } from '@/preview/static-mermaid-loader'
import {
  clampHtmlZoomPercent, contentDigest, HTML_ZOOM_MAX, HTML_ZOOM_MIN,
  stepHtmlZoomPercent, type StaticPreviewKind,
} from '@/preview/static-detect'
import { logPreviewFailure, previewFailureHint } from '@/preview/preview-failure'
import { noteInlineFile, type InlineFileStatus } from '@/session/inline-preview-status'
import { copyText } from '@/core/bridge'
import { SystemNote } from '@/ui/feedback'
import { IconBtn } from '@/preview/icon-btn'
import { ImageLightbox } from '@/preview/image-lightbox'
import { codePreviewTarget, type CodePreviewTarget } from '@/preview/code-preview'
import { downloadSvgText } from '@/preview/svg-download'
import { HtmlPreviewGate } from './inline-preview-html'

type Props = {
  kind: StaticPreviewKind
  source: string
  defaultNode: ReactNode
  /** 流式中只延后已闭合块；完成态和直出预览不增加等待。 */
  streaming?: boolean
  onHeightChange?: () => void
  messageId?: string
  /** 与本预览同扩展名、同条消息里提到的工作区相对路径；用于抑制右侧自动预览。 */
  relatedPaths?: string[]
  sessionId?: string
  previewId?: string
  onOpenCodePreview?: (target: CodePreviewTarget) => void
}

type Ready =
  | { kind: 'svg' | 'mermaid'; url: string; svg: string; width: number; height: number }
  | { kind: 'html'; srcdoc: string; mode: 'static' | 'scripted'; channel: string | null; blocked: { scripts: number; externalStyles: number } }

const PREVIEW_TITLE: Record<StaticPreviewKind, string> = {
  mermaid: 'Mermaid 预览',
  svg: 'SVG 预览',
  html: 'HTML 预览',
}

const INLINE_IMAGE_MAX_HEIGHT = 'clamp(240px, 42vh, 420px)'

const STREAMING_DEBOUNCE_MS = 200

function reportPaths(sessionId: string | undefined, paths: string[] | undefined, status: InlineFileStatus) {
  if (!sessionId || !paths?.length) return
  for (const p of paths) noteInlineFile(sessionId, p, status)
}

export function InlinePreview({
  kind, source, defaultNode, streaming = false, onHeightChange, messageId, relatedPaths, sessionId,
  previewId, onOpenCodePreview,
}: Props) {
  const [renderSource, setRenderSource] = useState<string | null>(() => streaming ? null : source)
  const [ready, setReady] = useState<Ready | null>(null)
  const [failReason, setFailReason] = useState<string | null>(null)
  const [view, setView] = useState<'preview' | 'source'>('preview')
  const [lightboxOpen, setLightboxOpen] = useState(false)
  /** HTML 用户缩放（相对设计宽）；null 表示使用初始 100%。 */
  const [zoomPercent, setZoomPercent] = useState<number | null>(null)
  const urlRef = useRef<string | null>(null)
  const notify = useRef(onHeightChange)
  notify.current = onHeightChange
  // 代次：源变更／卸载后迟到的异步结果不得回填。
  const genRef = useRef(0)

  // 路径列表用稳定键，避免父组件每次新数组引用导致重渲循环。
  const pathsKey = relatedPaths?.join('\0') ?? ''
  const pathsRef = useRef(relatedPaths)
  pathsRef.current = relatedPaths

  // 流式时只延后已经闭合的围栏；源内容变化时先撤掉旧结果，避免展示过期图。
  useEffect(() => {
    if (!streaming) {
      setRenderSource(source)
      return
    }
    setRenderSource(null)
    setReady(null)
    setFailReason(null)
    setLightboxOpen(false)
    if (urlRef.current) {
      URL.revokeObjectURL(urlRef.current)
      urlRef.current = null
    }
    const timer = window.setTimeout(() => setRenderSource(source), STREAMING_DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [source, streaming])

  useEffect(() => {
    if (renderSource === null) return
    const stableSource = renderSource
    const gen = ++genRef.current
    let cancelled = false
    const ac = new AbortController()
    const paths = pathsRef.current
    setLightboxOpen(false)
    setReady(null)
    setFailReason(null)
    reportPaths(sessionId, paths, 'pending')

    const fail = (reason: string) => {
      if (cancelled || gen !== genRef.current) return
      setFailReason(reason)
      logPreviewFailure({ kind, reason, messageId })
      reportPaths(sessionId, paths, 'fallback')
    }

    async function run() {
      try {
        if (kind === 'svg') {
          const r = sanitizeSvg(stableSource)
          if (!r.ok) { fail(r.reason); return }
          const url = URL.createObjectURL(new Blob([r.svg], { type: 'image/svg+xml' }))
          if (cancelled || gen !== genRef.current) { URL.revokeObjectURL(url); return }
          if (urlRef.current) URL.revokeObjectURL(urlRef.current)
          urlRef.current = url
          setReady({ kind: 'svg', url, svg: r.svg, width: r.width, height: r.height })
          reportPaths(sessionId, paths, 'rendered')
          return
        }
        if (kind === 'html') {
          const r = prepareHtmlPreview(stableSource)
          if (!r.ok) { fail(r.reason); return }
          if (!cancelled && gen === genRef.current) {
            setReady({ ...r, kind: 'html' })
            reportPaths(sessionId, paths, 'rendered')
          }
          return
        }
        const r = await renderMermaid(stableSource, ac.signal)
        if (cancelled || ac.signal.aborted || gen !== genRef.current) return
        if (!r.ok) { fail(r.reason); return }
        const url = URL.createObjectURL(new Blob([r.svg], { type: 'image/svg+xml' }))
        if (cancelled || gen !== genRef.current) { URL.revokeObjectURL(url); return }
        if (urlRef.current) URL.revokeObjectURL(urlRef.current)
        urlRef.current = url
        setReady({ kind: 'mermaid', url, svg: r.svg, width: r.width, height: r.height })
        reportPaths(sessionId, paths, 'rendered')
      } catch (err) {
        const msg = err instanceof Error ? err.message : 'unknown'
        fail(/CSSStyleSheet/i.test(msg) ? 'platform-unsupported' : 'syntax')
      }
    }
    void run()
    return () => {
      cancelled = true
      ac.abort()
      if (urlRef.current) { URL.revokeObjectURL(urlRef.current); urlRef.current = null }
    }
  }, [kind, renderSource, messageId, sessionId, pathsKey])

  useEffect(() => {
    if (ready) notify.current?.()
  }, [ready])

  // 换 HTML 源：清用户缩放，新页面重新从 100% 开始。
  const htmlSrcdoc = ready?.kind === 'html' ? ready.srcdoc : null
  useEffect(() => {
    if (!htmlSrcdoc) return
    setZoomPercent(null)
  }, [htmlSrcdoc])

  const title = PREVIEW_TITLE[kind]
  const imageReady = ready && ready.kind !== 'html' ? ready : null
  const canFullscreen = targetFullscreen(imageReady, view)
  const digest = contentDigest(source.trim())
  const displayZoom = zoomPercent ?? 100
  const applyZoom = (next: number) => {
    setZoomPercent(clampHtmlZoomPercent(next))
  }
  /** 用函数式更新，避免连点／同步多击仍读到同一帧的旧比例。 */
  const stepZoom = (direction: 1 | -1) => {
    setZoomPercent((cur) => stepHtmlZoomPercent(cur ?? 100, direction))
  }
  const openSidePreview = () => {
    if (!onOpenCodePreview) return
    onOpenCodePreview(codePreviewTarget({
      kind, source, title, sessionId, messageId, previewId, relatedPaths,
    }))
  }
  const onCopySource = () => {
    void copyText(source).catch(() => {})
  }
  const controls = ready ? (
    <div data-inline-preview-controls className={FLOATING_CONTROLS}>
      {kind === 'html' && view === 'preview' && onOpenCodePreview ? (
        <IconBtn name="external-link" title="在右侧预览打开" onClick={openSidePreview} />
      ) : null}
      {view === 'source' ? (
        <IconBtn name="copy" title="复制源码" onClick={onCopySource} />
      ) : null}
      {kind === 'html' && view === 'preview' ? (
        <div className="flex-none inline-flex items-center gap-0.5" data-html-zoom-controls>
          <button
            type="button"
            className={FLOATING_TOOL_BTN}
            aria-label="缩小"
            title="缩小"
            disabled={displayZoom <= HTML_ZOOM_MIN}
            onClick={() => stepZoom(-1)}
          >
            −
          </button>
          <span
            className="min-w-10 text-center text-[12px] tabular-nums text-muted px-0.5"
            title={`缩放 ${displayZoom}%`}
            aria-live="polite"
          >
            {displayZoom}%
          </span>
          <button
            type="button"
            className={FLOATING_TOOL_BTN}
            aria-label="放大"
            title="放大"
            disabled={displayZoom >= HTML_ZOOM_MAX}
            onClick={() => stepZoom(1)}
          >
            +
          </button>
        </div>
      ) : null}
      <button
        type="button"
        className={FLOATING_TOOL_BTN}
        onClick={() => setView(view === 'preview' ? 'source' : 'preview')}
      >
        {view === 'source' ? (kind === 'html' ? '查看页面' : '查看图片') : '查看代码'}
      </button>
      {imageReady && view === 'preview' && (
        <IconBtn
          name="download"
          title="下载 SVG"
          onClick={() => downloadSvgText(imageReady.svg, `code-${kind}-${digest}.svg`)}
        />
      )}
      {canFullscreen && <IconBtn name="expand" title="全屏" onClick={() => setLightboxOpen(true)} />}
    </div>
  ) : null

  if (failReason) {
    return (
      <div className="my-2 min-w-0 py-1.5">
        {defaultNode}
        <p className="text-[12px] text-faint px-0.5 mt-1.5" role="note">
          未显示预览：{previewFailureHint(failReason)}
        </p>
      </div>
    )
  }
  if (!ready) {
    return <div className="my-2 min-h-12" aria-hidden />
  }
  if (ready.kind === 'html') {
    return (
      <div className="group relative my-2 min-w-0">
        {controls}
        {ready.blocked.scripts + ready.blocked.externalStyles > 0 && (
          <SystemNote tone="warning">页面依赖的外部脚本或样式未加载，部分内容可能未显示；可切换源码查看。</SystemNote>
        )}
        {view === 'source' ? (
          <SourceShell>{defaultNode}</SourceShell>
        ) : (
          <HtmlPreviewGate
            key={ready.srcdoc}
            srcdoc={ready.srcdoc}
            mode={ready.mode}
            channel={ready.channel}
            zoomPercent={displayZoom}
            onZoomPercentChange={applyZoom}
            defaultNode={defaultNode}
            onHeightChange={() => notify.current?.()}
            onFallback={(reason) => {
              setFailReason(reason)
              logPreviewFailure({ kind: 'html', reason, messageId })
              reportPaths(sessionId, relatedPaths, 'fallback')
            }}
          />
        )}
      </div>
    )
  }
  const imageWidth = Math.max(1, ready.width)
  const imageHeight = Math.max(1, ready.height)
  const frameWidth = `min(${imageWidth}px, 100%, calc(${INLINE_IMAGE_MAX_HEIGHT} * ${imageWidth / imageHeight}))`
  return (
    <div className="my-2 min-w-0">
      {view === 'source' ? (
        <div className="group relative min-w-0">
          {controls}
          <SourceShell>{defaultNode}</SourceShell>
        </div>
      ) : (
        <div className="group relative block max-w-full mx-auto" style={{ width: frameWidth }}>
          {controls}
          {/* 图面不再单击开右侧专用预览（尤其 Mermaid inline-static）；全屏走顶栏。 */}
          <img
            src={ready.url}
            alt={`${title}图`}
            width={ready.width}
            height={ready.height}
            className="block max-w-full w-auto h-auto object-contain"
            style={{ width: '100%', height: 'auto' }}
            onLoad={() => notify.current?.()}
          />
        </div>
      )}
      <ImageLightbox
        open={lightboxOpen && !!imageReady}
        src={imageReady?.url ?? ''}
        alt={`${title}图`}
        mode="vector"
        onClose={() => setLightboxOpen(false)}
      />
    </div>
  )
}

const FLOATING_CONTROLS = 'absolute right-2 top-2 z-20 inline-flex items-center gap-1 rounded-lg border border-line2/70 bg-surface/95 p-1 shadow-card backdrop-blur-sm opacity-0 pointer-events-none transition-opacity group-hover:opacity-100 group-hover:pointer-events-auto group-focus-within:opacity-100 group-focus-within:pointer-events-auto'
const FLOATING_TOOL_BTN = 'flex-none inline-flex items-center gap-1 h-7 px-2 rounded-md text-[12px] text-muted hover:text-ink hover:bg-surface2'

/** 源码视图：限高 + 纵向滚动 + 显示折行；复制走 header 的原始 source，不依赖选区。 */
function SourceShell({ children }: { children: ReactNode }) {
  return (
    <div
      data-inline-source-shell
      className="p-2.5 max-h-[min(50vh,480px)] overflow-y-auto overflow-x-hidden break-words whitespace-pre-wrap [&_pre]:whitespace-pre-wrap [&_pre]:break-words [&_code]:whitespace-pre-wrap [&_code]:break-words"
    >
      {children}
    </div>
  )
}

function targetFullscreen(ready: Extract<Ready, { kind: 'svg' | 'mermaid' }> | null, view: 'preview' | 'source'): boolean {
  return !!ready && view === 'preview'
}

/** HTML：先占位并观察视口，有名额才挂 HtmlFrame；无 IO 且无名额时回退源码。 */
