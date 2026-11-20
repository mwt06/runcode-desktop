// 栅格生图模态外壳：portal 到 #image-modal-root，锁背景滚动／wheel，焦点循环。
import { useEffect, useRef, type HTMLAttributes, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

const Z_IMAGE = 'var(--z-image-modal, 60)'
const Z_CONFIRM = 'var(--z-image-confirm, 61)'

function ensureModalRoot(): HTMLElement {
  let el = document.getElementById('image-modal-root')
  if (!el) {
    el = document.createElement('div')
    el.id = 'image-modal-root'
    document.body.appendChild(el)
  }
  return el
}

export function GeneratedImageModalSurface({
  open, suspended, confirmOpen, onRequestClose, children, confirm,
}: {
  open: boolean
  suspended: boolean
  confirmOpen: boolean
  onRequestClose: () => void
  children: ReactNode
  confirm?: ReactNode
}) {
  const panelRef = useRef<HTMLDivElement>(null)
  const scrollLock = useRef<{ el: HTMLElement; top: number; overflow: string } | null>(null)

  useEffect(() => {
    if (!open || suspended) return
    const chat = document.querySelector<HTMLElement>('[data-chat-scroll]')
      ?? document.querySelector<HTMLElement>('.flex-1.overflow-y-auto')
    const el = chat ?? document.documentElement
    scrollLock.current = { el, top: el.scrollTop, overflow: el.style.overflow }
    el.style.overflow = 'hidden'
    const bg = document.getElementById('app-background')
    bg?.setAttribute('inert', '')
    const onWheel = (e: WheelEvent) => { e.preventDefault() }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        onRequestClose()
      } else if (e.key === 'Tab' && panelRef.current) {
        const nodes = panelRef.current.querySelectorAll<HTMLElement>(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
        )
        const list = Array.from(nodes).filter((n) => !n.hasAttribute('disabled') && n.offsetParent !== null)
        if (list.length === 0) return
        const first = list[0]!
        const last = list[list.length - 1]!
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault()
          last.focus()
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault()
          first.focus()
        }
      } else if (['PageDown', 'PageUp', ' '].includes(e.key)) {
        const t = e.target as HTMLElement | null
        if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
        e.preventDefault()
      }
    }
    window.addEventListener('wheel', onWheel, { passive: false, capture: true })
    window.addEventListener('keydown', onKey, true)
    const focus = window.requestAnimationFrame(() => {
      panelRef.current?.querySelector<HTMLElement>('button, [href], input')?.focus()
    })
    return () => {
      window.cancelAnimationFrame(focus)
      window.removeEventListener('wheel', onWheel, true)
      window.removeEventListener('keydown', onKey, true)
      bg?.removeAttribute('inert')
      const lock = scrollLock.current
      if (lock) {
        lock.el.style.overflow = lock.overflow
        lock.el.scrollTop = lock.top
        scrollLock.current = null
      }
    }
  }, [open, suspended, onRequestClose])

  if (!open) return null
  const root = ensureModalRoot()

  return createPortal(
    <>
      <div
        className="fixed inset-0"
        style={{
          zIndex: Z_IMAGE,
          display: suspended ? 'none' : 'flex',
          background: 'rgba(30,33,50,0.45)',
          backdropFilter: 'blur(2px)',
          alignItems: 'center',
          justifyContent: 'center',
          padding: 16,
        }}
        data-generated-image-modal=""
        aria-hidden={suspended || undefined}
        onMouseDown={(e) => {
          if (e.target === e.currentTarget) onRequestClose()
        }}
      >
        <div
          ref={panelRef}
          role="dialog"
          aria-modal="true"
          aria-label="生成图片查看"
          className="w-[min(960px,96vw)] max-h-[92vh] bg-surface rounded-2xl shadow-modal flex flex-col overflow-hidden"
          {...(suspended ? { inert: '' } as HTMLAttributes<HTMLDivElement> : {})}
        >
          {children}
        </div>
      </div>
      {confirmOpen && !suspended ? (
        <div className="fixed inset-0 flex items-center justify-center" style={{ zIndex: Z_CONFIRM }}>
          {confirm}
        </div>
      ) : null}
    </>,
    root,
  )
}
