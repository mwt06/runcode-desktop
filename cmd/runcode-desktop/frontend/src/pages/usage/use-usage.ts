// useUsage 管用量页的数据生命周期：按「身份 + 租户」分代、总览先固定范围再读日志与分组、
// 整套数据一次性替换、第二页以后冻结、可见时 60 秒自动刷新且从不叠加请求。
//
// 分页有两套，互不覆盖：
//   1) 汇总表 dimensionPage —— 当前筛选下的模型／供应商分组表翻页；
//   2) 筛选候选 modelCandidate／providerCandidate —— 下拉可选 ID 的搜索与续载，
//      pageSize 固定 50，独立于汇总表；选定才改 filters，搜索只改候选列表。
//
// 所有读取串行发出：本机 SQLite 查询共享一个快照水位，避免总览、日志和维度各读一份。
// identityKey 只用来丢弃旧响应，不是授权凭证——身份由桌面端从当前登录态推导。
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  getUsageDimensions, getUsageOverview, getUsageRequests,
  type ResolvedUsageQuery, type UsageDimensionRow, type UsageDimensionsPage, type UsageOverview, type UsageRequestsPage,
} from '@/core/bridge'
import { usePersistentBool } from '@/hooks/use-persistent-state'
import { buildUsageQuery, usageFailure, type UsageFailure, type UsageFilters, type UsageRangeSelection } from '@/core/usage'

export const USAGE_AUTO_REFRESH_MS = 60_000
export type UsagePageSize = 20 | 50 | 100
export type UsageGroupBy = 'model' | 'provider'
/** 汇总表与筛选候选共用 dimensions 接口，但候选页码独立；默认每页 50。 */
const DIMENSION_PAGE_SIZE = 50
const OPTION_PAGE_SIZE = 50

/** 筛选下拉的一维候选状态：与汇总表 dimensionPage 分开记。 */
export interface UsageOptionCandidate {
  search: string
  page: number
  pageSize: typeof OPTION_PAGE_SIZE
  hasMore: boolean
  totalCount: string
}

const EMPTY_CANDIDATE: UsageOptionCandidate = {
  search: '', page: 1, pageSize: OPTION_PAGE_SIZE, hasMore: false, totalCount: '0',
}

// Snapshot 是一次「刷新」得到的整套数据：卡片、趋势、日志、分组与筛选项来自同一个
// effectiveRange，只能整体替换。cursors[i] 是读取第 i+1 页用的游标（第一页为空串）。
interface Snapshot {
  resolved: ResolvedUsageQuery
  overview: UsageOverview
  requests: UsageRequestsPage
  cursors: string[]
  dimensions: UsageDimensionsPage
  dimensionPage: number
  modelOptions: UsageDimensionRow[]
  providerOptions: UsageDimensionRow[]
  modelCandidate: UsageOptionCandidate
  providerCandidate: UsageOptionCandidate
}

interface LoadParams {
  tenantId: string
  selection: UsageRangeSelection
  filters: UsageFilters
  pageSize: UsagePageSize
  groupBy: UsageGroupBy
}

function resolvedFrom(tenantId: string, overview: UsageOverview, filters: UsageFilters): ResolvedUsageQuery {
  const q: ResolvedUsageQuery = { tenantId, effectiveRange: overview.effectiveRange }
  if (overview.snapshotSeq !== undefined) q.snapshotSeq = overview.snapshotSeq
  if (filters.providerId) q.providerId = filters.providerId
  if (filters.model) q.model = filters.model
  return q
}

const withoutFilter = (q: ResolvedUsageQuery, drop: ('model' | 'providerId')[]): ResolvedUsageQuery => {
  const copy = { ...q }
  for (const key of drop) delete copy[key]
  return copy
}

function requestsQuery(resolved: ResolvedUsageQuery, pageSize: UsagePageSize, cursor: string) {
  return cursor ? { query: resolved, pageSize, cursor } : { query: resolved, pageSize }
}

function candidateFrom(page: UsageDimensionsPage, search: string): UsageOptionCandidate {
  return {
    search,
    page: page.page,
    pageSize: OPTION_PAGE_SIZE,
    hasMore: page.hasMore,
    totalCount: page.totalCount,
  }
}

function sameRange(a: ResolvedUsageQuery['effectiveRange'], b: ResolvedUsageQuery['effectiveRange']): boolean {
  return a.startAt === b.startAt && a.endAt === b.endAt && a.timezone === b.timezone
}

