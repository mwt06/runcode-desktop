import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { UsageTrend } from './trend'
import { FIXTURE_RANGE, fixtureBucket, fixtureOverview } from './fixtures'

const text = (markup: string) => markup.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')
const noop = () => {}

describe('UsageTrend', () => {
  it.each([
    [{ inputTokens: '1000', outputTokens: null }, '输入 1,000，输出 —', 'fill-primary'],
    [{ inputTokens: null, outputTokens: '500' }, '输入 —，输出 500', 'fill-primary/35'],
  ])('单项 Token 未知时仍绘制另一项已知值：%o', (tokens, label, fill) => {
    const bucket = fixtureBucket(FIXTURE_RANGE.startAt, FIXTURE_RANGE.endAt, { ...tokens, totalTokens: null })
    const markup = renderToStaticMarkup(<UsageTrend buckets={[bucket]} range={FIXTURE_RANGE} mode="tokens" onModeChange={noop} coverageStatus="known" />)
    expect(markup).toContain(`${label}（已知部分）`)
    expect(markup).toContain(`height="156" class="${fill}"`)
  })

  it('Tokens 模式：输入（含缓存命中）与输出两段，表格给出精确值与完整区间', () => {
    const o = fixtureOverview()
    const markup = renderToStaticMarkup(<UsageTrend buckets={o.trend ?? []} range={FIXTURE_RANGE} mode="tokens" onModeChange={noop} coverageStatus="known" />)
    const t = text(markup)
    expect(t).toContain('输入（含缓存命中）')
    expect(t).toContain('输出')
    expect(t).toContain('10,000')
    expect(t).toContain('2026-11-10 06:00 — 2026-11-10 12:00（上海时间）')
    expect(t).toContain('非 Token 计量的调用不计入此图')
    expect(markup).toContain('tabindex="0"')
    expect(markup).toContain('aria-label="2026-11-10 06:00 — 2026-11-10 12:00（上海时间）：输入 10,000，输出 1,000"')
  })

  it('请求模式只有请求数，不带 Token 单位', () => {
    const o = fixtureOverview()
    const t = text(renderToStaticMarkup(<UsageTrend buckets={o.trend ?? []} range={FIXTURE_RANGE} mode="requests" onModeChange={noop} coverageStatus="known" />))
    expect(t).toContain('请求数')
    expect(t).not.toContain('输入（含缓存命中）')
    expect(t).not.toMatch(/\d Token/)
  })

  it.each([
    ['2026-11-10T00:00:00+08:00', '2026-11-10T02:00:00+08:00', '01:00'],
    ['2026-11-01T00:00:00+08:00', '2026-11-03T00:00:00+08:00', '11-02'],
    ['2026-01-01T00:00:00+08:00', '2026-11-01T00:00:00+08:00', '2026-10'],
  ])('按范围长度选小时/日/月刻度：%s → %s', (start, end, label) => {
    const buckets = label === '01:00'
      ? [fixtureBucket(start, '2026-11-10T01:00:00+08:00'), fixtureBucket('2026-11-10T01:00:00+08:00', end)]
      : label === '11-02'
        ? [fixtureBucket(start, '2026-11-02T00:00:00+08:00'), fixtureBucket('2026-11-02T00:00:00+08:00', end)]
        : [fixtureBucket(start, '2026-10-01T00:00:00+08:00'), fixtureBucket('2026-10-01T00:00:00+08:00', end)]
    const t = text(renderToStaticMarkup(<UsageTrend buckets={buckets} range={{ ...FIXTURE_RANGE, startAt: start, endAt: end }} mode="requests" onModeChange={noop} coverageStatus="known" />))
    expect(t).toContain(label)
  })

  it('首尾部分桶按真实裁剪边界显示', () => {
    const buckets = [
      fixtureBucket('2026-11-10T00:30:00+08:00', '2026-11-10T01:00:00+08:00'),
      fixtureBucket('2026-11-10T01:00:00+08:00', '2026-11-10T01:20:00+08:00'),
    ]
    const t = text(renderToStaticMarkup(<UsageTrend buckets={buckets} range={{ ...FIXTURE_RANGE, startAt: buckets[0].startAt, endAt: buckets[1].endAt }} mode="requests" onModeChange={noop} coverageStatus="known" />))
    expect(t).toContain('2026-11-10 00:30 — 2026-11-10 01:00')
    expect(t).toContain('2026-11-10 01:00 — 2026-11-10 01:20')
  })

  it('覆盖未知与采集缺口如实标注，空白不当作零', () => {
    const buckets = [
      fixtureBucket('2026-11-10T00:00:00+08:00', '2026-11-10T06:00:00+08:00', { coverage: 'gap', inputTokens: null, outputTokens: null, totalTokens: null }),
      fixtureBucket('2026-11-10T06:00:00+08:00', '2026-11-10T12:00:00+08:00'),
    ]
    const t = text(renderToStaticMarkup(<UsageTrend buckets={buckets} range={FIXTURE_RANGE} mode="tokens" onModeChange={noop} coverageStatus="unknown" />))
    expect(t).toContain('采集缺口')
    expect(t).toContain('覆盖范围未知')
  })
})
