// 请求日志：本机快照游标分页，页序号由 useUsage 维护，只能前后翻、不能跳到没访问过的页。
// 非 Token 计量的记录在 Token 列统一显示「—」（生图等只计调用次数）；用量与单位放在详情里。
// 详情只有记录 ID、模型、时间、用量与口径，没有正文、成本或密钥。
import { Fragment, useState } from 'react'
import type { UsageRequestRow, UsageRequestsPage } from '@/core/bridge'
import { TOKENS_METER, formatDurationMs, formatExact, formatShanghai, requestStatusLabel, sourceLabel } from '@/core/usage'
import { BTN } from '@/ui/tokens'
import type { UsagePageSize } from './use-usage'

const TH = 'py-2 px-3 font-normal text-muted text-[12px] whitespace-nowrap'
const TD = 'py-2 px-3 whitespace-nowrap'
const PAGE_SIZES: UsagePageSize[] = [20, 50, 100]

const tokenCell = (row: UsageRequestRow, value: string | null) => (row.meterKind === TOKENS_METER ? formatExact(value) : '—')

function meterText(row: UsageRequestRow): string {
  if (row.meterKind === TOKENS_METER) return 'Token'
  if (row.usageQuantity == null) return `${row.meterKind}（用量未记录）`
  return `${formatExact(row.usageQuantity)} ${row.usageUnit ?? ''}`.trim()
}

function Detail({ row }: { row: UsageRequestRow }) {
  const items: [string, string][] = [
    ['记录 ID', row.id],
    ['模型', row.model || '未记录'],
    ['入账时间', formatShanghai(row.recordedAt, true)],
    ['开始时间', row.startedAt ? formatShanghai(row.startedAt, true) : '—'],
    ['计量', meterText(row)],
    ['Token 口径', sourceLabel(row.quality.tokenSource)],
    ['缓存口径', sourceLabel(row.quality.cacheSource)],
    ['思考 Token', `${formatExact(row.reasoningTokens)}（${sourceLabel(row.quality.reasoningSource)}）`],
    ['总耗时', `${formatDurationMs(row.durationMs)}（整次调用耗时，不等于首个字返回的时间）`],
  ]
  return (
    <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[12px]">
      {items.map(([k, v]) => (
        <Fragment key={k}>
          <dt className="text-muted">{k}</dt>
          <dd className="m-0 text-ink break-all">{v}</dd>
        </Fragment>
      ))}
    </dl>
  )
}

export function UsageRequests({ page, pageIndex, pageSize, providerColumn, statusColumn = false, loading, onPrev, onNext, onPageSize }: {
  page: UsageRequestsPage
  pageIndex: number
  pageSize: UsagePageSize
  providerColumn: boolean
  statusColumn?: boolean
  loading: boolean
  onPrev: () => void
  onNext: () => void
  onPageSize: (size: UsagePageSize) => void
}) {
  const [expanded, setExpanded] = useState('')
  const items = page.items ?? []
  const columns = 8 + Number(providerColumn) + Number(statusColumn)
  return (
    <div className="flex flex-col gap-2">
      <div className="overflow-x-auto border border-line2 rounded-card bg-surface">
        <table className="w-full text-[13px] tabular-nums">
          <thead className="bg-surface2 text-left">
            <tr>
              <th className={TH}>时间（入账）</th>
              <th className={TH}>模型</th>
              {providerColumn && <th className={TH}>供应商</th>}
              {statusColumn && <th className={TH}>状态</th>}
              <th className={`${TH} text-right`}>输入 Token</th>
              <th className={`${TH} text-right`}>输出 Token</th>
              <th className={`${TH} text-right`}>缓存命中</th>
              <th className={`${TH} text-right`}>未命中输入</th>
              <th className={`${TH} text-right`} title="整次调用的耗时，不是首个字返回的时间">总耗时</th>
              {/* 隐藏列名直接交给表头，避免绝对定位的隐藏文本越过滚动区撑高文档。 */}
              <th className={TH} aria-label="详情" />
            </tr>
          </thead>
          <tbody>
            {items.length === 0 && (
              <tr><td colSpan={columns} className="py-6 text-center text-muted">本页没有记录</td></tr>
            )}
            {items.map((row) => (
              <Fragment key={row.id}>
                <tr className="border-t border-line">
                  <td className={TD}>{formatShanghai(row.recordedAt, true)}</td>
                  <td className="py-2 px-3 max-w-[240px]"><span className="block truncate" title={row.model}>{row.model || '未记录'}</span></td>
                  {providerColumn && <td className={TD}>{row.providerName ?? row.providerId ?? '未记录'}</td>}
                  {statusColumn && <td className={TD}>{requestStatusLabel(row.status)}</td>}
                  <td className={`${TD} text-right`}>{tokenCell(row, row.inputTokens)}</td>
                  <td className={`${TD} text-right`}>{tokenCell(row, row.outputTokens)}</td>
                  <td className={`${TD} text-right`}>{tokenCell(row, row.cacheHitTokens)}</td>
                  <td className={`${TD} text-right`}>{tokenCell(row, row.cacheMissTokens)}</td>
                  <td className={`${TD} text-right`}>{formatDurationMs(row.durationMs)}</td>
                  <td className={TD}>
                    <button
                      type="button"
                      aria-expanded={expanded === row.id}
                      onClick={() => setExpanded((id) => (id === row.id ? '' : row.id))}
                      className="text-[12px] text-primaryink hover:underline"
                    >
                      详情
                    </button>
                  </td>
                </tr>
                {expanded === row.id && (
                  <tr className="bg-surface2"><td colSpan={columns} className="px-3 py-2.5"><Detail row={row} /></td></tr>
                )}
              </Fragment>
            ))}
          </tbody>
        </table>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2 text-[12px] text-muted">
        <span>第 {pageIndex} 页 · 共 {formatExact(page.totalCount)} 条（读取时的可见总数）</span>
        <div className="flex items-center gap-2">
          <label className="inline-flex items-center gap-1.5">
            每页
            <select
              value={String(pageSize)}
              disabled={loading}
              onChange={(e) => onPageSize(Number(e.target.value) as UsagePageSize)}
              className="bg-surface2 border border-line2 rounded-field px-2 py-1 text-[12px] text-ink"
            >
              {PAGE_SIZES.map((n) => <option key={n} value={String(n)}>{n}</option>)}
            </select>
            条
          </label>
          <button type="button" className={`${BTN} px-3 py-1 text-[12px]`} disabled={loading || pageIndex <= 1} onClick={onPrev}>上一页</button>
          <button type="button" className={`${BTN} px-3 py-1 text-[12px]`} disabled={loading || !page.hasMore} onClick={onNext}>下一页</button>
        </div>
      </div>
    </div>
  )
}
