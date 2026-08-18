// Shared feedback surfaces: system-event dividers in the conversation, block
// banners inside dialogs/forms, and page-level error copy.
//
// These used to be written out at every call site, which is how the app ended up
// with six shapes for "something went wrong" (a bordered banner, an icon banner,
// bare text at three different sizes, a conversation bubble, an inline chip) and
// two different ambers for a warning. One severity scale, defined once, lives here.
import { type ReactNode } from 'react'

// Severity, shared by every surface below. It drives color *and* size: the
// quieter a note is, the smaller it renders, so importance reads at a glance
// without the caller picking type scales by hand.
export type Tone = 'neutral' | 'warning' | 'danger'

const NOTE_TONE: Record<Tone, { line: string; text: string }> = {
  neutral: { line: 'bg-line2', text: 'text-faint text-[12px]' },
  warning: { line: 'bg-amber/45', text: 'text-amberink text-[13px]' },
  danger: { line: 'bg-red/35', text: 'text-red text-[13px]' },
}

// SystemNote is the conversation's system-event row: a centered label with a rule
// running out to both edges. It is deliberately NOT a bubble — bubbles sit in the
// assistant column and read as something the model said, while these are the app
// speaking (compaction ran, a retry fired, the turn failed).
//
// The label wraps instead of using whitespace-nowrap: engine error text is
// arbitrary-length, and a nowrap label blows through the pane width. Short labels
// (compaction/retry) are unaffected — they never reach the max width.
export function SystemNote({ tone = 'neutral', icon, children, sub, title, selectable }: {
  tone?: Tone
  icon?: ReactNode
  children: ReactNode
  // Optional second line, centered under the rule (compaction's token counts).
  sub?: ReactNode
  title?: string
  // Decorative notes opt out of selection; anything carrying an error message the
  // user may need to copy or paste into a bug report must stay selectable.
  selectable?: boolean
}) {
  const t = NOTE_TONE[tone]
  const rule = <div className={`flex-1 min-w-[10px] h-px ${t.line}`} />
  return (
    <div
      className={`flex flex-col items-center gap-1 my-1 anim-rise${selectable ? '' : ' select-none'}`}
      title={title}
    >
      <div className="flex items-center gap-3 w-full">
        {rule}
        <span className={`max-w-[76%] text-center leading-[1.5] break-words inline-flex items-center gap-1.5 ${t.text}`}>
          {icon && <span className="flex-none">{icon}</span>}
          {children}
        </span>
        {rule}
      </div>
      {sub}
    </div>
  )
}

const BANNER_TONE: Record<Tone, string> = {
  neutral: 'bg-surface2 border-line2 text-muted',
  warning: 'bg-amber/12 border-amber/40 text-amberink',
  danger: 'bg-redbg border-red/35 text-red',
}

// Banner is the block-level callout used inside dialogs and forms — an icon rail
// on the left, a bold heading, then body copy. Both permission-modal callouts and
// the session-settings warning are this exact shape; they differed only in color
// (and one of them hand-wrote rgba(224,86,74,0.35) instead of the red token).
//
// Body color follows the title: with a heading the heading carries the severity
// and the body reads as ink, so long copy stays comfortable; with no heading the
// body *is* the message and inherits the tone color instead of going quiet.
export function Banner({ tone = 'neutral', icon, title, children, className }: {
  tone?: Tone
  icon?: ReactNode
  title?: ReactNode
  children?: ReactNode
  className?: string
}) {
  return (
    <div className={`flex items-start gap-2 border rounded-lg px-3 py-2.5 text-[13px] ${BANNER_TONE[tone]}${className ? ' ' + className : ''}`}>
      {icon && <span className="flex-none mt-px">{icon}</span>}
      <div className="min-w-0 flex-1">
        {title && <div className="font-semibold">{title}</div>}
        {children && <div className={`break-words${title ? ' text-ink mt-0.5' : ''}`}>{children}</div>}
      </div>
    </div>
  )
}

// InlineError is page-level failure copy — the result of a command that did not
// go through. `variant` picks how loud it is:
//   'banner' (default) for a failure the user must act on (a form that refused),
//   'text' for a quiet note next to the control that produced it.
// Both keep pre-wrap + break-words: backend errors carry newlines and long paths,
// and the old bare-text call sites clipped them.
export function InlineError({ children, variant = 'banner', className }: {
  children: ReactNode
  variant?: 'banner' | 'text'
  className?: string
}) {
  if (!children) return null
  const base = 'text-red text-[13px] whitespace-pre-wrap break-words'
  return variant === 'banner'
    ? <div className={`${base} bg-redbg border border-red/25 rounded-lg px-3 py-2.5${className ? ' ' + className : ''}`}>{children}</div>
    : <div className={`${base}${className ? ' ' + className : ''}`}>{children}</div>
}
