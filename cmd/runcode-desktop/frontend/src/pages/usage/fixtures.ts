// 用量页组件测试与 ?preview 共用的合成数据（不代表任何真实用户）。数值沿用设计第 4 节的
// 校验例：两次调用合计输入 10000、输出 1000、命中 1700、命中率 17.0%。
import type { UsageBucket, UsageOverview, UsageRequestRow, UsageRequestsPage, UsageTotals } from '@/core/bridge'

export const FIXTURE_RANGE = { startAt: '2026-11-10T00:00:00+08:00', endAt: '2026-11-10T12:00:00+08:00', timezone: 'Asia/Shanghai' }

export function fixtureTotals(over: Partial<UsageTotals> = {}): UsageTotals {
  return {
    requestCount: '2', tokenRequestCount: '2',
    inputTokens: '10000', outputTokens: '1000', totalTokens: '11000',
    cacheHitTokens: '1700', cacheMissTokens: '8300', cacheEligibleInputTokens: '10000', cacheHitRate: 0.17,
    cacheKnownRequestCount: '2', cacheEligibleRequestCount: '2',
    reasoningTokens: null, durationSampleCount: '2', averageDurationMs: 1500,
    quality: { tokenCoverage: 'complete', cacheCoverage: 'complete', containsEstimates: false, containsLegacyUnknown: true, containsLegacyInferredCache: true, invalidRecordCount: '0' },
    ...over,
  }
}

export function fixtureBucket(startAt: string, endAt: string, over: Partial<UsageBucket> = {}): UsageBucket {
  return {
    startAt, endAt, requestCount: '0', inputTokens: '0', outputTokens: '0', totalTokens: '0',
    cacheHitTokens: null, cacheEligibleInputTokens: null, coverage: 'known', ...over,
  }
}

export function fixtureOverview(over: Partial<UsageOverview> = {}): UsageOverview {
  return {
    scopeId: 'scope', queryKey: 'qk', effectiveRange: FIXTURE_RANGE, dataAsOf: '2026-11-10T03:59:30Z',
    summary: fixtureTotals(),
    trend: [
      fixtureBucket('2026-11-10T00:00:00+08:00', '2026-11-10T06:00:00+08:00'),
      fixtureBucket('2026-11-10T06:00:00+08:00', '2026-11-10T12:00:00+08:00', { requestCount: '2', inputTokens: '10000', outputTokens: '1000', totalTokens: '11000', cacheHitTokens: '1700', cacheEligibleInputTokens: '10000' }),
    ],
    capabilities: { providerBreakdown: false, cacheMetrics: true, requestStatus: false, ttft: false },
    empty: false, coverageStart: null, coverageGaps: [], coverageStatus: 'unknown', replicaLagSeconds: null,
    ...over,
  }
}

export function fixtureRow(over: Partial<UsageRequestRow> = {}): UsageRequestRow {
  return {
    id: 'log-1', recordedAt: '2026-11-10T03:00:00Z', startedAt: null, model: 'qwen-max',
    providerId: null, providerName: null, meterKind: 'tokens', usageQuantity: null, usageUnit: null,
    inputTokens: '1000', outputTokens: '200', cacheHitTokens: '800', cacheMissTokens: '200', reasoningTokens: null,
    durationMs: 1200,
    quality: { tokenSource: 'legacyUnknown', cacheSource: 'legacyInferred', reasoningSource: 'unavailable' },
    ...over,
  }
}

export function fixtureRequests(over: Partial<UsageRequestsPage> = {}): UsageRequestsPage {
  return {
    items: [fixtureRow()], totalCount: '21', pageSize: 20, nextCursor: 'c1', hasMore: true,
    effectiveRange: FIXTURE_RANGE, queryKey: 'qk', ...over,
  }
}
