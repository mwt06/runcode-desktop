// Small inline status chips shared by the conversation cards and the manager pages.

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
