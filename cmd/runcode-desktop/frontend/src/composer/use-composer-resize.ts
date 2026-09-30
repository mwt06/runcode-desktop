import { useLayoutEffect, useRef, useState, type KeyboardEvent, type PointerEvent, type RefObject } from 'react'
import { usePersistentNumber } from '@/hooks/use-persistent-state'
import { isComposingKey } from '@/ui/keys'
import { clampComposerHeight, composerHeightBounds, DEFAULT_COMPOSER_HEIGHT, draggedComposerHeight, parseComposerHeight } from './resize'

export function useComposerResize(textarea: RefObject<HTMLTextAreaElement>, chat: RefObject<HTMLDivElement>) {
  const footerRef = useRef<HTMLElement>(null)
  const [wanted, setWanted, commit] = usePersistentNumber('composer.height', parseComposerHeight)
  const [bounds, setBounds] = useState(() => composerHeightBounds(window.innerHeight, window.innerHeight))
  const latestBounds = useRef(bounds)
  const frame = useRef<number | null>(null)
  const drag = useRef<{
    pointerId: number; target: HTMLElement; startY: number; startHeight: number; startWanted: number; next: number; moved: boolean
  } | null>(null)
  const height = clampComposerHeight(wanted, bounds)

  useLayoutEffect(() => {
    const input = textarea.current
    const scroll = chat.current
    const footer = footerRef.current
    const parent = footer?.parentElement
    if (!input || !scroll || !footer || !parent) return
    let measuring: number | null = null
    const measure = () => {
      measuring = null
      // 输入区已超出主栏时不能把溢出的部分再算成可用空间（例如恢复了很高的偏好）。
      const overflow = Math.max(0, footer.getBoundingClientRect().bottom - parent.getBoundingClientRect().bottom)
      const available = input.getBoundingClientRect().height + scroll.getBoundingClientRect().height - overflow
      const next = composerHeightBounds(window.innerHeight, available)
      latestBounds.current = next
      setBounds((prev) => prev.max === next.max ? prev : next)
    }
    const schedule = () => { if (measuring === null) measuring = requestAnimationFrame(measure) }
    measure()
    const observer = new ResizeObserver(schedule)
    for (const node of [input, scroll, footer, parent]) observer.observe(node)
    window.addEventListener('resize', schedule)
    return () => {
      observer.disconnect()
      window.removeEventListener('resize', schedule)
      if (measuring !== null) cancelAnimationFrame(measuring)
      if (frame.current !== null) cancelAnimationFrame(frame.current)
      frame.current = null
      const active = drag.current
      drag.current = null
      if (active?.target.hasPointerCapture(active.pointerId)) active.target.releasePointerCapture(active.pointerId)
    }
  }, [textarea, chat])

  function flushFrame() {
    if (frame.current !== null) cancelAnimationFrame(frame.current)
    frame.current = null
  }

  function save(next: number) {
    setWanted(next)
    commit(next)
  }

  function endDrag(e: PointerEvent<HTMLElement>, cancelled: boolean) {
    const active = drag.current
    if (!active || active.pointerId !== e.pointerId) return
    flushFrame()
    drag.current = null
    if (cancelled) setWanted(active.startWanted)
    else if (active.moved || e.clientY !== active.startY) {
      save(draggedComposerHeight(active.startHeight, active.startY, e.clientY, latestBounds.current))
    }
    if (active.target.hasPointerCapture(active.pointerId)) active.target.releasePointerCapture(active.pointerId)
  }

  const handleProps = {
    onPointerDown: (e: PointerEvent<HTMLElement>) => {
      if (!e.isPrimary || e.button !== 0 || drag.current) return
      e.preventDefault()
      e.currentTarget.setPointerCapture(e.pointerId)
      drag.current = {
        pointerId: e.pointerId, target: e.currentTarget, startY: e.clientY,
        startHeight: height, startWanted: wanted, next: height, moved: false,
      }
    },
    onPointerMove: (e: PointerEvent<HTMLElement>) => {
      const active = drag.current
      if (!active || active.pointerId !== e.pointerId) return
      active.moved ||= e.clientY !== active.startY
      if (!active.moved) return
      active.next = draggedComposerHeight(active.startHeight, active.startY, e.clientY, latestBounds.current)
      if (frame.current === null) frame.current = requestAnimationFrame(() => {
        frame.current = null
        if (drag.current) setWanted(drag.current.next)
      })
    },
    onPointerUp: (e: PointerEvent<HTMLElement>) => endDrag(e, false),
    onPointerCancel: (e: PointerEvent<HTMLElement>) => endDrag(e, true),
    onLostPointerCapture: (e: PointerEvent<HTMLElement>) => endDrag(e, true),
    onDoubleClick: () => save(DEFAULT_COMPOSER_HEIGHT),
    onKeyDown: (e: KeyboardEvent<HTMLElement>) => {
      if (isComposingKey(e) || drag.current) return
      let next: number
      switch (e.key) {
        case 'ArrowUp': next = height + 16; break
        case 'ArrowDown': next = height - 16; break
        case 'Home': next = bounds.min; break
        case 'End': next = bounds.max; break
        default: return
      }
      e.preventDefault()
      save(clampComposerHeight(next, latestBounds.current))
    },
  }
  return { footerRef, height, bounds, handleProps }
}
