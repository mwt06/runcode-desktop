import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import {
  HTML_PREVIEW_SANDBOX, createHeightGovernor, probeIframeContentHeight,
} from '@/preview/static-html'
import { HTML_SCRIPT_PREVIEW_SANDBOX, readHtmlPreviewMessage } from '@/preview/html-document'
import {
  clampHtmlZoomPercent, htmlZoomScrollLeft, PREVIEW_LIMITS, stepHtmlZoomPercent,
} from '@/preview/static-detect'
import { previewFailureHint } from '@/preview/preview-failure'
import { InlineError } from '@/ui/feedback'
import { useHtmlPreviewSlot } from './inline-preview-budget'

export type InlineHtmlPreviewMode = 'static' | 'scripted'

/**
 * HTML 内联预览入口：负责活动 iframe 名额、离屏高度保存和回退源码。
 * 具体 iframe 缩放、高度治理与 postMessage 校验留在 HtmlFrame 内。
 */
export function HtmlPreviewGate({
  srcdoc, mode, channel, zoomPercent, onZoomPercentChange, defaultNode, onHeightChange, onFallback,
}: {
  srcdoc: string
  mode: InlineHtmlPreviewMode
  channel: string | null
  zoomPercent: number
  onZoomPercentChange: (percent: number) => void
  defaultNode: ReactNode
  onHeightChange?: () => void
  onFallback?: (reason: string) => void
}) {
  const shellRef = useRef<HTMLDivElement>(null)
  const hasSlot = useHtmlPreviewSlot(shellRef, true)
  const [savedHeight, setSavedHeight] = useState<number | null>(null)
  const noIo = typeof IntersectionObserver === 'undefined'
  const unsupported = noIo && !hasSlot
  const onFallbackRef = useRef(onFallback)
  onFallbackRef.current = onFallback

  useEffect(() => {
    if (unsupported) onFallbackRef.current?.('platform-unsupported')
  }, [unsupported])

  // 无 IntersectionObserver 且拿不到名额 → 保留源码，不显示无限空白。
  if (unsupported) {
    return (
      <div className="my-2 flex flex-col gap-1">
        {defaultNode}
        <p className="text-[12px] text-faint px-0.5" role="note">
          未显示预览：{previewFailureHint('platform-unsupported')}
        </p>
      </div>
    )
  }

  return (
    <div ref={shellRef}>
      {hasSlot ? (
        <HtmlFrame
          srcdoc={srcdoc}
          mode={mode}
          channel={channel}
          zoomPercent={zoomPercent}
          onZoomPercentChange={onZoomPercentChange}
          onHeightChange={onHeightChange}
          onMeasured={(h) => setSavedHeight(h)}
        />
      ) : (
        // 等待名额或暂时离屏：占位，不当渲染失败。
        <div
          className="rounded-lg border border-line2 bg-inset/40"
          style={{ minHeight: savedHeight ?? 48 }}
          aria-hidden
        />
      )}
    </div>
  )
}

