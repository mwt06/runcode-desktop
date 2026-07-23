// ContextMeter shows how full the model's context window is — the last turn's
// input tokens against the compaction budget — with a bar that turns amber as it
// nears the 80% auto-compaction threshold, plus a manual "压缩" button. With no
// budget set (auto-compaction off) it just shows the raw occupancy.
import { Icon } from '@/ui/icons'
import { NO_DRAG } from '@/ui/tokens'
import { fmtTokens } from '@/core/format'

export function ContextMeter({
  used,
  budget,
  estimated,
  onCompact,
  compacting,
  busy,
}: {
  used: number
  budget: number
  estimated?: boolean
  onCompact: () => void
  compacting: boolean
  busy: boolean
}) {
  const pct = budget > 0 ? Math.min(100, Math.round((used / budget) * 100)) : 0
  const near = budget > 0 && pct >= 80
  const bar = pct >= 100 ? 'bg-red' : near ? 'bg-[#e0954a]' : 'bg-primary'
  // A leading "≈" marks a resume-time estimate, until the first turn reports the
  // provider's exact count.
  const approx = estimated ? '≈' : ''
  return (
    <div className="inline-flex items-center gap-2" style={NO_DRAG}>
      <span
        className="inline-flex items-center gap-1.5"
        title={
          (estimated ? '（估算值，发送一条消息后即为精确值）\n' : '') +
          (budget > 0
            ? `上下文占用 ${used.toLocaleString()} / ${budget.toLocaleString()} tokens · 达 80% 自动总结压缩`
            : `上下文占用 ${used.toLocaleString()} tokens · 未设预算，自动压缩关闭`)
        }
      >
        <span>上下文</span>
        {budget > 0 ? (
          <>
            <span className="w-[62px] h-[6px] rounded-full bg-surface2 border border-line2 overflow-hidden inline-block align-middle">
              <span className={`block h-full ${bar} transition-[width]`} style={{ width: pct + '%' }} />
            </span>
            <b className={`font-semibold tabular-nums ${near ? 'text-[#b26a1f]' : 'text-ink'}`}>{approx}{pct}%</b>
            <span className="text-faint tabular-nums">{approx}{fmtTokens(used)}/{fmtTokens(budget)}</span>
          </>
        ) : (
          <span className="text-ink font-semibold tabular-nums">{approx}{fmtTokens(used)} <span className="text-faint font-normal">· 未限</span></span>
        )}
      </span>
      <button
        onClick={onCompact}
        disabled={busy || compacting}
        title="压缩上下文：把较早的对话总结成摘要、保留最近几轮原文（磁盘记录保持完整）"
        className="inline-flex items-center gap-1 px-2 py-1 rounded-lg border border-line2 text-[12px] text-muted hover:text-ink hover:bg-surface2 transition cursor-pointer disabled:opacity-40 disabled:cursor-default"
      >
        <Icon name="compress" size={13} /> {compacting ? '压缩中…' : '压缩'}
      </button>
    </div>
  )
}
