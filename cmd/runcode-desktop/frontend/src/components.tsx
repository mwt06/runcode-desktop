// Shared UI components used across the app shell and its pages. Keeping these in one
// place (rather than re-declaring near each consumer) so inputs, dropdowns, confirm
// modals and collapsible groups all render identically wherever they appear.
import { useEffect, useState, type ReactNode } from 'react'
import { Icon } from './icons'
import { BTN, BTN_DANGER } from './ui'

// FIELD_CLS is the one shared input/select look (surface2 bg, line2 border, 9px
// radius, 14px, primary focus ring). SelectField / ModelSelect reference this so
// every control lines up pixel-for-pixel with the plain <input> fields.
export const FIELD_CLS = 'font-sans text-[14px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2.5 outline-none focus:border-primary focus:shadow-[0_0_0_3px_var(--color-primarysoft)] disabled:opacity-60'

// LABEL_CLS is the matching form-field wrapper: a stacked muted caption above
// its control (used with <label>/<div> around a FIELD_CLS input).
export const LABEL_CLS = 'flex flex-col gap-1.5 text-[12.5px] text-muted'

// Popover is the shared click-away dropdown: a full-screen transparent overlay
// (click closes it, and the event is stopped so dismissing never also triggers
// whatever sits underneath) plus a panel absolutely positioned against the
// nearest `relative` ancestor — the trigger's wrapper. `menu` is the compact
// item-list look; `panel` is the search-picker look (column flex, deeper
// shadow, taller radius). className adds per-site sizing (width/max-height).
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
      : 'rounded-[11px] shadow-card py-1'
  return (
    <>
      <div className="fixed inset-0 z-10" onClick={(e) => { e.stopPropagation(); onClose() }} />
      <div className={`absolute ${POPOVER_POS[placement]} z-20 bg-surface border border-line2 overflow-hidden ${look} ${className ?? ''}`} onClick={(e) => e.stopPropagation()}>
        {children}
      </div>
    </>
  )
}

// ModelOption is one row in the shared model picker. `id` is what picking it
// submits (a platform model id, or a custom connection's name/model id depending
// on the caller); `modelId` is the session-model id it maps to when that differs
// (custom connections), used only to mark the current selection.
export type ModelOption = { id: string; label: string; sub?: string; kind: 'platform' | 'custom'; modelId?: string }

// ModelPickerPopover is the shared searchable model dropdown behind the in-chat
// model switcher and the settings pickers: a search box over platform + custom
// options with the standard row look (mono label, 自定义 pill, current ✓). It
// owns the query (reset on every open) and closes itself after any pick.
// `allowCustom` offers the typed text verbatim when it matches no option;
// `clearLabel` prepends a row that picks '' (e.g. 留空 = 默认).
export function ModelPickerPopover({ open, onClose, placement, className, options, current, limit = 12, allowCustom, clearLabel, onPick }: {
  open: boolean
  onClose: () => void
  placement: PopoverPlacement
  className?: string
  options: ModelOption[]
  current?: string
  limit?: number
  allowCustom?: boolean
  clearLabel?: string
  onPick: (id: string, option?: ModelOption) => void
}) {
  const [query, setQuery] = useState('')
  useEffect(() => {
    if (open) setQuery('')
  }, [open])
  const q = query.trim().toLowerCase()
  const matches = options
    .filter((o) => !q || o.label.toLowerCase().includes(q) || (o.sub ?? '').toLowerCase().includes(q) || o.id.toLowerCase().includes(q))
    .slice(0, limit)
  const typed = query.trim()
  const showCustom = !!allowCustom && typed !== '' && !options.some((o) => o.id === typed)
  const choose = (id: string, option?: ModelOption) => { onPick(id, option); onClose() }
  return (
    <Popover open={open} onClose={onClose} placement={placement} variant="panel" className={className}>
      <div className="p-2.5 border-b border-line">
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索模型…"
          className="w-full font-sans text-[13px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2 outline-none focus:border-primary"
        />
      </div>
      <div className="overflow-y-auto py-1">
        {clearLabel && (
          <button type="button" onClick={() => choose('')} className={`w-full text-left px-3.5 py-2 text-[12.5px] hover:bg-surface2 ${current ? 'text-muted' : 'text-primary'}`}>
            {clearLabel}{!current && ' ✓'}
          </button>
        )}
        {matches.map((o) => {
          const cur = !!current && (o.modelId ?? o.id) === current
          return (
            <button
              key={`${o.kind}:${o.id}`}
              type="button"
              onClick={() => choose(o.id, o)}
              className={`w-full text-left px-3.5 py-2 flex items-center gap-2 hover:bg-surface2 transition ${cur ? 'text-primary' : 'text-ink'}`}
            >
              <span className="font-mono text-[12.5px] truncate flex-1">{o.label}</span>
              {o.kind === 'custom' && <span className="text-[10px] leading-none px-1.5 py-0.5 rounded-full bg-primarysoft text-primaryink flex-none">自定义</span>}
              {o.sub && o.sub !== o.label && <span className="text-[11px] text-faint flex-none truncate max-w-[110px]">{o.sub}</span>}
              {cur && <span className="text-primary text-[13px] flex-none">✓</span>}
            </button>
          )
        })}
        {showCustom && (
          <button type="button" onClick={() => choose(typed)} className="w-full text-left px-3.5 py-2 hover:bg-surface2 text-ink">
            <span className="text-[12.5px]">使用自定义：<span className="font-mono">{typed}</span></span>
          </button>
        )}
        {matches.length === 0 && !showCustom && (
          <div className="px-3.5 py-6 text-center text-[12.5px] text-muted">{options.length === 0 ? '无可选模型(登录通行证或在设置中添加自定义模型)' : '没有匹配的模型'}</div>
        )}
      </div>
    </Popover>
  )
}

