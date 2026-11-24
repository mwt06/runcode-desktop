// RangePicker 是用量页的时间范围选择：快捷项点了就应用；自定义在弹层里编辑草稿，
// 「确定」才提交，「取消」/Esc/点外面都不改变已应用的范围，并把焦点还给触发按钮。
import { useRef, useState, type KeyboardEvent } from 'react'
import { Popover } from '@/ui/popover'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { FIELD_CLS } from '@/ui/fields'
import { InlineError } from '@/ui/feedback'
import { isComposingKey } from '@/ui/keys'
import {
  USAGE_RANGES, customDraftToSelection, rangeLabel, shanghaiDayStart, toShanghaiInput,
  type CustomDraft, type UsageRangeSelection,
} from '@/core/usage'

const CHIP = 'px-3 py-1.5 rounded-field text-[13px] border transition cursor-pointer disabled:opacity-40 disabled:cursor-default'
const chipCls = (active: boolean) =>
  `${CHIP} ${active ? 'bg-primarysoft border-primary/40 text-primaryink font-medium' : 'bg-surface border-line2 text-muted hover:text-ink hover:border-primary/40'}`

function initialDraft(value: UsageRangeSelection, now: Date): CustomDraft {
  if (value.kind === 'custom' && value.startAt) {
    return {
      start: toShanghaiInput(new Date(value.startAt)),
      end: value.endAt ? toShanghaiInput(new Date(value.endAt)) : toShanghaiInput(now),
      followNow: !!value.followNow,
    }
  }
  return { start: toShanghaiInput(shanghaiDayStart(now)), end: toShanghaiInput(now), followNow: false }
}

export function RangePicker({ value, onApply, now = () => new Date(), disabled }: {
  value: UsageRangeSelection
  onApply: (selection: UsageRangeSelection) => void
  now?: () => Date
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<CustomDraft>({ start: '', end: '', followNow: false })
  const trigger = useRef<HTMLButtonElement>(null)
  const checked = customDraftToSelection(draft, now())

  const close = () => {
    setOpen(false)
    trigger.current?.focus()
  }
  const apply = () => {
    if (!checked.selection) return
    onApply(checked.selection)
    close()
  }
  const onKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    if (isComposingKey(e)) return
    if (e.key === 'Escape') { e.preventDefault(); close() }
    else if (e.key === 'Enter') { e.preventDefault(); apply() }
  }

  return (
    <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="时间范围">
      {USAGE_RANGES.map((r) => (
        <button
          key={r.kind}
          type="button"
          disabled={disabled}
          aria-pressed={value.kind === r.kind}
          onClick={() => onApply({ kind: r.kind })}
          className={chipCls(value.kind === r.kind)}
        >
          {r.label}
        </button>
      ))}
      <div className="relative">
        <button
          ref={trigger}
          type="button"
          disabled={disabled}
          aria-pressed={value.kind === 'custom'}
          aria-haspopup="dialog"
          aria-expanded={open}
          onClick={() => { setDraft(initialDraft(value, now())); setOpen((o) => !o) }}
          className={chipCls(value.kind === 'custom')}
        >
          {value.kind === 'custom' ? rangeLabel(value) : '自定义…'}
        </button>
        <Popover open={open} onClose={close} placement="down-right" variant="panel" className="w-[300px]">
          <div role="dialog" aria-label="自定义时间范围" className="p-3.5 flex flex-col gap-3" onKeyDown={onKeyDown}>
            <label className="flex flex-col gap-1 text-[12px] text-muted">
              开始（上海时间）
              <input
                autoFocus
                type="datetime-local"
                aria-label="开始时间"
                value={draft.start}
                onChange={(e) => setDraft((d) => ({ ...d, start: e.target.value }))}
                className={`${FIELD_CLS} text-[13px] py-2`}
              />
            </label>
            <label className="flex flex-col gap-1 text-[12px] text-muted">
              结束（上海时间，不含）
              <input
                type="datetime-local"
                aria-label="结束时间"
                value={draft.end}
                disabled={draft.followNow}
                onChange={(e) => setDraft((d) => ({ ...d, end: e.target.value }))}
                className={`${FIELD_CLS} text-[13px] py-2`}
              />
            </label>
            <label className="flex items-center gap-2 text-[13px] text-ink cursor-pointer">
              <input
                type="checkbox"
                checked={draft.followNow}
                onChange={(e) => setDraft((d) => ({ ...d, followNow: e.target.checked }))}
              />
              结束时间跟随当前时刻
            </label>
            {checked.error && <InlineError variant="text">{checked.error}</InlineError>}
            <div className="flex justify-end gap-2">
              <button type="button" className={`${BTN} py-1.5 text-[13px]`} onClick={close}>取消</button>
              <button type="button" className={`${BTN} ${BTN_PRIMARY} py-1.5 text-[13px]`} disabled={!checked.selection} onClick={apply}>确定</button>
            </div>
          </div>
        </Popover>
      </div>
    </div>
  )
}
