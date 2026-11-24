// 按模型 / 供应商汇总：桌面端按请求数降序、稳定 ID 分页，每行合计原样展示；
// 已下线模型照样列出，供应商无法确定的记录归到「未记录」。
import type { UsageDimensionsPage } from '@/core/bridge'
import { formatExact, formatRate, tokenSampleLabel } from '@/core/usage'
import { BTN } from '@/ui/tokens'

const TH = 'py-2 px-3 font-normal text-muted text-[12px] whitespace-nowrap'
const TD = 'py-2 px-3 whitespace-nowrap text-right'

function TokenCell({ value, samples, requests }: { value: string | null; samples?: string; requests: string }) {
  return <td className={TD}>
    {formatExact(value)}
    <span className="block text-[11px] text-muted">{value != null ? '已知合计 · ' : ''}{tokenSampleLabel(samples, requests)}</span>
  </td>
}

export function UsageDimensions({ page, loading, onPage }: {
  page: UsageDimensionsPage
  loading: boolean
  onPage: (page: number) => void
}) {
  const items = page.items ?? []
  const label = page.groupBy === 'provider' ? '供应商' : '模型'
  return (
    <div className="flex flex-col gap-2">
      <div className="overflow-x-auto border border-line2 rounded-card bg-surface">
        <table className="w-full text-[13px] tabular-nums">
          <thead className="bg-surface2 text-left">
            <tr>
              <th className={TH}>{label}</th>
              <th className={`${TH} text-right`}>请求数</th>
              <th className={`${TH} text-right`}>输入 Token</th>
              <th className={`${TH} text-right`}>输出 Token</th>
              <th className={`${TH} text-right`}>消耗 Token</th>
              <th className={`${TH} text-right`}>缓存命中率</th>
            </tr>
          </thead>
          <tbody>
            {items.length === 0 && (
              <tr><td colSpan={6} className="py-6 text-center text-muted">所选范围没有记录</td></tr>
            )}
            {items.map((row) => {
              const name = row.id === 'unknown' || !row.name ? '未记录' : row.name
              return (
                <tr key={row.id} className="border-t border-line">
                  <td className="py-2 px-3 max-w-[280px]"><span className="block truncate" title={name}>{name}</span></td>
                  <td className={TD}>{formatExact(row.totals.requestCount)}</td>
                  <TokenCell value={row.totals.inputTokens} samples={row.totals.inputTokenSampleCount} requests={row.totals.tokenRequestCount} />
                  <TokenCell value={row.totals.outputTokens} samples={row.totals.outputTokenSampleCount} requests={row.totals.tokenRequestCount} />
                  <TokenCell value={row.totals.totalTokens} samples={row.totals.totalTokenSampleCount} requests={row.totals.tokenRequestCount} />
                  <td className={TD}>{formatRate(row.totals.cacheHitRate)}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2 text-[12px] text-muted">
        <span>第 {page.page} 页 · 共 {formatExact(page.totalCount)} 项</span>
        <div className="flex items-center gap-2">
          <button type="button" className={`${BTN} px-3 py-1 text-[12px]`} disabled={loading || page.page <= 1} onClick={() => onPage(page.page - 1)}>上一页</button>
          <button type="button" className={`${BTN} px-3 py-1 text-[12px]`} disabled={loading || !page.hasMore} onClick={() => onPage(page.page + 1)}>下一页</button>
        </div>
      </div>
    </div>
  )
}
