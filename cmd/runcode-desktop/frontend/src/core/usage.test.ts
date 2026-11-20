import { describe, expect, it } from 'vitest'
import {
  bucketGranularity, buildUsageQuery, customDraftToSelection, formatBucketRange, formatCompact, formatDurationMs,
  formatExact, formatRate, parseCount, previewRange, scaleCounts, sourceLabel, toOffsetISO, toShanghaiInput,
  usageFailure,
} from './usage'

const at = (iso: string) => new Date(iso)
const NOW = at('2026-11-10T01:30:45+08:00')

describe('精确整数', () => {
  it('超过 2^53 的计数逐位保留，不经 Number', () => {
    expect(formatExact('9007199254740993')).toBe('9,007,199,254,740,993')
    expect(formatExact('9223372036854775807')).toBe('9,223,372,036,854,775,807')
    expect(parseCount('9007199254740993')).toBe(9007199254740993n)
  })
  it('null 显示 —，"0" 显示 0，非法值不被当成 0', () => {
    expect(formatExact(null)).toBe('—')
    expect(formatExact('0')).toBe('0')
    expect(formatExact('-1')).toBe('—')
    expect(formatExact('1.5')).toBe('—')
    expect(parseCount('007')).toBeNull()
  })
  it('缩写只用于显示，不四舍五入抬高', () => {
    expect(formatCompact('9999')).toBe('9,999')
    expect(formatCompact('11000')).toBe('1.1万')
    expect(formatCompact('19999')).toBe('1.9万')
    expect(formatCompact('123456789')).toBe('1.2亿')
    expect(formatCompact('9007199254740993')).toBe('90071992.5亿')
    expect(formatCompact(null)).toBe('—')
  })
  it('图表缩放在 BigInt 里完成，超大值比例不失真', () => {
    const { ratios } = scaleCounts(['9007199254740993', '4503599627370496', null, '0'])
    expect(ratios[0]).toBe(1)
    expect(ratios[1]).toBeCloseTo(0.5, 6)
    expect(ratios[2]).toBeNull()
    expect(ratios[3]).toBe(0)
    expect(scaleCounts(['0', null]).ratios).toEqual([0, null])
  })
})

describe('比例与耗时', () => {
  it('加权命中率按一位小数显示，未知为 —，真 0 是 0.0%', () => {
    expect(formatRate(0.17)).toBe('17.0%')
    expect(formatRate(0)).toBe('0.0%')
    expect(formatRate(null)).toBe('—')
  })
  it('总耗时不是首字时间：毫秒/秒文案', () => {
    expect(formatDurationMs(850)).toBe('850 毫秒')
    expect(formatDurationMs(1500)).toBe('1.5 秒')
    expect(formatDurationMs(null)).toBe('—')
  })
})

describe('上海时间范围', () => {
  it('当天从上海 00:00 起，近 24 小时是滚动窗口', () => {
    expect(toOffsetISO(previewRange('today', NOW).start!)).toBe('2026-11-10T00:00:00+08:00')
    expect(toOffsetISO(previewRange('last24h', NOW).start!)).toBe('2026-11-09T01:30:45+08:00')
  })
  it('近 7/14/30 天是含今天的自然日', () => {
    expect(toOffsetISO(previewRange('last7d', NOW).start!)).toBe('2026-11-04T00:00:00+08:00')
    expect(toOffsetISO(previewRange('last14d', NOW).start!)).toBe('2026-10-28T00:00:00+08:00')
    expect(toOffsetISO(previewRange('last30d', NOW).start!)).toBe('2026-10-12T00:00:00+08:00')
  })
  it('UTC 已跨日而上海未跨日时仍按上海自然日', () => {
    const lateUtc = at('2026-11-09T23:30:00Z') // 上海 11-10 07:30
    expect(toOffsetISO(previewRange('today', lateUtc).start!)).toBe('2026-11-10T00:00:00+08:00')
  })
  it('全部范围不猜起点', () => {
    expect(previewRange('all', NOW).start).toBeNull()
  })
  it('分钟级输入与带偏移 ISO 互转', () => {
    expect(toShanghaiInput(at('2026-11-09T16:05:00Z'))).toBe('2026-11-10T00:05')
  })
})

