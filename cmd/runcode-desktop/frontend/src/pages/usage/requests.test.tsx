// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { UsageRequests } from './requests'
import { fixtureRequests, fixtureRow } from './fixtures'

const text = (markup: string) => markup.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')
const noop = () => {}
const COST_WORDS = ['成本', '费用', '价格', '单价', '余额', '¥', '$']

function render(over: Partial<Parameters<typeof UsageRequests>[0]> = {}) {
  return renderToStaticMarkup(
    <UsageRequests page={fixtureRequests()} pageIndex={1} pageSize={20} providerColumn={false} loading={false} onPrev={noop} onNext={noop} onPageSize={noop} {...over} />,
  )
}

describe('UsageRequests', () => {
  it('启用状态列后展示显式状态，历史缺失字段仍为未知', () => {
    const page = fixtureRequests({ items: [fixtureRow({ id: 'known', status: 'save_failed' }), fixtureRow({ id: 'legacy' })] })
    const t = text(render({ page, statusColumn: true }))
    expect(t).toContain('状态')
    expect(t).toContain('保存失败')
    expect(t).toContain('未知')
    expect(t).not.toContain('未提交')
  })

  it('默认列与时长文案：总耗时不冒充首字时间', () => {
    const t = text(render())
    for (const col of ['时间（入账）', '模型', '输入 Token', '输出 Token', '缓存命中', '未命中输入', '总耗时']) expect(t).toContain(col)
    expect(t).toContain('1.2 秒')
    expect(t).not.toContain('首字')
    expect(t).not.toContain('状态')
    expect(t).not.toContain('Tokens/s')
    for (const w of COST_WORDS) expect(t).not.toContain(w)
  })

  it('20/50/100 每页条数、上一页在第一页不可用、读取时总数', () => {
    const markup = render()
    for (const n of ['20', '50', '100']) expect(markup).toContain(`<option value="${n}"`)
    expect(markup).toMatch(/<button[^>]*disabled=""[^>]*>上一页/)
    expect(text(markup)).toContain('第 1 页')
    expect(text(markup)).toContain('共 21 条')
    const last = render({ page: fixtureRequests({ hasMore: false, nextCursor: '' }), pageIndex: 3 })
    expect(last).toMatch(/<button[^>]*disabled=""[^>]*>下一页/)
    expect(text(last)).toContain('第 3 页')
  })

  it('非 Token 记录在 Token 四列显示横划线；长模型名截断并保留全文', () => {
    const long = 'a-very-long-model-name-that-should-be-truncated-in-the-table-but-kept-in-title'
    const page = fixtureRequests({
      items: [
        fixtureRow({ id: 'img', meterKind: 'image', inputTokens: null, outputTokens: null, cacheHitTokens: null, cacheMissTokens: null, usageQuantity: '4', usageUnit: '张', model: long }),
        fixtureRow({ id: 'unknown-cache', cacheHitTokens: null, cacheMissTokens: null }),
      ],
    })
    const markup = render({ page })
    const t = text(markup)
    expect(t).not.toContain('不适用')
    // 生图行四个 Token 列均为 —（输入/输出/缓存命中/未命中）。
    expect(t.match(/—/g)?.length ?? 0).toBeGreaterThanOrEqual(4)
    expect(markup).toContain(`title="${long}"`)
    expect(markup).toContain('truncate')
  })

  it('供应商不可靠时没有供应商列，也不虚构品牌', () => {
    expect(text(render())).not.toContain('供应商')
    const t = text(render({ providerColumn: true, page: fixtureRequests({ items: [fixtureRow({ providerName: null })] }) }))
    expect(t).toContain('供应商')
    expect(t).toContain('未记录')
  })

  it('详情只给记录 ID、计量与口径', async () => {
    ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
    const host = document.createElement('div')
    const root = createRoot(host)
    const onNext = vi.fn()
    await act(async () => root.render(
      <UsageRequests page={fixtureRequests({ items: [fixtureRow({ id: 'log-42', meterKind: 'audio', usageQuantity: '90', usageUnit: '秒', inputTokens: null, outputTokens: null, cacheHitTokens: null, cacheMissTokens: null })] })} pageIndex={1} pageSize={20} providerColumn={false} loading={false} onPrev={noop} onNext={onNext} onPageSize={noop} />,
    ))
    const toggle = host.querySelector('button[aria-expanded]') as HTMLButtonElement
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    await act(async () => toggle.click())
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(host.textContent).toContain('log-42')
    expect(host.textContent).toContain('90 秒')
    expect(host.textContent).toContain('历史口径未确认')
    expect(host.textContent).not.toMatch(/提示词|回答|正文|密钥/)
    await act(async () => ([...host.querySelectorAll('button')].find((b) => b.textContent === '下一页') as HTMLButtonElement).click())
    expect(onNext).toHaveBeenCalledTimes(1)
    await act(async () => root.unmount())
  })
})
