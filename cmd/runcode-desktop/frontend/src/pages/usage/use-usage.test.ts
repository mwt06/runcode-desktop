// @vitest-environment jsdom
import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Envelope, PassportStatus, UsageDimensionRow, UsageDimensionsPage, UsageOverview, UsageRequestsPage } from '@/core/bridge'
import { Events } from '@/core/bridge'
import { usePassportStatus } from '@/session/use-passport'
import { useUsage } from './use-usage'

const native = vi.hoisted(() => ({
  overview: vi.fn(),
  requests: vi.fn(),
  dimensions: vi.fn(),
  passportStatus: vi.fn(),
  listeners: new Map<string, (event: Envelope<unknown>) => void>(),
}))
vi.mock('@/core/bridge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/core/bridge')>()),
  getUsageOverview: native.overview,
  getUsageRequests: native.requests,
  getUsageDimensions: native.dimensions,
  passportStatus: native.passportStatus,
  onEvent: (name: string, fn: (payload: unknown) => void) => {
    native.listeners.set(name, (e) => fn(e.payload))
    return () => native.listeners.delete(name)
  },
}))

const range = { startAt: '2026-11-10T00:00:00+08:00', endAt: '2026-11-10T12:00:00+08:00', timezone: 'Asia/Shanghai' }
const overview = (scopeId: string, requestCount = '2'): UsageOverview => ({
  scopeId, queryKey: `qk-${scopeId}`, effectiveRange: range, dataAsOf: '2026-11-10T04:00:00Z',
  summary: {
    requestCount, tokenRequestCount: requestCount, inputTokens: '10', outputTokens: '1', totalTokens: '11',
    cacheHitTokens: null, cacheMissTokens: null, cacheEligibleInputTokens: null, cacheHitRate: null,
    cacheKnownRequestCount: '0', cacheEligibleRequestCount: '0', reasoningTokens: null, durationSampleCount: '0', averageDurationMs: null,
    quality: { tokenCoverage: 'complete', cacheCoverage: 'none', containsEstimates: false, containsLegacyUnknown: true, containsLegacyInferredCache: false, invalidRecordCount: '0' },
  },
  trend: [], capabilities: { providerBreakdown: false, cacheMetrics: false, requestStatus: false, ttft: false },
  empty: false, coverageStart: null, coverageGaps: [], coverageStatus: 'unknown', replicaLagSeconds: null,
})
const requestsPage = (cursor: string, hasMore = true): UsageRequestsPage => ({
  items: [], totalCount: '45', pageSize: 20, nextCursor: hasMore ? `${cursor}>next` : '', hasMore, effectiveRange: range, queryKey: 'qk',
})
const dimRow = (id: string, name = id): UsageDimensionRow => ({
  id, name, totals: overview('row').summary,
})
const dimensionsPage = (over: Partial<UsageDimensionsPage> = {}): UsageDimensionsPage => ({
  items: [], groupBy: 'model', page: 1, pageSize: 50, totalCount: '0', hasMore: false, effectiveRange: range, queryKey: 'qk',
  ...over,
})

type Usage = ReturnType<typeof useUsage>
let root: Root
let usage!: Usage

function mount(identity: string, tenant: string) {
  function Harness({ identity, tenant }: { identity: string; tenant: string }) {
    usage = useUsage(identity, tenant)
    return null
  }
  return act(async () => root.render(createElement(Harness, { identity, tenant })))
}

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  localStorage.clear()
  native.overview.mockReset()
  native.requests.mockReset().mockImplementation(async (q: { cursor?: string }) => requestsPage(q.cursor ?? 'p1'))
  native.dimensions.mockReset().mockImplementation(async () => dimensionsPage())
  native.listeners.clear()
  Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
  root = createRoot(document.createElement('div'))
})
afterEach(async () => {
  await act(async () => root.unmount())
  vi.useRealTimers()
})

