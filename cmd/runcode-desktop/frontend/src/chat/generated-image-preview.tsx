// GeneratedImagePreview：执行卡行下的等比例预览舞台（扫光／揭示／角标操作）。
// 资源按来源 session+path 申请 ArtifactReference；动画资格绑定 toolUseID，不绑临时 URL。
import {
  useContext, useEffect, useId, useLayoutEffect, useRef, useState,
  type Ref, type RefObject, type ReactNode,
} from 'react'
import {
  openArtifact, releaseArtifact, errCode,
  type ArtifactReference, type ImageGenerationArtifact, type ToolEvent,
} from '@/core/bridge'
import { basename } from '@/core/paths'
import { Icon } from '@/ui/icons'
import { ArtifactActionsContext } from './artifact-actions'
import { IMAGE_GENERATION_ANNOTATION_UI } from './image-generation-annotation'
import {
  computePreviewDisplaySize, parseSizeHint, resolvePreviewGeometry,
  type PreviewGeometry,
} from './generated-image-size'
import './generated-image-preview.css'

const REVEAL_STRIPS = 8
const REVEAL_FALLBACK_MS = 700

function useVisible(ref: RefObject<HTMLElement | null>): boolean {
  const [visible, setVisible] = useState(false)
  useEffect(() => {
    const el = ref.current
    if (!el || visible) return
    if (typeof IntersectionObserver === 'undefined') {
      setVisible(true)
      return
    }
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        setVisible(true)
        io.disconnect()
      }
    })
    io.observe(el)
    return () => io.disconnect()
  }, [ref, visible])
  return visible
}

function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = useState(() =>
    typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches,
  )
  useEffect(() => {
    if (typeof matchMedia === 'undefined') return
    const mq = matchMedia('(prefers-reduced-motion: reduce)')
    const onChange = () => setReduced(mq.matches)
    mq.addEventListener?.('change', onChange)
    return () => mq.removeEventListener?.('change', onChange)
  }, [])
  return reduced
}

type Resource =
  | { status: 'idle' }
  | { status: 'loading'; path: string }
  | { status: 'ready'; path: string; artifact: ArtifactReference; decoded: boolean }
  | { status: 'missing'; path: string }
  | { status: 'error'; path: string }

