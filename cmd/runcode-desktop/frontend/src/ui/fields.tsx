// Form-field primitives: the one shared control look plus the dressed-up native
// <select>. Every input across the settings / start / manager pages references
// these so the controls line up pixel-for-pixel wherever they appear.
import { type ReactNode } from 'react'
import { Icon } from './icons'

// FIELD_CLS is the one shared input/select look (surface2 bg, line2 border, 9px
// radius, 14px, primary focus ring). SelectField / ModelSelect reference this so
// every control lines up pixel-for-pixel with the plain <input> fields.
export const FIELD_CLS = 'font-sans text-[14px] bg-surface2 text-ink border border-line2 rounded-[9px] px-3 py-2.5 outline-none focus:border-primary focus:shadow-[0_0_0_3px_var(--color-primarysoft)] disabled:opacity-60'

// LABEL_CLS is the matching form-field wrapper: a stacked muted caption above
// its control (used with <label>/<div> around a FIELD_CLS input).
export const LABEL_CLS = 'flex flex-col gap-1.5 text-[12.5px] text-muted'

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
