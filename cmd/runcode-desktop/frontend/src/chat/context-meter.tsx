// ContextMeter shows how full the model's context window is, against the compaction
// budget, with a bar that turns amber as it nears the 80% threshold where the engine
// automatically condenses and carries on — plus a manual "压缩" button. With no
// budget set (auto-compaction off) it just shows the raw occupancy.
//
// The figure updates on every model round-trip, not once per turn: a turn is where
// context fills up (one can span dozens of tool rounds), so a per-turn meter showed
// a number that was already stale. It is a calibrated estimate rather than a
// provider-exact count — the same estimate the automatic thresholds act on, so what
// the bar shows and what the engine does always agree.
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
  const bar = pct >= 100 ? 'bg-red' : near ? 'bg-amber' : 'bg-primary'
  // A leading "≈" marks the figure as not yet calibrated against this model's real
  // token usage — the state a resumed session starts in, before any round-trip has
  // been observed.
  const approx = estimated ? '≈' : ''
  return (
    <div className="inline-flex items-center gap-2" style={NO_DRAG}>
      <span
        className="inline-flex items-center gap-1.5"
        title={
          (estimated ? '（尚未按当前模型校准，完成一次往返后自动校准）\n' : '') +
          (budget > 0
            ? `上下文占用 ${used.toLocaleString()} / ${budget.toLocaleString()} tokens\n每次模型往返实时刷新 · 达 80% 自动整理并总结后继续`
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