export function GeneratedImagePreview({
  tool, sessionId, image, collapsed: collapsedProp, onToggleCollapse, onHeightChange, onView, onAnnotate, stageRef,
}: {
  tool: ToolEvent
  sessionId: string
  image?: ImageGenerationArtifact
  /** 受控折叠；省略时组件自管开关（对话卡默认）。 */
  collapsed?: boolean
  onToggleCollapse?: () => void
  onHeightChange?: () => void
  onView?: () => void
  onAnnotate?: () => void
  stageRef?: Ref<HTMLDivElement | null>
}) {
  const actions = useContext(ArtifactActionsContext)
  const rootRef = useRef<HTMLDivElement>(null)
  const localStageRef = useRef<HTMLDivElement>(null)
  const stage = (stageRef ?? localStageRef) as Ref<HTMLDivElement>
  const visible = useVisible(rootRef)
  const reduceMotion = usePrefersReducedMotion()
  const titleId = useId()
  const path = image?.path ?? ''
  const running = tool.type !== 'completed' && tool.type !== 'failed'
  const [resource, setResource] = useState<Resource>({ status: 'idle' })
  const [boxW, setBoxW] = useState(560)
  const [revealing, setRevealing] = useState(false)
  // 对话卡未传受控 props 时自管折叠；只传 onClick 空壳会导致「预览 …」点了无反应。
  const [uncontrolledCollapsed, setUncontrolledCollapsed] = useState(false)
  const controlled = collapsedProp !== undefined
  const collapsed = controlled ? collapsedProp : uncontrolledCollapsed
  const toggleCollapsed = () => {
    if (onToggleCollapse) onToggleCollapse()
    if (!controlled) setUncontrolledCollapsed((v) => !v)
  }
  const revealRootRef = useRef<HTMLDivElement>(null)
  const sawRunningRef = useRef(false)
  const revealedKeyRef = useRef('')
  const notify = useRef(onHeightChange)
  notify.current = onHeightChange

  if (running) sawRunningRef.current = true

  useLayoutEffect(() => {
    const el = rootRef.current
    if (!el) return
    // 根节点无水平内边距，直接用满可用宽，避免再人为减 32 把舞台夹小。
    const measure = () => setBoxW(Math.max(1, el.clientWidth))
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  useEffect(() => {
    if (!visible || !path || !sessionId || collapsed) return
    let cancelled = false
    let reference = ''
    setResource({ status: 'loading', path })
    openArtifact(sessionId, path)
      .then((artifact) => {
        if (cancelled || artifact.sessionId !== sessionId || artifact.path.replace(/\\/g, '/') !== path.replace(/\\/g, '/')) {
          void releaseArtifact(artifact.reference).catch(() => {})
          return
        }
        reference = artifact.reference
        setResource({ status: 'ready', path, artifact, decoded: false })
      })
      .catch((err) => {
        if (cancelled) return
        if (errCode(err) === 'not_found') setResource({ status: 'missing', path })
        else setResource({ status: 'error', path })
      })
    return () => {
      cancelled = true
      if (reference) void releaseArtifact(reference).catch(() => {})
    }
  }, [visible, path, sessionId, collapsed])

  const hint = parseSizeHint(tool)
  const naturalW = image?.width || hint?.w || 1024
  const naturalH = image?.height || hint?.h || 1024
  const vh = typeof window !== 'undefined' ? window.innerHeight : 800
  const layoutKey = `${sessionId}|${tool.toolUseID ?? path}`
  const layoutRef = useRef<PreviewGeometry | null>(null)
  layoutRef.current = resolvePreviewGeometry(layoutRef.current, {
    key: layoutKey,
    width: naturalW,
    height: naturalH,
    running,
    hint,
  })
  const display = computePreviewDisplaySize(
    layoutRef.current.width,
    layoutRef.current.height,
    boxW,
    vh,
  )
  const title = running ? '预览图片' : `预览 ${basename(path) || 'image.png'}`
  const readyUrl = resource.status === 'ready' && resource.path === path ? resource.artifact.url : ''
  const canInteract = !!readyUrl && resource.status === 'ready' && resource.decoded && !revealing && !running

  useEffect(() => {
    if (reduceMotion || collapsed) {
      setRevealing(false)
      return
    }
    if (resource.status !== 'ready' || !resource.decoded || resource.path !== path) return
    if (!sawRunningRef.current) return
    const key = `${sessionId}|${tool.toolUseID ?? ''}|${path}`
    if (revealedKeyRef.current === key) return
    revealedKeyRef.current = key
    setRevealing(true)
  }, [resource, path, sessionId, tool.toolUseID, reduceMotion, collapsed])

  useEffect(() => {
    if (!revealing || reduceMotion) return
    const root = revealRootRef.current
    const last = root?.querySelector('span:last-child')
    let done = false
    const clear = () => {
      if (done) return
      done = true
      setRevealing(false)
    }
    last?.addEventListener('animationend', clear)
    last?.addEventListener('animationcancel', clear)
    const t = window.setTimeout(clear, REVEAL_FALLBACK_MS)
    return () => {
      last?.removeEventListener('animationend', clear)
      last?.removeEventListener('animationcancel', clear)
      window.clearTimeout(t)
    }
  }, [revealing, reduceMotion])

  useEffect(() => {
    notify.current?.()
  }, [display.width, display.height, collapsed, resource.status, revealing])

  let body: ReactNode = null
  if (collapsed) {
    body = null
  } else if (resource.status === 'missing') {
    body = <p className="text-[12px] text-muted text-center py-6">文件不存在</p>
  } else if (resource.status === 'error') {
    body = (
      <div className="text-center py-6 space-y-2">
        <p className="text-[12px] text-muted">暂时无法预览</p>
        <button
          type="button"
          className="text-[12px] text-primary hover:underline"
          onClick={() => setResource({ status: 'idle' })}
        >
          重新加载预览
        </button>
      </div>
    )
  } else if (readyUrl) {
    // 已有 URL：挂 img 解码；未 decode 前叠扫光，decode 成功后再播揭示／开放交互。
    const decoded = resource.status === 'ready' && resource.decoded
    const showShimmer = !decoded || running
    body = (
      <div
        ref={stage as Ref<HTMLDivElement>}
        className="ig-preview-stage group relative mx-auto rounded-[12px] border border-line2 bg-surface2 overflow-hidden"
        style={{ width: display.width, height: display.height }}
      >
        <button
          type="button"
          className="block w-full h-full cursor-zoom-in focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
          disabled={!canInteract}
          onClick={() => { if (canInteract) onView?.() }}
          aria-label="查看大图"
        >
          <img
            src={readyUrl}
            alt=""
            width={display.width}
            height={display.height}
            className={`block w-full h-full object-contain${decoded ? '' : ' opacity-0'}`}
            onLoad={(e) => {
              const img = e.currentTarget
              const finish = () => {
                setResource((prev) =>
                  prev.status === 'ready' && prev.path === path ? { ...prev, decoded: true } : prev,
                )
              }
              if (typeof img.decode === 'function') {
                void img.decode().then(finish).catch(finish)
              } else {
                finish()
              }
            }}
          />
        </button>
        {showShimmer ? (
          <div
            className={`ig-preview-shimmer absolute inset-0 rounded-[12px]${reduceMotion ? '' : ' ig-preview-shimmer-animate'}`}
            aria-hidden
          />
        ) : null}
        {revealing ? (
          <div
            ref={revealRootRef}
            className={`ig-preview-reveal${reduceMotion ? '' : ' ig-preview-reveal-animate'}`}
            aria-hidden
          >
            {Array.from({ length: REVEAL_STRIPS }, (_, i) => <span key={i} />)}
          </div>
        ) : null}
        {canInteract ? (
          <div className="pointer-events-none absolute top-2 right-2 flex gap-1 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 transition-opacity">
            <CornerIconBtn icon="expand" tip="查看大图" onClick={() => onView?.()} />
            {/* 标注角标：见 image-generation-annotation.ts；恢复开关后取消下一行条件即可。 */}
            {IMAGE_GENERATION_ANNOTATION_UI ? (
              <CornerIconBtn icon="pencil" tip="标注" onClick={() => onAnnotate?.()} />
            ) : null}
            <CornerIconBtn icon="folder" tip="在文件夹中显示" onClick={() => { void actions.reveal(path).catch(() => {}) }} />
            <CornerIconBtn icon="external-link" tip="用系统程序打开" onClick={() => { void actions.external(path).catch(() => {}) }} />
          </div>
        ) : null}
      </div>
    )
  } else if (running || resource.status === 'loading' || !path) {
    body = (
      <div
        className={`ig-preview-shimmer rounded-[12px] border border-line2 mx-auto${reduceMotion ? '' : ' ig-preview-shimmer-animate'}`}
        style={{ width: display.width, height: display.height }}
        aria-hidden
      />
    )
  }

  return (
    <div ref={rootRef} className="mt-4" data-generated-image-preview="">
      <div className="flex items-center gap-2 min-h-7 px-0.5">
        <button
          type="button"
          id={titleId}
          className="inline-flex items-center gap-1 text-[12px] text-muted hover:text-ink"
          aria-expanded={!collapsed}
          onClick={toggleCollapsed}
        >
          <Icon name="chevron-down" size={12} className={collapsed ? '-rotate-90 transition' : 'transition'} />
          {title}
        </button>
      </div>
      {!collapsed ? <div className="mt-3" aria-labelledby={titleId}>{body}</div> : null}
    </div>
  )
}

function CornerIconBtn({ icon, tip, onClick }: { icon: string; tip: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className="ig-preview-corner-btn pointer-events-auto relative inline-flex items-center justify-center w-7 h-7 rounded-md bg-surface/95 border border-line2 text-ink2 hover:text-primaryink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
      aria-label={tip}
      onClick={(e) => { e.stopPropagation(); onClick() }}
    >
      <Icon name={icon} size={14} />
      <span className="ig-preview-corner-tip" role="tooltip">{tip}</span>
    </button>
  )
}