// DiffStat is the shared "+N −N" churn badge: green adds, red deletes (faint when
// nothing was deleted). Size/mono/layout styling comes from the caller via
// className; without one it inherits the surrounding text style.
export function DiffStat({ add, del, className }: { add: number; del: number; className?: string }) {
  return (
    <span className={className}>
      <span className="text-green">+{add}</span> <span className={del > 0 ? 'text-red' : 'text-faint'}>−{del}</span>
    </span>
  )
}

// sourceLabel maps a capability's origin (skill/sub-agent/tool source field) to
// its Chinese label; SourceBadge renders it as the standard tiny outline chip.
export function sourceLabel(source: string): string {
  return source === 'builtin' ? '内置' : source === 'user' ? '用户' : '项目'
}

export function SourceBadge({ source }: { source: string }) {
  return <span className="text-[10px] text-faint border border-line2 rounded px-1.5 py-px flex-none">{sourceLabel(source)}</span>
}

// SelectField is a native <select> dressed to match FIELD_CLS: the browser arrow is
// removed (appearance-none) and one shared chevron is overlaid at the right, so these
// and the custom ModelSelect all read as the same dropdown.
export function SelectField({ value, onChange, children, disabled }: {
  value: string
  onChange: (value: string) => void
  children: ReactNode
  disabled?: boolean
}) {
  return (
    <div className="relative">
      <select
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        className={`${FIELD_CLS} w-full appearance-none pr-9 cursor-pointer`}
      >
        {children}
      </select>
      <Icon name="chevron-down" size={16} className="text-faint pointer-events-none absolute right-3 top-1/2 -translate-y-1/2" />
    </div>
  )
}

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
          <span className="flex-1 min-w-0 truncate text-[13.5px] text-[#3f4653]">
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

// ConfirmDialog is an in-app confirmation modal in the app's own style — replaces
// the browser's native window.confirm(), which renders the ugly "wails.localhost
// 显示" chrome. Backdrop click and 取消 both dismiss; 确认 runs onConfirm.
export function ConfirmDialog({ title, message, confirmLabel, onConfirm, onCancel }: {
  title: string
  message: ReactNode
  confirmLabel: string
  onConfirm: () => void
  onCancel: () => void
}) {
  return (
    <div className="fixed inset-0 bg-[rgba(30,33,50,0.32)] backdrop-blur-[2px] flex items-center justify-center z-30 anim-rise" onClick={onCancel}>
      <div className="w-[400px] max-w-[92vw] bg-surface rounded-[16px] p-[22px] shadow-[0_30px_70px_rgba(30,35,60,0.28)]" onClick={(e) => e.stopPropagation()}>
        <h3 className="m-0 mb-2.5 text-[15.5px] font-bold flex items-center gap-2.5">
          <span className="w-[9px] h-[9px] rounded-[3px] bg-red" />{title}
        </h3>
        <div className="text-[13.5px] text-muted leading-relaxed break-words">{message}</div>
        <div className="mt-5 flex justify-end gap-2.5">
          <button type="button" onClick={onCancel} className={`${BTN} px-5`}>取消</button>
          <button type="button" autoFocus onClick={onConfirm} className={`${BTN} ${BTN_DANGER} px-5`}>{confirmLabel}</button>
        </div>
      </div>
    </div>
  )
}
