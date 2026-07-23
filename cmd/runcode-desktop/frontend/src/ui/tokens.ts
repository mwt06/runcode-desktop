// Shared UI class strings and style constants used across the app shell and its pages.
import { type CSSProperties } from 'react'

export const BTN =
  'font-[inherit] border border-line2 bg-surface text-ink rounded-[10px] px-4 py-[9px] cursor-pointer transition hover:border-primary hover:text-primaryink disabled:opacity-40 disabled:cursor-default'
// Override the base bg/border with !important — same-specificity utilities don't
// reliably win by class-string order, so an un-flagged bg-primary lost to bg-surface
// and the primary button rendered white-on-white.
export const BTN_PRIMARY = '!bg-primary !text-white !border-primary font-semibold hover:brightness-105'
export const BTN_DANGER = '!text-red !border-[rgba(224,86,74,0.4)] hover:!text-red'

// The OS title bar is hidden (Frameless), so a region must be marked draggable
// and the window controls provided in-app. DRAG marks a drag region; NO_DRAG opts
// interactive children out of dragging.
export const DRAG = { ['--wails-draggable' as string]: 'drag' } as CSSProperties
export const NO_DRAG = { ['--wails-draggable' as string]: 'no-drag' } as CSSProperties
