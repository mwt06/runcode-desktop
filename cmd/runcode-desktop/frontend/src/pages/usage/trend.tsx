// 用量趋势：Tokens 模式是输入 / 输出两段堆叠（输入已含缓存命中，命中不再叠加），请求模式
// 是请求数（含非 Token 计量的调用）。高度在 BigInt 里按最大值缩放后才转 Number；每个桶
// 可用键盘聚焦并读出精确值，下面另有一张精确值表，不依赖悬停。
import type { UsageBucket, UsageEffectiveRange } from '@/core/bridge'
import { bucketGranularity, formatBucketLabel, formatBucketRange, formatExact, parseCount } from '@/core/usage'

export type TrendMode = 'tokens' | 'requests'

const W = 720
const H = 160
const MAX_LABELS = 6
const RATIO_SCALE = 1_000_000_000n
const COVERAGE: Record<string, string> = { known: '已覆盖', gap: '采集缺口', unknown: '覆盖未知' }

const ratio = (v: bigint | null, max: bigint) => (v === null || max === 0n ? 0 : Number((v * RATIO_SCALE) / max) / Number(RATIO_SCALE))

interface Bar {
  bucket: UsageBucket
  label: string
  first: number // 底段高度比例（输入或请求数）
  second: number // 上段高度比例（输出）
  noData: boolean
}

function bars(buckets: UsageBucket[], mode: TrendMode): Bar[] {
  const rows = buckets.map((b) => {
    const a = parseCount(mode === 'tokens' ? b.inputTokens : b.requestCount)
    const o = mode === 'tokens' ? parseCount(b.outputTokens) : 0n
    return { b, a, o, total: a === null && o === null ? null : (a ?? 0n) + (o ?? 0n) }
  })
  const max = rows.reduce<bigint>((m, r) => (r.total !== null && r.total > m ? r.total : m), 0n)
  return rows.map(({ b, a, o, total }) => {
    const range = formatBucketRange(b.startAt, b.endAt)
    let detail: string
    if (total === null) detail = b.coverage === 'unknown' ? '无数据（覆盖未知）' : '无可统计的 Token'
    else detail = mode === 'tokens'
      ? `输入 ${formatExact(b.inputTokens)}，输出 ${formatExact(b.outputTokens)}${a === null || o === null ? '（已知部分）' : ''}`
      : `请求 ${formatExact(b.requestCount)}`
    if (b.coverage === 'gap') detail += ' · 采集缺口'
    return { bucket: b, label: `${range}：${detail}`, first: ratio(a, max), second: ratio(o, max), noData: total === null }
  })
}

const MODE_BTN = 'px-2.5 py-1 rounded-md text-[12px] transition'

