import { type ReactNode } from 'react'

// GhostBtn is the borderless toolbar button (composer toolbar, card actions):
// muted until hovered, no chrome of its own.
export function GhostBtn({ children, onClick, title, className, disabled }: { children: ReactNode; onClick?: () => void; title?: string; className?: string; disabled?: boolean }) {
  return (
    <button
      className={`border-none bg-transparent text-muted text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 hover:bg-surface2 hover:text-ink disabled:opacity-40 disabled:cursor-default ${className ?? ''}`}
      disabled={disabled}
      onClick={onClick}
      title={title}
    >
      {children}
    </button>
  )
}
