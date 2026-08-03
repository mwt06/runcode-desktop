// PptxView presents a deck one slide at a time with left/right navigation and a page
// indicator. pptx-preview renders every slide stacked (list mode) into the host —
// including a hard-coded black wrapper background that styles.css overrides — and we
// turn that into a slideshow by showing only the active slide. It sizes each slide to
// the host width, deriving the height from the deck's real aspect ratio itself.
import { useEffect, useRef, useState } from 'react'
import { Icon } from '@/ui/icons'
import { errCode, errText, readArtifactBytes } from '@/core/bridge'
import { ViewerError } from './viewer-error'

// onMissing fires when the file is gone rather than unreadable, so the panel can
// close the tab instead of showing an error for something the user cannot fix.
export function PptxView({ relPath, reloadKey, onMissing }: { relPath: string; reloadKey: number; onMissing?: () => void }) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const slidesRef = useRef<HTMLElement[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [err, setErr] = useState('')
  const [count, setCount] = useState(0)
  const [idx, setIdx] = useState(0)
  const onMissingRef = useRef(onMissing)
  onMissingRef.current = onMissing

  useEffect(() => {
    let cancelled = false
    setState('loading')
    setErr('')
    setCount(0)
    setIdx(0)
    slidesRef.current = []
    void (async () => {
      try {
        const buf = await readArtifactBytes(relPath)
        const { init } = await import('pptx-preview')
        const host = hostRef.current
        if (cancelled || !host) return
        host.replaceChildren()
        const width = Math.max(360, Math.floor(host.clientWidth) || 900)
        const previewer = init(host, { width, mode: 'list' })
        await previewer.preview(buf)
        if (cancelled) return
        const slides = Array.from(host.querySelectorAll<HTMLElement>('.pptx-preview-slide-wrapper'))
        slides.forEach((el, i) => { el.style.display = i === 0 ? '' : 'none' })
        slidesRef.current = slides
        setCount(slides.length)
        setState('ready')
      } catch (e) {
        if (!cancelled) {
          if (errCode(e) === 'not_found') { onMissingRef.current?.(); return }
          setErr(errText(e))
          setState('error')
        }
      }
    })()
    return () => { cancelled = true }
  }, [relPath, reloadKey])

  // Navigation toggles DOM visibility directly (the slides are the library's nodes,
  // not React's) and mirrors the position into state for the indicator.
  const go = (to: number) => {
    const clamped = Math.max(0, Math.min(to, slidesRef.current.length - 1))
    slidesRef.current.forEach((el, i) => { el.style.display = i === clamped ? '' : 'none' })
    setIdx(clamped)
  }

  if (state === 'error') return <ViewerError relPath={relPath} message={err} />
  return (
    <div
      className="relative flex flex-col h-full min-h-0 bg-inset/40 outline-none"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'ArrowLeft') { e.preventDefault(); go(idx - 1) }
        else if (e.key === 'ArrowRight') { e.preventDefault(); go(idx + 1) }
      }}
    >
      {state === 'loading' && <div className="p-6 text-[13px] text-muted">正在加载幻灯片预览…</div>}
      <div className="flex-1 min-h-0 overflow-auto p-3">
        {/* min-h-full + items-center 让幻灯片在面板里垂直居中；内容比面板高时容器随之
            撑高、从顶部正常滚动，不会裁掉顶部。水平居中由 pptx-host 的 margin:auto 负责。 */}
        <div className="min-h-full flex items-center justify-center">
          <div ref={hostRef} className="pptx-host w-full" />
        </div>
      </div>
      {state === 'ready' && count > 1 && (
        <>
          <button
            type="button" aria-label="上一页" title="上一页 (←)" disabled={idx === 0} onClick={() => go(idx - 1)}
            className="absolute left-2 top-1/2 -translate-y-1/2 w-9 h-9 rounded-full bg-surface/90 border border-line2 shadow-card flex items-center justify-center text-muted hover:text-ink disabled:opacity-30 disabled:cursor-default"
          >
            <Icon name="chevron-down" size={18} className="rotate-90" />
          </button>
          <button
            type="button" aria-label="下一页" title="下一页 (→)" disabled={idx === count - 1} onClick={() => go(idx + 1)}
            className="absolute right-2 top-1/2 -translate-y-1/2 w-9 h-9 rounded-full bg-surface/90 border border-line2 shadow-card flex items-center justify-center text-muted hover:text-ink disabled:opacity-30 disabled:cursor-default"
          >
            <Icon name="chevron-down" size={18} className="-rotate-90" />
          </button>
          <div className="absolute bottom-3 left-1/2 -translate-x-1/2 px-3 py-1 rounded-full bg-surface/90 border border-line2 shadow-card text-[12px] text-muted tabular-nums select-none">
            {idx + 1} / {count}
          </div>
        </>
      )}
    </div>
  )
}