/** 响应须对上本次候选查询的 groupBy／页码／搜索与冻结范围，否则丢弃（防串页／串代）。 */
function acceptOptionPage(
  page: UsageDimensionsPage,
  expected: { groupBy: UsageGroupBy; page: number; range: ResolvedUsageQuery['effectiveRange'] },
): boolean {
  // search 以本次请求为准记入 candidate；dimensions 响应不回显 search 字段。
  return page.groupBy === expected.groupBy
    && page.page === expected.page
    && sameRange(page.effectiveRange, expected.range)
}

function mergeUnique(prev: UsageDimensionRow[], next: UsageDimensionRow[]): UsageDimensionRow[] {
  const seen = new Set(prev.map((r) => r.id))
  const out = prev.slice()
  for (const row of next) {
    if (seen.has(row.id)) continue
    seen.add(row.id)
    out.push(row)
  }
  return out
}

async function fetchOptions(
  resolved: ResolvedUsageQuery,
  groupBy: UsageGroupBy,
  search: string,
  page: number,
): Promise<UsageDimensionsPage> {
  const query = groupBy === 'model'
    ? withoutFilter(resolved, ['model'])
    : withoutFilter(resolved, ['model', 'providerId'])
  const req: Parameters<typeof getUsageDimensions>[0] = {
    query, groupBy, page, pageSize: OPTION_PAGE_SIZE,
  }
  const trimmed = search.trim()
  if (trimmed) req.search = trimmed
  return getUsageDimensions(req)
}

// loadSnapshot 读取一整套数据。模型下拉不应用当前模型筛选、供应商下拉不应用供应商与
// 模型筛选（设计 8.3）。分组表首屏若恰好是无模型筛选的 model 分组，可复用为模型候选，
// 但仍必须带上 hasMore／totalCount 等元数据，不能只抄 items。
async function loadSnapshot(p: LoadParams): Promise<Snapshot> {
  const overview = await getUsageOverview(buildUsageQuery(p.tenantId, p.selection, p.filters))
  const resolved = resolvedFrom(p.tenantId, overview, p.filters)
  const providerBreakdown = overview.capabilities.providerBreakdown
  const groupBy: UsageGroupBy = p.groupBy === 'provider' && providerBreakdown ? 'provider' : 'model'
  const requests = await getUsageRequests(requestsQuery(resolved, p.pageSize, ''))
  const dimensions = await getUsageDimensions({ query: resolved, groupBy, page: 1, pageSize: DIMENSION_PAGE_SIZE })

  let modelPage: UsageDimensionsPage
  if (groupBy === 'model' && !p.filters.model) {
    modelPage = dimensions
  } else {
    modelPage = await fetchOptions(resolved, 'model', '', 1)
  }
  const modelOptions = modelPage.items ?? []
  const modelCandidate = candidateFrom(modelPage, '')

  let providerOptions: UsageDimensionRow[] = []
  let providerCandidate = EMPTY_CANDIDATE
  if (providerBreakdown) {
    const providerPage = await fetchOptions(resolved, 'provider', '', 1)
    providerOptions = providerPage.items ?? []
    providerCandidate = candidateFrom(providerPage, '')
  }

  return {
    resolved, overview, requests, cursors: [''], dimensions, dimensionPage: 1,
    modelOptions, providerOptions, modelCandidate, providerCandidate,
  }
}