describe('useUsage', () => {
  it('logs, dimensions and candidates reuse the overview snapshot including sequence zero', async () => {
    native.overview.mockResolvedValue({ ...overview('A'), snapshotSeq: '0' })
    await mount('user-1', 'tenant-a')
    expect(native.requests).toHaveBeenCalledWith(expect.objectContaining({ query: expect.objectContaining({ snapshotSeq: '0' }) }))
    expect(native.dimensions).toHaveBeenCalledWith(expect.objectContaining({ query: expect.objectContaining({ snapshotSeq: '0' }) }))
    await act(async () => usage.searchOptions('model', 'needle'))
    expect(native.dimensions).toHaveBeenLastCalledWith(expect.objectContaining({ query: expect.objectContaining({ snapshotSeq: '0' }) }))
  })

  it('a newer snapshot flags status changes even when request and token totals stay unchanged', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    native.overview.mockResolvedValue({ ...overview('A'), snapshotSeq: '10' })
    await mount('user-1', 'tenant-a')
    await act(async () => usage.nextPage())
    native.overview.mockResolvedValue({ ...overview('A'), snapshotSeq: '11' })
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(usage.hasUpdate).toBe(true)
    expect(usage.overview?.snapshotSeq).toBe('10')
  })

  it('rejectsStaleIdentityAndTenantResponses', async () => {
    let releaseA!: (v: UsageOverview) => void
    native.overview.mockImplementation(async (q: { tenantId: string }) =>
      q.tenantId === 'tenant-a' ? new Promise<UsageOverview>((yes) => { releaseA = yes }) : overview('B'))
    await mount('user-1', 'tenant-a')
    expect(usage.loading).toBe(true)
    await mount('user-1', 'tenant-b')
    expect(usage.overview?.scopeId).toBe('B')
    await act(async () => releaseA(overview('A')))
    expect(usage.overview?.scopeId).toBe('B')

    // 登出：数据立即清空，迟到的旧身份响应不能回填。
    let releaseB!: (v: UsageOverview) => void
    native.overview.mockImplementation(() => new Promise<UsageOverview>((yes) => { releaseB = yes }))
    await act(async () => usage.refresh())
    await mount('', 'tenant-b')
    expect(usage.overview).toBeNull()
    expect(usage.requests).toBeNull()
    await act(async () => releaseB(overview('B-late')))
    expect(usage.overview).toBeNull()
    expect(native.requests.mock.calls.every(([q]) => q.query.tenantId !== '')).toBe(true)
  })

  it('a failed refresh keeps the last data but marks it stale', async () => {
    native.overview.mockResolvedValueOnce(overview('A'))
    await mount('user-1', 'tenant-a')
    native.overview.mockRejectedValueOnce(new Error(JSON.stringify({ code: 'usage_timeout', message: '统计超时' })))
    await act(async () => usage.refresh())
    expect(usage.overview?.scopeId).toBe('A')
    expect(usage.stale).toBe(true)
    expect(usage.error?.kind).toBe('timeout')
  })

  it('freezesAllPanelsBeyondFirstPage', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    native.overview.mockResolvedValue(overview('A', '2'))
    await mount('user-1', 'tenant-a')
    expect(usage.requestPage).toBe(1)
    await act(async () => usage.nextPage())
    expect(usage.requestPage).toBe(2)
    expect(native.requests).toHaveBeenLastCalledWith({ query: { tenantId: 'tenant-a', effectiveRange: range }, pageSize: 20, cursor: 'p1>next' })

    // 第二页上自动刷新只探测：有新数据也不替换任何区域。
    native.overview.mockResolvedValue(overview('A-new', '3'))
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(usage.overview?.scopeId).toBe('A')
    expect(usage.requestPage).toBe(2)
    expect(usage.hasUpdate).toBe(true)

    await act(async () => usage.prevPage())
    expect(native.requests).toHaveBeenLastCalledWith({ query: { tenantId: 'tenant-a', effectiveRange: range }, pageSize: 20 })
    expect(usage.requestPage).toBe(1)

    // 点击新数据提示：统一回第一页、清空游标、整套替换。
    await act(async () => usage.nextPage())
    await act(async () => usage.refresh())
    expect(usage.requestPage).toBe(1)
    expect(usage.overview?.scopeId).toBe('A-new')
    expect(usage.hasUpdate).toBe(false)
  })

  it('page size change returns to page one on the same frozen range', async () => {
    native.overview.mockResolvedValue(overview('A'))
    await mount('user-1', 'tenant-a')
    await act(async () => usage.nextPage())
    const overviewCalls = native.overview.mock.calls.length
    await act(async () => usage.setPageSize(50))
    expect(usage.requestPage).toBe(1)
    expect(native.overview.mock.calls.length).toBe(overviewCalls)
    expect(native.requests).toHaveBeenLastCalledWith({ query: { tenantId: 'tenant-a', effectiveRange: range }, pageSize: 50 })
  })

  it('visibleAutoRefreshDoesNotOverlap', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    native.overview.mockResolvedValueOnce(overview('A'))
    await mount('user-1', 'tenant-a')
    expect(native.overview).toHaveBeenCalledTimes(1)

    // 一次刷新挂住时，后面的定时点不叠加新请求。
    let release!: (v: UsageOverview) => void
    native.overview.mockImplementation(() => new Promise<UsageOverview>((yes) => { release = yes }))
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(native.overview).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(180_000) })
    expect(native.overview).toHaveBeenCalledTimes(2)
    await act(async () => release(overview('A2')))

    // 窗口不可见时不扫描；回到可见后才重新计时。
    native.overview.mockResolvedValue(overview('A3'))
    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
    const before = native.overview.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(300_000) })
    expect(native.overview.mock.calls.length).toBe(before)
    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(native.overview.mock.calls.length).toBe(before + 1)

    // 关闭自动刷新后不再扫描。
    await act(async () => usage.toggleAutoRefresh())
    await act(async () => { await vi.advanceTimersByTimeAsync(300_000) })
    expect(native.overview.mock.calls.length).toBe(before + 1)
  })

  it('model filter change clears nothing else but reloads with the filter; provider change clears the model', async () => {
    native.overview.mockResolvedValue({ ...overview('A'), capabilities: { providerBreakdown: true, cacheMetrics: false, requestStatus: false, ttft: false } })
    await mount('user-1', 'tenant-a')
    await act(async () => usage.setModel('qwen-max'))
    expect(native.overview).toHaveBeenLastCalledWith(expect.objectContaining({ model: 'qwen-max' }))
    await act(async () => usage.setProvider('dashscope'))
    const last = native.overview.mock.calls[native.overview.mock.calls.length - 1][0]
    expect(last.providerId).toBe('dashscope')
    expect(last.model).toBeUndefined()
  })

  it('候选默认 pageSize=50，hasMore 时可加载第 51 项；无更多时不再请求', async () => {
    const page1 = Array.from({ length: 50 }, (_, i) => dimRow(`m-${i + 1}`))
    native.overview.mockResolvedValue(overview('A'))
    native.dimensions.mockImplementation(async (q: { groupBy: string; page: number; pageSize: number; search?: string }) => {
      if (q.groupBy !== 'model') return dimensionsPage({ groupBy: q.groupBy })
      if (q.page === 1) {
        expect(q.pageSize).toBe(50)
        return dimensionsPage({ items: page1, page: 1, pageSize: 50, totalCount: '101', hasMore: true })
      }
      return dimensionsPage({
        items: [dimRow('m-51'), dimRow('m-52')],
        page: 2, pageSize: 50, totalCount: '101', hasMore: false, groupBy: 'model',
      })
    })
    await mount('user-1', 'tenant-a')
    expect(usage.modelOptions).toHaveLength(50)
    expect(usage.modelCandidate.hasMore).toBe(true)
    expect(usage.modelCandidate.pageSize).toBe(50)
    expect(usage.modelCandidate.totalCount).toBe('101')

    await act(async () => usage.loadMoreOptions('model'))
    expect(usage.modelOptions.map((m) => m.id)).toContain('m-51')
    expect(usage.modelOptions).toHaveLength(52)
    expect(usage.modelCandidate.page).toBe(2)
    expect(usage.modelCandidate.hasMore).toBe(false)

    const calls = native.dimensions.mock.calls.length
    await act(async () => usage.loadMoreOptions('model'))
    expect(native.dimensions.mock.calls.length).toBe(calls)
  })

  it('超过 100 项可服务端搜索；跨页按 ID 去重；选中项不在首屏仍保留', async () => {
    native.overview.mockResolvedValue(overview('A'))
    native.dimensions.mockImplementation(async (q: { groupBy: string; page: number; search?: string }) => {
      if (q.groupBy !== 'model') return dimensionsPage({ groupBy: q.groupBy })
      if (q.search === 'needle') {
        return dimensionsPage({
          items: [dimRow('needle-101', 'needle-101-long-name')],
          page: 1, pageSize: 50, totalCount: '1', hasMore: false, groupBy: 'model',
        })
      }
      if (q.page === 2) {
        return dimensionsPage({
          items: [dimRow('m-1'), dimRow('m-51')],
          page: 2, pageSize: 50, totalCount: '120', hasMore: true, groupBy: 'model',
        })
      }
      return dimensionsPage({
        items: Array.from({ length: 50 }, (_, i) => dimRow(`m-${i + 1}`)),
        page: 1, pageSize: 50, totalCount: '120', hasMore: true, groupBy: 'model',
      })
    })
    await mount('user-1', 'tenant-a')
    await act(async () => usage.setModel('kept-selected'))
    expect(usage.filters.model).toBe('kept-selected')

    await act(async () => usage.loadMoreOptions('model'))
    expect(usage.modelOptions.filter((m) => m.id === 'm-1')).toHaveLength(1)
    expect(usage.modelOptions.map((m) => m.id)).toContain('m-51')

    await act(async () => usage.searchOptions('model', 'needle'))
    expect(native.dimensions).toHaveBeenCalledWith(expect.objectContaining({
      groupBy: 'model', search: 'needle', page: 1, pageSize: 50,
    }))
    expect(usage.modelOptions.map((m) => m.id)).toEqual(['needle-101'])
    expect(usage.modelCandidate.search).toBe('needle')
    expect(usage.filters.model).toBe('kept-selected')
  })

  it('候选读取失败保留已选项与既有候选，不把失败伪装成没有更多；忙时忽略重复操作', async () => {
    native.overview.mockResolvedValue(overview('A'))
    native.dimensions.mockResolvedValue(dimensionsPage({
      items: [dimRow('m-1')], page: 1, pageSize: 50, totalCount: '80', hasMore: true, groupBy: 'model',
    }))
    await mount('user-1', 'tenant-a')
    await act(async () => usage.setModel('m-1'))
    expect(usage.modelCandidate.hasMore).toBe(true)

    native.dimensions.mockRejectedValueOnce(new Error(JSON.stringify({ code: 'usage_timeout', message: '统计超时' })))
    await act(async () => usage.loadMoreOptions('model'))
    expect(usage.modelOptions.map((m) => m.id)).toEqual(['m-1'])
    expect(usage.modelCandidate.hasMore).toBe(true)
    expect(usage.filters.model).toBe('m-1')
    expect(usage.stale).toBe(true)

    let release!: (v: UsageDimensionsPage) => void
    native.dimensions.mockImplementation(() => new Promise<UsageDimensionsPage>((yes) => { release = yes }))
    await act(async () => { void usage.searchOptions('model', 'x') })
    const during = native.dimensions.mock.calls.length
    await act(async () => { void usage.loadMoreOptions('model') })
    expect(native.dimensions.mock.calls.length).toBe(during)
    await act(async () => release(dimensionsPage({ items: [dimRow('x-1')], page: 1, hasMore: false, groupBy: 'model' })))
  })

  it('换租户清空候选；providerBreakdown=false 不查供应商候选；旧搜索响应丢弃', async () => {
    native.overview.mockImplementation(async (q: { tenantId: string }) => ({
      ...overview(q.tenantId),
      capabilities: {
        providerBreakdown: q.tenantId === 'tenant-b',
        cacheMetrics: false, requestStatus: false, ttft: false,
      },
    }))
    native.dimensions.mockResolvedValue(dimensionsPage({
      items: [dimRow('m-a')], page: 1, pageSize: 50, totalCount: '1', hasMore: false, groupBy: 'model',
    }))
    await mount('user-1', 'tenant-a')
    expect(usage.modelOptions.map((m) => m.id)).toEqual(['m-a'])
    expect(usage.providerOptions).toEqual([])
    expect(native.dimensions.mock.calls.every(([q]) => q.groupBy !== 'provider')).toBe(true)

    let releaseSearch!: (v: UsageDimensionsPage) => void
    native.dimensions.mockImplementation((q: { tenantId?: string; groupBy: string; search?: string }) => {
      if (q.search === 'stale') return new Promise<UsageDimensionsPage>((yes) => { releaseSearch = yes })
      if (q.groupBy === 'provider') {
        return Promise.resolve(dimensionsPage({
          items: [dimRow('prov-1', '供应商一')], groupBy: 'provider', page: 1, pageSize: 50, totalCount: '1', hasMore: false,
        }))
      }
      return Promise.resolve(dimensionsPage({
        items: [dimRow('m-b')], groupBy: 'model', page: 1, pageSize: 50, totalCount: '1', hasMore: false,
      }))
    })
    await act(async () => { void usage.searchOptions('model', 'stale') })
    await mount('user-1', 'tenant-b')
    expect(usage.modelOptions.map((m) => m.id)).toEqual(['m-b'])
    expect(usage.providerOptions.map((p) => p.id)).toEqual(['prov-1'])
    expect(usage.modelCandidate.search).toBe('')
    await act(async () => releaseSearch(dimensionsPage({
      items: [dimRow('should-not-appear')], page: 1, hasMore: false, groupBy: 'model',
    })))
    expect(usage.modelOptions.map((m) => m.id)).toEqual(['m-b'])
  })
})

describe('usePassportStatus', () => {
  it('doesNotReplaceNewLogoutWithInitialStatus', async () => {
    let resolveInitial!: (s: PassportStatus) => void
    native.passportStatus.mockImplementation(() => new Promise<PassportStatus>((yes) => { resolveInitial = yes }))
    let status: PassportStatus | null = null
    function Harness() { status = usePassportStatus(); return null }
    await act(async () => root.render(createElement(Harness)))
    await act(async () => native.listeners.get(Events.PassportChanged)?.({ payload: { loggedIn: false } } as Envelope<unknown>))
    await act(async () => resolveInitial({ loggedIn: true, userId: 'old-user' }))
    expect(status).toEqual({ loggedIn: false })
  })
})
