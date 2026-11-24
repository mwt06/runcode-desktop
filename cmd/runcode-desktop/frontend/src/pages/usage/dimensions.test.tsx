import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { UsageDimensionsPage } from '@/core/bridge'
import { UsageDimensions } from './dimensions'
import { FIXTURE_RANGE, fixtureTotals } from './fixtures'

const text = (markup: string) => markup.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')
const noop = () => {}

const page = (over: Partial<UsageDimensionsPage> = {}): UsageDimensionsPage => ({
  items: [
    { id: 'qwen-max', name: 'qwen-max', totals: fixtureTotals() },
    { id: 'retired-model', name: 'retired-model', totals: fixtureTotals({ cacheHitRate: null, cacheHitTokens: null, cacheMissTokens: null, cacheEligibleInputTokens: null }) },
  ],
  groupBy: 'model', page: 1, pageSize: 50, totalCount: '2', hasMore: false, effectiveRange: FIXTURE_RANGE, queryKey: 'qk',
  ...over,
})

describe('UsageDimensions', () => {
  it('部分 Token 合计展示各自样本，未知输出不隐藏已知输入', () => {
    const totals = fixtureTotals({ inputTokens: '1000', outputTokens: null, totalTokens: '1000', inputTokenSampleCount: '1', outputTokenSampleCount: '0', totalTokenSampleCount: '1', quality: { ...fixtureTotals().quality, tokenCoverage: 'partial' } })
    const t = text(renderToStaticMarkup(<UsageDimensions page={page({ items: [{ id: 'partial', name: 'partial', totals }] })} loading={false} onPage={noop} />))
    expect(t).toContain('1,000')
    expect(t).toContain('已知合计')
    expect(t).toContain('样本 1/2 次')
    expect(t).toContain('样本 0/2 次')
  })

  it('按模型汇总：服务端合计原样展示，已下线模型保留，未知命中率为 —', () => {
    const t = text(renderToStaticMarkup(<UsageDimensions page={page()} loading={false} onPage={noop} />))
    for (const col of ['模型', '请求数', '输入 Token', '输出 Token', '消耗 Token', '缓存命中率']) expect(t).toContain(col)
    expect(t).toContain('retired-model')
    expect(t).toContain('11,000')
    expect(t).toContain('17.0%')
    expect(t).toContain('—')
    expect(t).not.toMatch(/成本|费用|价格|¥|\$/)
  })

  it('供应商汇总：未记录供应商明确归组，不虚构品牌', () => {
    const t = text(renderToStaticMarkup(<UsageDimensions page={page({ groupBy: 'provider', items: [{ id: 'unknown', name: '', totals: fixtureTotals() }] })} loading={false} onPage={noop} />))
    expect(t).toContain('供应商')
    expect(t).toContain('未记录')
  })

  it('分页按服务端 hasMore，不在前端截取前 N 个冒充全部', () => {
    const markup = renderToStaticMarkup(<UsageDimensions page={page({ hasMore: true, totalCount: '120' })} loading={false} onPage={noop} />)
    expect(markup).toMatch(/<button[^>]*disabled=""[^>]*>上一页/)
    expect(markup).not.toMatch(/<button[^>]*disabled=""[^>]*>下一页/)
    expect(text(markup)).toContain('共 120 项')
  })
})