export function useUsage(identityKey: string, tenantId: string) {
  const scopeKey = identityKey && tenantId ? `${identityKey}\u0000${tenantId}` : ''
  const [selection, setSelection] = useState<UsageRangeSelection>({ kind: 'today' })
  // 筛选按「身份 + 租户」记：换租户后旧租户的模型名不能带过去。
  const [filterState, setFilterState] = useState<{ key: string; filters: UsageFilters }>({ key: '', filters: {} })
  const filters = filterState.key === scopeKey ? filterState.filters : {}
  const [pageSize, setPageSizeState] = useState<UsagePageSize>(20)
  const [groupBy, setGroupByState] = useState<UsageGroupBy>('model')
  const [snap, setSnap] = useState<Snapshot | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<UsageFailure | null>(null)
  const [refreshedAt, setRefreshedAt] = useState<Date | null>(null)
  const [hasUpdate, setHasUpdate] = useState(false)
  const [nonce, setNonce] = useState(0)
  const [autoRefresh, toggleAutoRefresh] = usePersistentBool('usage.autoRefresh', true)

  // gen 每次换身份 / 租户 / 范围 / 筛选或手动刷新时加一；返回时代际不符的响应一律丢弃。
  const gen = useRef(0)
  // busy 保证同一时刻只有一趟读取（含自动刷新的探测与候选搜索／续载），不叠加请求。
  const busy = useRef(false)
  const latest = useRef({ snap, tenantId, selection, filters, pageSize, groupBy })
  latest.current = { snap, tenantId, selection, filters, pageSize, groupBy }

  const selectionKey = JSON.stringify(selection)
  const filtersKey = JSON.stringify(filters)

  // 身份或租户一变就清空全部数据并作废在途请求；登出时不再发起任何读取。
  useEffect(() => {
    gen.current++
    busy.current = false
    setSnap(null)
    setError(null)
    setHasUpdate(false)
    setRefreshedAt(null)
    setLoading(false)
  }, [scopeKey])

  useEffect(() => {
    if (!scopeKey) return
    const my = ++gen.current
    const { tenantId: tenant, selection: sel, filters: f, pageSize: size, groupBy: g } = latest.current
    busy.current = true
    setLoading(true)
    loadSnapshot({ tenantId: tenant, selection: sel, filters: f, pageSize: size, groupBy: g })
      .then((next) => {
        if (gen.current !== my) return
        setSnap(next)
        setError(null)
        setHasUpdate(false)
        setRefreshedAt(new Date())
      })
      .catch((e: unknown) => {
        if (gen.current !== my) return
        const failure = usageFailure(e)
        // 账号在读取途中变了：身份那条 effect 会清场，这里不再报错。
        if (failure.kind !== 'identityChanged') setError(failure)
      })
      .finally(() => {
        if (gen.current !== my) return
        busy.current = false
        setLoading(false)
      })
  }, [scopeKey, selectionKey, filtersKey, nonce])

  // partial 在已固定的范围上做一次局部读取（翻页、每页条数、分组、候选搜索／续载）。
  // 它不推进代际：换范围或刷新会让它的结果作废；busy 期间直接忽略，避免并发改同一份快照。
  const partial = useCallback(async (run: (s: Snapshot) => Promise<Snapshot>) => {
    const current = latest.current.snap
    if (!current || busy.current) return
    const my = gen.current
    busy.current = true
    setLoading(true)
    try {
      const next = await run(current)
      if (gen.current !== my) return
      setSnap(next)
      setError(null)
    } catch (e: unknown) {
      if (gen.current !== my) return
      const failure = usageFailure(e)
      if (failure.kind !== 'identityChanged') setError(failure)
    } finally {
      if (gen.current === my) {
        busy.current = false
        setLoading(false)
      }
    }
  }, [])

  const nextPage = useCallback(() => partial(async (s) => {
    if (!s.requests.hasMore || !s.requests.nextCursor) return s
    const cursor = s.requests.nextCursor
    const requests = await getUsageRequests(requestsQuery(s.resolved, latest.current.pageSize, cursor))
    return { ...s, requests, cursors: [...s.cursors, cursor] }
  }), [partial])

  const prevPage = useCallback(() => partial(async (s) => {
    if (s.cursors.length <= 1) return s
    const cursors = s.cursors.slice(0, -1)
    const requests = await getUsageRequests(requestsQuery(s.resolved, latest.current.pageSize, cursors[cursors.length - 1]))
    return { ...s, requests, cursors }
  }), [partial])

  const setPageSize = useCallback((size: UsagePageSize) => {
    setPageSizeState(size)
    void partial(async (s) => {
      const requests = await getUsageRequests(requestsQuery(s.resolved, size, ''))
      return { ...s, requests, cursors: [''] }
    })
  }, [partial])

  const loadDimensions = useCallback((g: UsageGroupBy, page: number) => partial(async (s) => {
    const dimensions = await getUsageDimensions({ query: s.resolved, groupBy: g, page, pageSize: DIMENSION_PAGE_SIZE })
    return { ...s, dimensions, dimensionPage: page }
  }), [partial])

  const setGroupBy = useCallback((g: UsageGroupBy) => {
    setGroupByState(g)
    void loadDimensions(g, 1)
  }, [loadDimensions])

  const setDimensionPage = useCallback((page: number) => {
    void loadDimensions(latest.current.snap?.dimensions.groupBy === 'provider' ? 'provider' : 'model', page)
  }, [loadDimensions])

  // 搜索只重置候选页，不改 filters，也不碰汇总表页码。
  const searchOptions = useCallback((g: UsageGroupBy, text: string) => partial(async (s) => {
    if (g === 'provider' && !s.overview.capabilities.providerBreakdown) return s
    const search = text.trim()
    const page = await fetchOptions(s.resolved, g, search, 1)
    if (!acceptOptionPage(page, { groupBy: g, page: 1, range: s.resolved.effectiveRange })) return s
    const candidate = candidateFrom(page, search)
    if (g === 'provider') {
      return { ...s, providerOptions: page.items ?? [], providerCandidate: candidate }
    }
    return { ...s, modelOptions: page.items ?? [], modelCandidate: candidate }
  }), [partial])

  // 续载追加并按 ID 去重；hasMore=false 时不发请求。失败走 partial 的 catch，保留原候选与 hasMore。
  const loadMoreOptions = useCallback((g: UsageGroupBy) => partial(async (s) => {
    if (g === 'provider' && !s.overview.capabilities.providerBreakdown) return s
    const state = g === 'provider' ? s.providerCandidate : s.modelCandidate
    if (!state.hasMore) return s
    const nextPage = state.page + 1
    const page = await fetchOptions(s.resolved, g, state.search, nextPage)
    if (!acceptOptionPage(page, {
      groupBy: g, page: nextPage, range: s.resolved.effectiveRange,
    })) return s
    const candidate = candidateFrom(page, state.search)
    if (g === 'provider') {
      return {
        ...s,
        providerOptions: mergeUnique(s.providerOptions, page.items ?? []),
        providerCandidate: candidate,
      }
    }
    return {
      ...s,
      modelOptions: mergeUnique(s.modelOptions, page.items ?? []),
      modelCandidate: candidate,
    }
  }), [partial])

  const refresh = useCallback(() => setNonce((n) => n + 1), [])

  const applySelection = useCallback((sel: UsageRangeSelection) => {
    setSelection(sel)
    // 选同一个相对范围也要重新固定结束时刻。
    setNonce((n) => n + 1)
  }, [])

  const setModel = useCallback((model: string) => {
    setFilterState({ key: scopeKey, filters: { ...latest.current.filters, model: model || undefined } })
  }, [scopeKey])

  // 供应商一变，旧模型可能不属于新供应商，按设计清掉模型筛选。
  const setProvider = useCallback((providerId: string) => {
    setFilterState({ key: scopeKey, filters: { providerId: providerId || undefined } })
  }, [scopeKey])

  // 第二页以后的自动刷新只探测：拿当前范围重新读一次总览，有变化就亮提示，不替换数据。
  const probe = useCallback(async () => {
    const { snap: s, tenantId: tenant, selection: sel, filters: f } = latest.current
    if (!s || busy.current) return
    const my = gen.current
    busy.current = true
    try {
      const o = await getUsageOverview(buildUsageQuery(tenant, sel, f))
      if (gen.current !== my) return
      const before = s.overview.summary
      if (o.snapshotSeq !== s.overview.snapshotSeq || o.summary.requestCount !== before.requestCount || o.summary.totalTokens !== before.totalTokens) setHasUpdate(true)
    } catch {
      /* 探测失败不打扰：用户仍在看冻结的那一页 */
    } finally {
      if (gen.current === my) busy.current = false
    }
  }, [])

  // 自动刷新：仅在开启、已有身份与租户、窗口可见时计时；每次触发前若有读取在途就跳过，
  // 所以永远不会叠加。刷新成功（refreshedAt 变化）后重新计时。
  useEffect(() => {
    if (!autoRefresh || !scopeKey) return
    let timer: ReturnType<typeof setTimeout> | undefined
    const schedule = () => {
      clearTimeout(timer)
      if (document.visibilityState === 'visible') timer = setTimeout(tick, USAGE_AUTO_REFRESH_MS)
    }
    const tick = () => {
      if (!busy.current) {
        if ((latest.current.snap?.cursors.length ?? 1) > 1) void probe()
        else setNonce((n) => n + 1)
      }
      schedule()
    }
    const onVisibility = () => {
      if (document.visibilityState === 'visible') schedule()
      else clearTimeout(timer)
    }
    schedule()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      clearTimeout(timer)
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [autoRefresh, scopeKey, refreshedAt, probe])

  return {
    selection, setSelection: applySelection,
    filters, setModel, setProvider,
    pageSize, setPageSize,
    groupBy, setGroupBy, setDimensionPage,
    overview: snap?.overview ?? null,
    requests: snap?.requests ?? null,
    requestPage: snap?.cursors.length ?? 0,
    dimensions: snap?.dimensions ?? null,
    dimensionPage: snap?.dimensionPage ?? 1,
    modelOptions: snap?.modelOptions ?? [],
    providerOptions: snap?.providerOptions ?? [],
    modelCandidate: snap?.modelCandidate ?? EMPTY_CANDIDATE,
    providerCandidate: snap?.providerCandidate ?? EMPTY_CANDIDATE,
    searchOptions, loadMoreOptions,
    loading, error, stale: !!error && !!snap,
    refreshedAt, hasUpdate,
    refresh, nextPage, prevPage,
    autoRefresh, toggleAutoRefresh,
  }
}