function HtmlFrame({
  srcdoc, mode, channel, zoomPercent, onZoomPercentChange, onHeightChange, onMeasured,
}: {
  srcdoc: string
  mode: InlineHtmlPreviewMode
  channel: string | null
  zoomPercent: number
  onZoomPercentChange: (percent: number) => void
  onHeightChange?: () => void
  onMeasured?: (height: number) => void
}) {
  const displayRef = useRef<HTMLDivElement>(null)
  const shellRef = useRef<HTMLDivElement>(null)
  const ref = useRef<HTMLIFrameElement>(null)
  const gov = useRef(createHeightGovernor({ max: PREVIEW_LIMITS.htmlMaxHeight }))
  const designW = PREVIEW_LIMITS.htmlDesignWidth
  const maxVisual = PREVIEW_LIMITS.htmlInlineMaxHeight
  const probeH = PREVIEW_LIMITS.htmlMaxHeight
  const initialHeight = mode === 'scripted' ? PREVIEW_LIMITS.htmlScriptViewportHeight : 120
  const [contentH, setContentH] = useState(initialHeight)
  const [innerScroll, setInnerScroll] = useState(false)
  const [scriptError, setScriptError] = useState(false)
  const [viewportW, setViewportW] = useState(0)
  const previousLayout = useRef({ width: 0, viewport: 0, scrollLeft: 0 })
  const onZoomRef = useRef(onZoomPercentChange)
  onZoomRef.current = onZoomPercentChange
  const zoomRef = useRef(zoomPercent)
  zoomRef.current = zoomPercent
  const onMeasuredRef = useRef(onMeasured)
  onMeasuredRef.current = onMeasured
  const onHeightChangeRef = useRef(onHeightChange)
  onHeightChangeRef.current = onHeightChange

  useEffect(() => {
    gov.current.reset()
    setContentH(initialHeight)
    setInnerScroll(false)
    setScriptError(false)
    previousLayout.current = { width: 0, viewport: 0, scrollLeft: 0 }
  }, [srcdoc, initialHeight])

  useEffect(() => {
    const el = shellRef.current
    if (!el) return
    const sync = () => {
      const w = el.clientWidth
      if (w > 0) setViewportW(w)
    }
    sync()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => sync())
    ro.observe(el)
    return () => ro.disconnect()
  }, [srcdoc, designW])

  // 父壳与静态文档共用缩放步进；脚本页面只能通过校验后的消息请求缩放。
  const stepWheelRef = useRef<(direction: 1 | -1) => void>(() => {})
  stepWheelRef.current = (direction) => {
    const next = stepHtmlZoomPercent(zoomRef.current, direction)
    zoomRef.current = next
    onZoomRef.current(next)
  }

  useEffect(() => {
    const el = shellRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return
      e.preventDefault()
      e.stopPropagation()
      stepWheelRef.current(e.deltaY < 0 ? 1 : -1)
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [srcdoc])

  useEffect(() => {
    if (mode !== 'scripted' || !channel) return
    const onMessage = (event: MessageEvent) => {
      // opaque origin 必须同时校验窗口与本代 channel，不能只凭 type 接受任意页面消息。
      if (event.source !== ref.current?.contentWindow || event.origin !== 'null') return
      const message = readHtmlPreviewMessage(event.data, channel)
      if (!message) return
      if ('height' in message) {
        const next = gov.current.observe(Math.max(initialHeight, message.height))
        setContentH(next.height)
        setInnerScroll(next.scroll)
      } else if ('zoom' in message) {
        stepWheelRef.current(message.zoom)
      } else {
        setScriptError(true)
      }
    }
    window.addEventListener('message', onMessage)
    return () => window.removeEventListener('message', onMessage)
  }, [mode, channel, initialHeight])

  useEffect(() => {
    if (mode !== 'static') return
    const frame = ref.current
    if (!frame) return
    let ro: ResizeObserver | null = null
    let wheelDoc: Document | null = null
    const onWheel = (event: WheelEvent) => {
      if (!event.ctrlKey && !event.metaKey) return
      event.preventDefault()
      event.stopPropagation()
      stepWheelRef.current(event.deltaY < 0 ? 1 : -1)
    }
    const measure = () => {
      const doc = frame.contentDocument
      if (!doc?.body) return
      // 探测高度后再收敛，避免短 iframe 下主栏塌缩。
      const probed = probeIframeContentHeight(frame, probeH)
      const next = gov.current.observe(probed)
      setContentH(next.height)
      setInnerScroll(next.scroll)
      const overflow = next.scroll ? 'auto' : 'hidden'
      doc.documentElement.style.overflow = overflow
      doc.body.style.overflow = overflow
      onHeightChangeRef.current?.()
    }
    const onLoad = () => {
      wheelDoc?.removeEventListener('wheel', onWheel)
      wheelDoc = frame.contentDocument
      wheelDoc?.addEventListener('wheel', onWheel, { passive: false })
      measure()
      ro?.disconnect()
      if (typeof ResizeObserver !== 'undefined' && frame.contentDocument?.body) {
        ro = new ResizeObserver(() => measure())
        ro.observe(frame.contentDocument.body)
      }
    }
    frame.addEventListener('load', onLoad)
    if (frame.contentDocument?.readyState === 'complete') onLoad()
    return () => {
      frame.removeEventListener('load', onLoad)
      wheelDoc?.removeEventListener('wheel', onWheel)
      ro?.disconnect()
    }
  }, [srcdoc, probeH, mode])

  const scale = clampHtmlZoomPercent(zoomPercent) / 100
  const scaledW = designW * scale
  const scaledH = Math.ceil(contentH * scale)
  const shellH = Math.min(maxVisual, scaledH)

  useLayoutEffect(() => {
    const shell = shellRef.current
    if (!shell || viewportW <= 0) return
    const nextScrollLeft = htmlZoomScrollLeft({
      previousWidth: previousLayout.current.width,
      previousViewport: previousLayout.current.viewport,
      scrollLeft: previousLayout.current.scrollLeft,
      width: scaledW,
      viewport: viewportW,
    })
    shell.scrollLeft = nextScrollLeft
    previousLayout.current = { width: scaledW, viewport: viewportW, scrollLeft: nextScrollLeft }
  }, [scaledW, viewportW])

  useEffect(() => {
    // 错误提示也是预览占位的一部分，离屏卸载时一并保存其真实高度。
    onMeasuredRef.current?.(displayRef.current?.getBoundingClientRect().height || shellH)
    onHeightChangeRef.current?.()
  }, [shellH, scriptError])

  return (
    <div ref={displayRef}>
      {scriptError && <InlineError variant="text" className="px-2.5 py-2">页面脚本执行失败或资源加载受限，部分内容可能未显示；可切换源码查看。</InlineError>}
      <div
        ref={shellRef}
        data-html-inline-shell
        data-html-inner-scroll={innerScroll ? '1' : '0'}
        onScroll={(event) => {
          const actual = event.currentTarget.scrollLeft
          if (Math.abs(actual - previousLayout.current.scrollLeft) > 1) previousLayout.current.scrollLeft = actual
        }}
        className="w-full max-w-full overflow-x-auto overflow-y-auto bg-surface"
        style={{ maxHeight: maxVisual, height: shellH, scrollbarGutter: 'stable' }}
      >
        <div className="mx-auto" style={{ width: scaledW, height: scaledH, overflow: 'hidden' }}>
          <iframe
            ref={ref}
            title="HTML 预览"
            sandbox={mode === 'scripted' ? HTML_SCRIPT_PREVIEW_SANDBOX : HTML_PREVIEW_SANDBOX}
            data-html-mode={mode}
            data-html-channel={channel ?? undefined}
            srcDoc={srcdoc}
            className="block border-0 bg-white"
            style={{
              width: designW,
              height: contentH,
              transform: `scale(${scale})`,
              transformOrigin: 'top left',
            }}
          />
        </div>
      </div>
    </div>
  )
}