describe('自定义范围草稿', () => {
  it('合法草稿转成带 +08:00 偏移的半开区间', () => {
    const r = customDraftToSelection({ start: '2026-11-09T08:00', end: '2026-11-10T00:00', followNow: false }, NOW)
    expect(r.error).toBeUndefined()
    expect(r.selection).toEqual({ kind: 'custom', startAt: '2026-11-09T08:00:00+08:00', endAt: '2026-11-10T00:00:00+08:00', followNow: false })
  })
  it('跟随现在不带结束时间', () => {
    const r = customDraftToSelection({ start: '2026-11-09T08:00', end: '', followNow: true }, NOW)
    expect(r.selection).toEqual({ kind: 'custom', startAt: '2026-11-09T08:00:00+08:00', followNow: true })
  })
  it.each([
    [{ start: '', end: '2026-11-10T00:00', followNow: false }, '请填写开始时间'],
    [{ start: '2026-11-10T02:00', end: '', followNow: true }, '开始时间不能晚于当前时刻'],
    [{ start: '2026-11-09T08:00', end: '', followNow: false }, '请填写结束时间'],
    [{ start: '2026-11-09T08:00', end: '2026-11-09T08:00', followNow: false }, '开始时间必须早于结束时间'],
    [{ start: '2026-11-09T08:00', end: '2026-11-10T02:00', followNow: false }, '结束时间不能晚于当前时刻'],
    [{ start: '2026-13-09T08:00', end: '2026-11-10T00:00', followNow: false }, '请填写开始时间'],
  ])('非法草稿 %o 给出原因且不产生查询', (draft, message) => {
    const r = customDraftToSelection(draft, NOW)
    expect(r.selection).toBeUndefined()
    expect(r.error).toBe(message)
  })
})

describe('查询与分桶', () => {
  it('查询只带单租户与范围，不带任何身份字段', () => {
    const q = buildUsageQuery('tenant-a', { kind: 'today' }, { model: 'qwen-max' })
    expect(q).toEqual({ tenantId: 'tenant-a', range: 'today', followNow: false, timezone: 'Asia/Shanghai', model: 'qwen-max' })
    expect(Object.keys(q)).not.toContain('userId')
    const custom = buildUsageQuery('t', { kind: 'custom', startAt: '2026-11-09T08:00:00+08:00', followNow: true }, {})
    expect(custom).toEqual({ tenantId: 't', range: 'custom', startAt: '2026-11-09T08:00:00+08:00', followNow: true, timezone: 'Asia/Shanghai' })
  })
  it('≤48 小时按小时、≤90 天按天、更长按月', () => {
    expect(bucketGranularity('2026-11-08T00:00:00+08:00', '2026-11-10T00:00:00+08:00')).toBe('hour')
    expect(bucketGranularity('2026-11-08T00:00:00+08:00', '2026-11-10T00:00:01+08:00')).toBe('day')
    expect(bucketGranularity('2026-08-12T00:00:00+08:00', '2026-11-10T00:00:00+08:00')).toBe('day')
    expect(bucketGranularity('2026-08-11T00:00:00+08:00', '2026-11-10T00:00:00+08:00')).toBe('month')
  })
  it('桶区间按上海时间完整写出', () => {
    expect(formatBucketRange('2026-11-09T16:00:00Z', '2026-11-09T17:00:00Z')).toBe('2026-11-10 00:00 — 2026-11-10 01:00（上海时间）')
  })
})

describe('口径与失败', () => {
  it('来源口径不认识时说未知，不说上报', () => {
    expect(sourceLabel('legacyInferred')).toBe('历史推导')
    expect(sourceLabel('legacyUnknown')).toBe('历史口径未确认')
    expect(sourceLabel('somethingNew')).toBe('口径未知')
  })
  it.each([
    ['not_logged_in', 'notLoggedIn'],
    ['usage_forbidden', 'forbidden'],
    ['usage_unsupported', 'unsupported'],
    ['usage_timeout', 'timeout'],
    ['usage_unavailable', 'unavailable'],
    ['usage_storage', 'storage'],
    ['usage_invalid_cursor', 'invalidCursor'],
    ['usage_invalid_response', 'invalidResponse'],
    ['usage_identity_changed', 'identityChanged'],
    ['internal', 'unknown'],
  ])('错误码 %s → %s', (code, kind) => {
    const failure = usageFailure(new Error(JSON.stringify({ code, message: '说明', details: { requestId: 'req-1' } })))
    expect(failure.kind).toBe(kind)
    expect(failure.message).toBe('说明')
    expect(failure.requestId).toBe('req-1')
  })
  it('非结构化错误按未知失败展示原文', () => {
    expect(usageFailure(new Error('boom'))).toEqual({ kind: 'unknown', message: 'boom' })
  })
})
