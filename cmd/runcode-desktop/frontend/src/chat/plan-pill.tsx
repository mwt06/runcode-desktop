// PlanPill is the top-center progress board: a compact pill showing the current
// step, files changed, and the running diff totals. Clicking it drops down the full
// task timeline (the same spine list). It stays visible whenever a plan exists.
import { Icon } from '@/ui/icons'
import { CheckMark, Spinner } from '@/ui/glyphs'
import { DiffStat } from '@/ui/badges'
import { type PlanItem, type PlanSnapshot } from '@/core/bridge'

export function PlanPill({
  plan,
  open,
  onToggle,
  filesChanged,
  adds,
  dels,
  running,
}: {
  plan: PlanSnapshot
  open: boolean
  onToggle: (open: boolean) => void
  filesChanged: number
  adds: number
  dels: number
  running: boolean
}) {
  const allDone = plan.total > 0 && plan.done >= plan.total
  const activeIndex = plan.items.findIndex((it) => it.status === 'in_progress')
  // "第 N / M 步": the running step if one is active, otherwise how many are done.
  const step = activeIndex >= 0 ? activeIndex + 1 : plan.done
  const frontier = activeIndex >= 0 ? activeIndex : plan.done
  const live = running || activeIndex >= 0
  return (
    <>
      <button
        onClick={() => onToggle(!open)}
        className={`relative z-10 inline-flex items-center gap-2.5 pl-3 pr-2.5 py-2 rounded-full bg-surface shadow-card text-[13px] cursor-pointer transition border ${open ? 'border-primary' : 'border-line2 hover:border-primary'}`}
      >
        {allDone ? (
          <CheckMark size={13} className="text-green flex-none" />
        ) : live ? (
          <Spinner size={13} />
        ) : (
          <span className="w-2 h-2 rounded-full bg-primary flex-none" />
        )}
        <span className="text-ink whitespace-nowrap">
          第 <b className="font-semibold tabular-nums">{step}</b> <span className="text-faint">/ {plan.total}</span> 步
        </span>
        {filesChanged > 0 && (
          <>
            <span className="w-px h-3.5 bg-line2 flex-none" />
            <span className="text-muted whitespace-nowrap">{filesChanged} 个文件已更改</span>
            {(adds > 0 || dels > 0) && (
              <DiffStat add={adds} del={dels} className="font-mono tabular-nums text-[12px] whitespace-nowrap" />
            )}
          </>
        )}
        <Icon name="chevron-down" size={14} className={`flex-none text-faint transition ${open ? 'rotate-180' : ''}`} />
      </button>
      {open && (
        <div className="absolute top-full left-1/2 -translate-x-1/2 mt-1.5 w-[380px] max-w-[calc(100vw-40px)] z-10">
          <div className="bg-surface border border-line2 rounded-card shadow-card overflow-hidden anim-rise">
            <div className="px-4 py-3 max-h-[52vh] overflow-y-auto">
              {plan.items.map((it, i) => (
                <PlanRow key={i} item={it} last={i === plan.items.length - 1} filled={i < frontier} />
              ))}
            </div>
          </div>
        </div>
      )}
    </>
  )
}

// PlanRow is one step on the timeline: a status marker in the left gutter, a spine
// segment dropping from it to the next marker (primary when `filled`, faint
// otherwise), and the label. The active step prefers its present-continuous
// activeForm and reads in primary; completed steps dim; pending steps stay muted.
function PlanRow({ item, last, filled }: { item: PlanItem; last: boolean; filled: boolean }) {
  const inProgress = item.status === 'in_progress'
  const done = item.status === 'completed'
  const label = inProgress && item.activeForm?.trim() ? item.activeForm : item.content
  const textClass = done ? 'text-faint' : inProgress ? 'text-primaryink font-medium' : 'text-muted'
  return (
    <div className="flex gap-3">
      <div className="w-[13px] flex-none flex flex-col items-center">
        <PlanMark status={item.status} />
        {!last && <span className={`w-[2px] flex-1 min-h-[14px] my-1 rounded-full ${filled ? 'bg-primary' : 'bg-line2'}`} />}
      </div>
      <span className={`min-w-0 break-words text-[13px] leading-[1.5] ${last ? 'pb-1' : 'pb-4'} ${textClass}`}>{label}</span>
    </div>
  )
}

// PlanMark is the per-step glyph: a filled primary check (completed), a primary core
// dot inside a pulsing halo (in progress), or a hollow faint dot (pending).
function PlanMark({ status }: { status: string }) {
  if (status === 'completed') {
    return (
      <span className="w-[13px] h-[13px] flex-none rounded-full bg-primary inline-flex items-center justify-center text-white">
        <CheckMark size={8} />
      </span>
    )
  }
  if (status === 'in_progress') {
    return (
      <span className="relative w-[13px] h-[13px] flex-none inline-flex items-center justify-center">
        <span className="absolute w-[13px] h-[13px] rounded-full plan-pulse" />
        <span className="relative w-[9px] h-[9px] rounded-full bg-primary ring-2 ring-surface" />
      </span>
    )
  }
  return <span className="w-[13px] h-[13px] flex-none rounded-full border-[1.6px] border-line2 bg-surface" />
}
