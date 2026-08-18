// Popover is the shared click-away dropdown: a full-screen transparent overlay
// (click closes it, and the event is stopped so dismissing never also triggers
// whatever sits underneath) plus a panel absolutely positioned against the
// nearest `relative` ancestor — the trigger's wrapper. `menu` is the compact
// item-list look; `panel` is the search-picker look (column flex, deeper
// shadow, taller radius). className adds per-site sizing (width/max-height).
import { type ReactNode } from 'react'

export type PopoverPlacement = 'up-left' | 'up-right' | 'up-full' | 'down-left' | 'down-right' | 'down-full'

const POPOVER_POS: Record<PopoverPlacement, string> = {
  'up-left': 'bottom-full left-0 mb-1.5',
  'up-right': 'bottom-full right-0 mb-1.5',
  'up-full': 'bottom-full left-0 right-0 mb-1.5',
  'down-left': 'top-full left-0 mt-1.5',
  'down-right': 'top-full right-0 mt-1.5',
  'down-full': 'top-full left-0 right-0 mt-1.5',
}

export function Popover({ open, onClose, placement, variant = 'menu', className, children }: {
  open: boolean
  onClose: () => void
  placement: PopoverPlacement
  variant?: 'menu' | 'panel'
  className?: string
  children: ReactNode
}) {
  if (!open) return null
  const look =
    variant === 'panel'
      ? 'rounded-[13px] shadow-[0_18px_50px_rgba(30,35,60,0.22)] flex flex-col'
      : 'rounded-btn shadow-card py-1'
  return (
    <>
      <div className="fixed inset-0 z-10" onClick={(e) => { e.stopPropagation(); onClose() }} />
      <div className={`absolute ${POPOVER_POS[placement]} z-20 bg-surface border border-line2 overflow-hidden ${look} ${className ?? ''}`} onClick={(e) => e.stopPropagation()}>
        {children}
      </div>
    </>
  )
}
