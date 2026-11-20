import { useEffect, useId, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Icon } from '@/ui/icons'

export type ImageLightboxProps = {
  open: boolean
  src: string
  alt: string
  /** 矢量图填满可用区域；位图放大不超过原图两倍。 */
  mode: 'vector' | 'raster'
  onClose: () => void
}

const CLICK_SHIELD_MS = 250

export function ImageLightbox({ open, src, alt, mode, onClose }: ImageLightboxProps) {
  const titleId = useId()
  const closeRef = useRef<HTMLButtonElement>(null)
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose
  const lastSrc = useRef(src)
  const [natural, setNatural] = useState({ width: 0, height: 0 })
  const shieldTimer = useRef<number | null>(null)
  const shieldListener = useRef<((event: MouseEvent) => void) | null>(null)

  // 来源在流式更新或会话 generation 变化时可能失效；先关闭遮罩，避免继续展示旧图。
  useEffect(() => {
    if (!open) {
      lastSrc.current = src
      return
    }
    if (lastSrc.current !== src) {
      lastSrc.current = src
      onCloseRef.current()
    }
  }, [open, src])

  useEffect(() => {
    if (!open) return
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const appRoot = document.getElementById('root')
    const wasInert = appRoot?.hasAttribute('inert') ?? false
    appRoot?.setAttribute('inert', '')
    const focus = window.requestAnimationFrame(() => closeRef.current?.focus())
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onCloseRef.current()
      } else if (event.key === 'Tab') {
        event.preventDefault()
        closeRef.current?.focus()
      }
    }
    document.addEventListener('keydown', onKeyDown)
    return () => {
      window.cancelAnimationFrame(focus)
      document.removeEventListener('keydown', onKeyDown)
      if (!wasInert) appRoot?.removeAttribute('inert')
      previous?.focus()
    }
  }, [open])

  useEffect(() => () => clearClickShield(), [])

  function clearClickShield() {
    if (shieldTimer.current != null) window.clearTimeout(shieldTimer.current)
    if (shieldListener.current) window.removeEventListener('click', shieldListener.current, true)
    shieldTimer.current = null
    shieldListener.current = null
  }

  function closeFromImage() {
    clearClickShield()
    const until = performance.now() + CLICK_SHIELD_MS
    const swallow = (event: MouseEvent) => {
      if (performance.now() < until) {
        event.preventDefault()
        event.stopPropagation()
        return
      }
      clearClickShield()
    }
    shieldListener.current = swallow
    window.addEventListener('click', swallow, true)
    shieldTimer.current = window.setTimeout(clearClickShield, CLICK_SHIELD_MS + 20)
    onCloseRef.current()
  }

  if (!open) return null

  const imageStyle = mode === 'vector'
    ? { width: '90vw', height: '90vh', objectFit: 'contain' as const }
    : natural.width > 0 && natural.height > 0
      ? {
          width: `min(90vw, ${natural.width * 2}px)`,
          height: `min(90vh, ${natural.height * 2}px)`,
          objectFit: 'contain' as const,
        }
      : { width: '90vw', height: '90vh', objectFit: 'contain' as const }

  return createPortal(
    <div
      className="fixed inset-0 z-[15] p-6 bg-ink/70 backdrop-blur-[2px] flex items-center justify-center anim-rise"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      data-image-lightbox={mode}
    >
      <h2 id={titleId} className="sr-only">图片全屏预览</h2>
      <button
        ref={closeRef}
        type="button"
        className="absolute top-4 right-4 z-10 flex-none w-9 h-9 flex items-center justify-center rounded-full bg-surface/95 text-ink shadow-card hover:bg-surface"
        aria-label="关闭全屏预览"
        title="关闭"
        onClick={closeFromImage}
      >
        <Icon name="win-close" size={16} />
      </button>
      <button
        type="button"
        className="max-w-full max-h-full flex items-center justify-center cursor-zoom-out bg-transparent border-0 p-0"
        aria-label="关闭全屏预览"
        onClick={closeFromImage}
      >
        <img
          src={src}
          alt={alt}
          width={natural.width || undefined}
          height={natural.height || undefined}
          style={imageStyle}
          className="block max-w-full max-h-full"
          draggable={false}
          onLoad={(event) => setNatural({
            width: event.currentTarget.naturalWidth,
            height: event.currentTarget.naturalHeight,
          })}
        />
      </button>
    </div>,
    document.body,
  )
}
