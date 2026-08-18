// The shared model picker: one searchable dropdown behind the in-chat model
// switcher, the settings session/judge pickers and the per-agent model choice —
// so "pick a model" looks and filters the same everywhere.
import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { Popover, type PopoverPlacement } from './popover'
import { FIELD_CLS } from './fields'
import { customModelOptionSub } from '@/core/custom-models'
import { type CustomModel, type PassportModel } from '@/core/bridge'

// ModelOption is one row in the shared model picker. `id` is what picking it
// submits (a platform model id, or a custom connection's name/model id depending
// on the caller); `modelId` is the session-model id it maps to when that differs
// (custom connections), used only to mark the current selection.
export type ModelOption = { id: string; label: string; sub?: string; kind: 'platform' | 'custom'; modelId?: string }

// toModelOptions merges the platform (passport) and local custom models into the one
// option list every model switcher shows — the composer's in-chat picker and the
// Settings connection field alike. Platform ids come first, then custom profiles
// (tagged 自定义). `id` is exactly what SwitchModel receives: a platform model id, or
// a custom profile's name; `modelId` carries the custom profile's underlying model id
// so the current live model (which reports the id, not the profile name) still marks.
export function toModelOptions(platform: PassportModel[], custom: CustomModel[]): ModelOption[] {
  return [
    ...platform.map((m): ModelOption => ({ kind: 'platform', id: m.id, label: m.id, sub: m.ownedBy })),
    ...custom.map((c): ModelOption => ({ kind: 'custom', id: c.name, label: c.name, sub: customModelOptionSub(c), modelId: c.model })),
  ]
}

// ModelPickerPopover is the dropdown itself: a search box over platform + custom
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
          className="w-full font-sans text-[13px] bg-surface2 text-ink border border-line2 rounded-field px-3 py-2 outline-none focus:border-primary"
        />
      </div>
      <div className="overflow-y-auto py-1">
        {clearLabel && (
          <button type="button" onClick={() => choose('')} className={`w-full text-left px-3.5 py-2 text-[13px] hover:bg-surface2 ${current ? 'text-muted' : 'text-primary'}`}>
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
              <span className="font-mono text-[13px] truncate flex-1">{o.label}</span>
              {o.kind === 'custom' && <span className="text-[10px] leading-none px-1.5 py-0.5 rounded-full bg-primarysoft text-primaryink flex-none">自定义</span>}
              {o.sub && o.sub !== o.label && <span className="text-[11px] text-faint flex-none truncate max-w-[110px]">{o.sub}</span>}
              {cur && <span className="text-primary text-[13px] flex-none">✓</span>}
            </button>
          )
        })}
        {showCustom && (
          <button type="button" onClick={() => choose(typed)} className="w-full text-left px-3.5 py-2 hover:bg-surface2 text-ink">
            <span className="text-[13px]">使用自定义：<span className="font-mono">{typed}</span></span>
          </button>
        )}
        {matches.length === 0 && !showCustom && (
          <div className="px-3.5 py-6 text-center text-[13px] text-muted">{options.length === 0 ? '无可选模型(登录通行证或在设置中添加自定义模型)' : '没有匹配的模型'}</div>
        )}
      </div>
    </Popover>
  )
}

// ModelSelect is the field form of the picker: a read-only trigger showing the
// current value, opening a ModelPickerPopover beneath it.
export function ModelSelect({ value, options, onPick, placeholder, allowCustom, clearLabel, disabled }: {
  value: string
  options: ModelOption[]
  // The picked option is forwarded so callers that need its kind (e.g. a live
  // connection switch: platform vs custom) can read it; string-only callers ignore it.
  onPick: (id: string, option?: ModelOption) => void
  placeholder: string
  allowCustom?: boolean
  clearLabel?: string
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        className={`${FIELD_CLS} w-full flex items-center text-left ${disabled ? '' : 'cursor-pointer hover:border-primary/60'} transition-colors`}
      >
        <span className={`flex-1 truncate ${value ? 'font-mono' : 'text-faint'}`}>{value || placeholder}</span>
        <Icon name="chevron-down" size={16} className="text-faint flex-none ml-2" />
      </button>
      <ModelPickerPopover
        open={open}
        onClose={() => setOpen(false)}
        placement="down-full"
        className="max-h-[320px]"
        options={options}
        current={value}
        allowCustom={allowCustom}
        clearLabel={clearLabel}
        onPick={onPick}
      />
    </div>
  )
}