export function UsageTrend({ buckets, range, mode, onModeChange, coverageStatus }: {
  buckets: UsageBucket[]
  range: UsageEffectiveRange
  mode: TrendMode
  onModeChange: (mode: TrendMode) => void
  coverageStatus: string
}) {
  const g = bucketGranularity(range.startAt, range.endAt)
  const data = bars(buckets, mode)
  const n = Math.max(data.length, 1)
  const slot = W / n
  const barW = Math.max(slot * 0.68, 1)
  const step = Math.max(1, Math.ceil(data.length / MAX_LABELS))
  const hasGap = buckets.some((b) => b.coverage === 'gap')
  return (
    <section aria-label="用量趋势" className="bg-surface border border-line2 rounded-card p-4 flex flex-col gap-3 shadow-xs">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-3 text-[12px] text-muted">
          <span className="text-[14px] font-semibold text-ink">用量趋势</span>
          {mode === 'tokens' ? (
            <>
              <span className="inline-flex items-center gap-1.5"><span className="w-2.5 h-2.5 rounded-sm bg-primary" />输入（含缓存命中）</span>
              <span className="inline-flex items-center gap-1.5"><span className="w-2.5 h-2.5 rounded-sm bg-primary/35" />输出</span>
            </>
          ) : (
            <span className="inline-flex items-center gap-1.5"><span className="w-2.5 h-2.5 rounded-sm bg-primary" />请求数</span>
          )}
          {hasGap && <span className="inline-flex items-center gap-1.5"><span className="w-2.5 h-2.5 rounded-sm bg-line2" />采集缺口</span>}
        </div>
        <div className="inline-flex p-0.5 rounded-field bg-surface2 border border-line2" role="group" aria-label="趋势指标">
          {(['tokens', 'requests'] as const).map((m) => (
            <button
              key={m}
              type="button"
              aria-pressed={mode === m}
              onClick={() => onModeChange(m)}
              className={`${MODE_BTN} ${mode === m ? 'bg-surface text-primaryink font-medium shadow-xs' : 'text-muted hover:text-ink'}`}
            >
              {m === 'tokens' ? 'Tokens' : '请求'}
            </button>
          ))}
        </div>
      </div>

      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="w-full h-[160px] block" role="list" aria-label="按时间段的用量">
        <line x1={0} y1={H - 0.5} x2={W} y2={H - 0.5} className="stroke-line2" />
        {data.map((d, i) => {
          const x = i * slot + (slot - barW) / 2
          const h1 = d.first * (H - 4)
          const h2 = d.second * (H - 4)
          return (
            <g key={d.bucket.startAt} role="listitem" tabIndex={0} aria-label={d.label} className="outline-none focus:opacity-80">
              <title>{d.label}</title>
              <rect x={i * slot} y={0} width={slot} height={H} className="fill-transparent" />
              {d.bucket.coverage === 'gap' && <rect x={x} y={0} width={barW} height={H} className="fill-line2/60" />}
              {!d.noData && <rect x={x} y={H - h1} width={barW} height={h1} className="fill-primary" />}
              {!d.noData && mode === 'tokens' && <rect x={x} y={H - h1 - h2} width={barW} height={h2} className="fill-primary/35" />}
            </g>
          )
        })}
      </svg>
      <div className="relative h-4 text-[11px] text-faint tabular-nums" aria-hidden>
        {data.map((d, i) => (i % step === 0 ? (
          <span key={d.bucket.startAt} className="absolute -translate-x-1/2 whitespace-nowrap" style={{ left: `${((i + 0.5) / n) * 100}%` }}>
            {formatBucketLabel(d.bucket.startAt, g)}
          </span>
        ) : null))}
      </div>

      <p className="m-0 text-[12px] text-muted">
        {mode === 'tokens' ? '非 Token 计量的调用不计入此图。' : '请求数包含非 Token 计量的调用。'}
        {coverageStatus === 'gap' ? ' 存在采集缺口：仅显示已采集用量。' : coverageStatus !== 'known' && ' 覆盖范围未知：空白时段不代表零用量。'}
      </p>

      <details>
        <summary className="cursor-pointer select-none text-[12px] text-muted hover:text-ink w-fit">查看数据表（精确值）</summary>
        <div className="mt-2 overflow-x-auto">
          <table className="w-full text-[12px] tabular-nums">
            <thead className="text-muted text-left">
              <tr>
                <th className="py-1 pr-3 font-normal">时间段</th>
                {mode === 'tokens' ? (
                  <>
                    <th className="py-1 pr-3 font-normal text-right">输入</th>
                    <th className="py-1 pr-3 font-normal text-right">输出</th>
                    <th className="py-1 pr-3 font-normal text-right">缓存命中</th>
                  </>
                ) : <th className="py-1 pr-3 font-normal text-right">请求数</th>}
                <th className="py-1 font-normal">覆盖</th>
              </tr>
            </thead>
            <tbody>
              {buckets.map((b) => (
                <tr key={b.startAt} className="border-t border-line">
                  <td className="py-1 pr-3 whitespace-nowrap">{formatBucketRange(b.startAt, b.endAt)}</td>
                  {mode === 'tokens' ? (
                    <>
                      <td className="py-1 pr-3 text-right">{formatExact(b.inputTokens)}</td>
                      <td className="py-1 pr-3 text-right">{formatExact(b.outputTokens)}</td>
                      <td className="py-1 pr-3 text-right">{formatExact(b.cacheHitTokens)}</td>
                    </>
                  ) : <td className="py-1 pr-3 text-right">{formatExact(b.requestCount)}</td>}
                  <td className="py-1 text-muted">{COVERAGE[b.coverage] ?? '覆盖未知'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </section>
  )
}
