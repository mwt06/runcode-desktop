import { useState, type ReactNode } from 'react'
import { Icon } from './icons'

// CollapsibleGroup renders a list that collapses to a single summary row once it
// exceeds `threshold` items (default 2) — a long run of edit/preview cards would
// otherwise swamp the conversation. The summary row matches the tool-execution row
// style (icon + label · count, chevron pushed to the right). `expanded` starts null =
// follow the default, so a group also auto-collapses if its count grows past the
// threshold mid-turn; once the user toggles, their choice sticks. `extra` is optional
// right-aligned content in the summary row (e.g. an aggregate +N −N).
export function CollapsibleGroup({ icon, label, count, threshold = 2, extra, children }: {
  icon: string
  label: string
  count: number
  threshold?: number
  extra?: ReactNode
  children: ReactNode
}) {
  const [expanded, setExpanded] = useState<boolean | null>(null)
  const collapsible = count > threshold
  const isExpanded = expanded ?? !collapsible
  return (
    <div className="flex flex-col gap-1.5 mt-1.5">
      {collapsible && (
        <div
          onClick={() => setExpanded(!isExpanded)}
          className="flex items-center gap-2.5 px-2 py-1.5 rounded-lg cursor-pointer select-none hover:bg-surface2"
        >
          <span className="flex-none text-faint"><Icon name={icon} size={15} /></span>
          <span className="flex-1 min-w-0 truncate text-[14px] text-ink2">
            {label} <span className="font-mono text-faint">· {count} 个</span>
          </span>
          {extra}
          <Icon name="chevron-down" size={13} className={`flex-none text-faint transition ${isExpanded ? 'rotate-180' : ''}`} />
        </div>
      )}
      {isExpanded && children}
    </div>
  )
}
