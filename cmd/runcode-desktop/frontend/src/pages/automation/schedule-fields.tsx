import type { AutomationSchedule } from '@/core/bridge'
import { FIELD_CLS, LABEL_CLS, SelectField } from '@/ui/fields'
import { SCHEDULE_MODES, TIME_ZONES } from './draft'

const WEEKDAYS = [
  { day: 1, label: '一' },
  { day: 2, label: '二' },
  { day: 3, label: '三' },
  { day: 4, label: '四' },
  { day: 5, label: '五' },
  { day: 6, label: '六' },
  { day: 7, label: '日' },
]

export function ScheduleFields({ value, onChange, disabled = false }: {
  value: AutomationSchedule
  onChange: (next: AutomationSchedule) => void
  disabled?: boolean
}) {
  const mode = SCHEDULE_MODES.find((item) => item.value === value.mode)
  const zones = TIME_ZONES.includes(value.timeZone as (typeof TIME_ZONES)[number]) || !value.timeZone
    ? [...TIME_ZONES]
    : [value.timeZone, ...TIME_ZONES]

  function setMode(nextMode: string) {
    const next: AutomationSchedule = { ...value, mode: nextMode }
    if (nextMode === 'once') delete next.interval
    else if (!next.interval) next.interval = 1
    onChange(next)
  }

  function toggleWeekday(day: number) {
    const current = new Set(value.weekdays ?? [])
    if (current.has(day)) current.delete(day)
    else current.add(day)
    onChange({ ...value, weekdays: [...current].sort((a, b) => a - b) })
  }

  return (
    <fieldset disabled={disabled} className="flex flex-col gap-3 border-0 m-0 p-0 min-w-0">
      <label className={LABEL_CLS}>
        重复
        <SelectField aria-label="重复" value={value.mode} onChange={setMode}>
          {SCHEDULE_MODES.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
        </SelectField>
      </label>
      {value.mode !== 'once' && mode?.unit && (
        <label className={LABEL_CLS}>
          {`重复间隔（${mode.unit}）`}
          <input
            aria-label="重复间隔"
            type="number"
            min={1}
            max={9999}
            step={1}
            value={value.interval ?? 1}
            onChange={(e) => onChange({ ...value, interval: e.target.value === '' ? undefined : Number(e.target.value) })}
            className={`${FIELD_CLS} w-28`}
          />
        </label>
      )}
      {value.mode === 'weekly' && (
        <label className={LABEL_CLS}>
          星期
          <span className="flex flex-wrap gap-1.5">
            {WEEKDAYS.map((item) => {
              const on = (value.weekdays ?? []).includes(item.day)
              return (
                <button
                  key={item.day}
                  type="button"
                  aria-pressed={on}
                  onClick={() => toggleWeekday(item.day)}
                  className={`w-8 h-8 rounded-full text-[13px] border ${on ? 'bg-primary text-white border-primary' : 'bg-surface text-muted border-line2'}`}
                >
                  {item.label}
                </button>
              )
            })}
          </span>
        </label>
      )}
      {value.mode === 'monthly' && (
        <label className={LABEL_CLS}>
          每月几号
          <input
            aria-label="每月几号"
            type="number"
            min={1}
            max={31}
            step={1}
            value={value.monthDay ?? 1}
            onChange={(e) => onChange({ ...value, monthDay: Number(e.target.value) })}
            className={`${FIELD_CLS} w-28`}
          />
          <span className="text-[12px] text-amberink">无此日期的月份跳过</span>
        </label>
      )}
      <label className={LABEL_CLS}>
        日期
        <input
          aria-label="日期"
          type="date"
          value={value.startDate}
          onChange={(e) => onChange({ ...value, startDate: e.target.value })}
          className={`${FIELD_CLS} w-full`}
        />
      </label>
      <label className={LABEL_CLS}>
        时间
        <input
          aria-label="时间"
          type="time"
          step={1}
          value={value.timeOfDay}
          onChange={(e) => onChange({ ...value, timeOfDay: e.target.value.length === 5 ? `${e.target.value}:00` : e.target.value })}
          className={`${FIELD_CLS} w-full`}
        />
      </label>
      <label className={LABEL_CLS}>
        时区
        <SelectField aria-label="时区" value={value.timeZone} onChange={(zone) => onChange({ ...value, timeZone: zone })}>
          {zones.map((zone) => <option key={zone} value={zone}>{zone}</option>)}
        </SelectField>
      </label>
    </fieldset>
  )
}
