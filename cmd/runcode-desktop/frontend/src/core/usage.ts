// 用量统计（USAGE-001）的前端纯逻辑：上海时间范围、精确整数显示、图表缩放、口径文案与
// 失败分类。统计口径由 Bridge 决定，这里只负责「如实显示」——null 永远是「—」不是 0，
// 计数与 Token 全程用 BigInt，只有图表在缩放之后才转成 Number。
import { ErrCodes, errText, type UsageQuery } from './bridge'

export const USAGE_TIMEZONE = 'Asia/Shanghai'
// Token 计量记录的 meterKind；其他单位（图片张数、语音秒数、页数）的 Token 列显示「不适用」。
export const TOKENS_METER = 'tokens'

export type UsageRangeKind = 'today' | 'last24h' | 'last7d' | 'last14d' | 'last30d' | 'all' | 'custom'

export const USAGE_RANGES: { kind: Exclude<UsageRangeKind, 'custom'>; label: string }[] = [
  { kind: 'today', label: '当天' },
  { kind: 'last24h', label: '近24小时' },
  { kind: 'last7d', label: '近7天' },
  { kind: 'last14d', label: '近14天' },
  { kind: 'last30d', label: '近30天' },
  { kind: 'all', label: '全部' },
]

// UsageRangeSelection 是已应用的范围。custom 的起止是带 +08:00 偏移的 ISO 字符串；
// followNow 时没有 endAt，每次刷新由 Bridge 固定成读取时刻。
export interface UsageRangeSelection {
  kind: UsageRangeKind
  startAt?: string
  endAt?: string
  followNow?: boolean
}

export interface UsageFilters {
  model?: string
  providerId?: string
}

// ---- 上海时间（固定 UTC+8，无夏令时）----------------------------------------------

const SH_OFFSET_MS = 8 * 3600_000
const HOUR_MS = 3600_000
const DAY_MS = 24 * HOUR_MS

const pad = (n: number, width = 2) => String(n).padStart(width, '0')

function shanghaiFields(d: Date) {
  const s = new Date(d.getTime() + SH_OFFSET_MS)
  return { y: s.getUTCFullYear(), mo: s.getUTCMonth() + 1, d: s.getUTCDate(), h: s.getUTCHours(), mi: s.getUTCMinutes(), s: s.getUTCSeconds() }
}

// shanghaiDayStart 返回上海时间 daysBack 天前那一天的 00:00。
export function shanghaiDayStart(now: Date, daysBack = 0): Date {
  const f = shanghaiFields(now)
  return new Date(Date.UTC(f.y, f.mo - 1, f.d - daysBack) - SH_OFFSET_MS)
}

// toOffsetISO 把时刻写成带 +08:00 偏移的 ISO 字符串（秒级），发给 Bridge 的自定义边界用它。
export function toOffsetISO(d: Date): string {
  const f = shanghaiFields(d)
  return `${f.y}-${pad(f.mo)}-${pad(f.d)}T${pad(f.h)}:${pad(f.mi)}:${pad(f.s)}+08:00`
}

// toShanghaiInput 是 <input type="datetime-local"> 的值（分钟级、上海时间）。
export function toShanghaiInput(d: Date): string {
  const f = shanghaiFields(d)
  return `${f.y}-${pad(f.mo)}-${pad(f.d)}T${pad(f.h)}:${pad(f.mi)}`
}

// parseShanghaiInput 把分钟级输入按上海时间解析；日期不存在（13 月、2 月 30 日）返回 null。
export function parseShanghaiInput(value: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value)
  if (!m) return null
  const [y, mo, d, h, mi] = m.slice(1).map(Number)
  const t = new Date(Date.UTC(y, mo - 1, d, h, mi) - SH_OFFSET_MS)
  return toShanghaiInput(t) === value ? t : null
}

export function formatShanghai(iso: string, withSeconds = false): string {
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return '—'
  const f = shanghaiFields(t)
  const base = `${f.y}-${pad(f.mo)}-${pad(f.d)} ${pad(f.h)}:${pad(f.mi)}`
  return withSeconds ? `${base}:${pad(f.s)}` : base
}

// previewRange 只用于界面说明与本地校验；真正的统计范围以 Bridge 返回的 effectiveRange 为准。
// 「全部」不猜起点（start=null），由 Bridge 按当前身份可见记录确定。
export function previewRange(kind: Exclude<UsageRangeKind, 'custom'>, now: Date): { start: Date | null; end: Date } {
  switch (kind) {
    case 'today': return { start: shanghaiDayStart(now), end: now }
    case 'last24h': return { start: new Date(now.getTime() - DAY_MS), end: now }
    case 'last7d': return { start: shanghaiDayStart(now, 6), end: now }
    case 'last14d': return { start: shanghaiDayStart(now, 13), end: now }
    case 'last30d': return { start: shanghaiDayStart(now, 29), end: now }
    case 'all': return { start: null, end: now }
  }
}

export interface CustomDraft {
  start: string
  end: string
  followNow: boolean
}

// customDraftToSelection 校验自定义范围草稿；非法时返回原因，调用方保持原筛选不提交。
export function customDraftToSelection(draft: CustomDraft, now: Date): { selection?: UsageRangeSelection; error?: string } {
  const start = parseShanghaiInput(draft.start)
  if (!start) return { error: '请填写开始时间' }
  if (start.getTime() >= now.getTime()) return { error: '开始时间不能晚于当前时刻' }
  if (draft.followNow) return { selection: { kind: 'custom', startAt: toOffsetISO(start), followNow: true } }
  const end = parseShanghaiInput(draft.end)
  if (!end) return { error: '请填写结束时间' }
  if (start.getTime() >= end.getTime()) return { error: '开始时间必须早于结束时间' }
  if (end.getTime() > now.getTime()) return { error: '结束时间不能晚于当前时刻' }
  return { selection: { kind: 'custom', startAt: toOffsetISO(start), endAt: toOffsetISO(end), followNow: false } }
}

export function rangeLabel(sel: UsageRangeSelection): string {
  if (sel.kind !== 'custom') return USAGE_RANGES.find((r) => r.kind === sel.kind)?.label ?? sel.kind
  const start = sel.startAt ? formatShanghai(sel.startAt) : '—'
  return `${start} — ${sel.followNow ? '现在' : sel.endAt ? formatShanghai(sel.endAt) : '—'}`
}

// buildUsageQuery 只带单租户、范围与筛选；身份由 Bridge 从登录令牌得出，这里没有 userId。
export function buildUsageQuery(tenantId: string, sel: UsageRangeSelection, filters: UsageFilters): UsageQuery {
  const q: UsageQuery = { tenantId, range: sel.kind, followNow: false, timezone: USAGE_TIMEZONE }
  if (sel.kind === 'custom') {
    if (sel.startAt) q.startAt = sel.startAt
    if (sel.followNow) q.followNow = true
    else if (sel.endAt) q.endAt = sel.endAt
  }
  if (filters.providerId) q.providerId = filters.providerId
  if (filters.model) q.model = filters.model
  return q
}

export type BucketGranularity = 'hour' | 'day' | 'month'

// bucketGranularity 与 Bridge 的分桶规则一致：≤48 小时按小时，≤90 天按天，更长按月。
export function bucketGranularity(startISO: string, endISO: string): BucketGranularity {
  const span = new Date(endISO).getTime() - new Date(startISO).getTime()
  if (span <= 48 * HOUR_MS) return 'hour'
  if (span <= 90 * DAY_MS) return 'day'
  return 'month'
}

export function formatBucketLabel(iso: string, g: BucketGranularity): string {
  const f = shanghaiFields(new Date(iso))
  if (g === 'hour') return `${pad(f.h)}:${pad(f.mi)}`
  if (g === 'day') return `${pad(f.mo)}-${pad(f.d)}`
  return `${f.y}-${pad(f.mo)}`
}

export function formatBucketRange(startISO: string, endISO: string): string {
  return `${formatShanghai(startISO)} — ${formatShanghai(endISO)}（上海时间）`
}

// ---- 精确整数 ----------------------------------------------------------------------

const DECIMAL = /^(0|[1-9]\d*)$/

// parseCount 只接受非负十进制整数字符串；null / 非法值返回 null（绝不当成 0）。
export function parseCount(v: string | null | undefined): bigint | null {
  if (v == null || !DECIMAL.test(v)) return null
  return BigInt(v)
}

export function formatExact(v: string | null | undefined): string {
  const n = parseCount(v)
  if (n === null) return '—'
  return n.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

// 截断到一位小数（不四舍五入），缩写永远不会显示得比真实值大。
function truncOne(n: bigint, unit: bigint): string {
  const tenths = (n * 10n) / unit
  return `${tenths / 10n}.${tenths % 10n}`
}

// formatCompact 是卡片上的缩写（万 / 亿）；精确值放在 title 里给 tooltip。
export function formatCompact(v: string | null | undefined): string {
  const n = parseCount(v)
  if (n === null) return '—'
  if (n < 10_000n) return formatExact(v)
  if (n < 100_000_000n) return `${truncOne(n, 10_000n)}万`
  return `${truncOne(n, 100_000_000n)}亿`
}

// scaleCounts 在 BigInt 里算出每个值相对最大值的比例，再转 Number 给图表；超过 2^53 的
// 值也不会因为先转 Number 而失真。全部为 0 时比例都是 0。
export function scaleCounts(values: (string | null)[]): { max: bigint; ratios: (number | null)[] } {
  const parsed = values.map(parseCount)
  const max = parsed.reduce<bigint>((m, v) => (v !== null && v > m ? v : m), 0n)
  const SCALE = 1_000_000_000n
  const ratios = parsed.map((v) => (v === null ? null : max === 0n ? 0 : Number((v * SCALE) / max) / Number(SCALE)))
  return { max, ratios }
}

export function sumCounts(a: string | null, b: string | null): bigint | null {
  const x = parseCount(a)
  const y = parseCount(b)
  return x === null || y === null ? null : x + y
}

export function formatRate(rate: number | null | undefined): string {
  if (rate == null || Number.isNaN(rate)) return '—'
  return `${(rate * 100).toFixed(1)}%`
}

export function formatDurationMs(ms: number | null | undefined): string {
  if (ms == null || Number.isNaN(ms)) return '—'
  if (ms < 1000) return `${Math.round(ms)} 毫秒`
  return `${(ms / 1000).toFixed(1)} 秒`
}

// ---- 口径文案 ----------------------------------------------------------------------

const SOURCE_LABELS: Record<string, string> = {
  reported: '供应商上报',
  estimated: '估算',
  derived: '推导',
  legacyUnknown: '历史口径未确认',
  legacyInferred: '历史推导',
  unavailable: '不可用',
}

// sourceLabel 把单条记录的来源口径翻成中文；新 Bridge 增加的值显示「口径未知」，不冒充上报。
export function sourceLabel(source: string): string {
  return SOURCE_LABELS[source] ?? '口径未知'
}

export const COVERAGE_LABELS: Record<string, string> = {
  complete: '全部记录可统计',
  partial: '部分记录可统计',
  none: '暂无可统计记录',
}

export function tokenSampleLabel(sampleCount: string | undefined, tokenRequestCount: string): string {
  return parseCount(sampleCount) === null ? '样本未知' : `样本 ${formatExact(sampleCount)}/${formatExact(tokenRequestCount)} 次`
}

const STATUS_LABELS: Record<string, string> = {
  pending: '处理中', succeeded: '成功', failed: '失败', cancelled: '已取消', unknown: '未知', save_failed: '保存失败',
}

export function requestStatusLabel(status: string | undefined): string {
  return STATUS_LABELS[status ?? ''] ?? '未知'
}

// ---- 失败分类 ----------------------------------------------------------------------

export type UsageFailureKind =
  | 'notLoggedIn' | 'forbidden' | 'invalidRange' | 'invalidCursor' | 'unsupported' | 'timeout'
  | 'unavailable' | 'storage' | 'invalidResponse' | 'identityChanged' | 'invalidArgument' | 'unknown'

export interface UsageFailure {
  kind: UsageFailureKind
  message: string
  requestId?: string
}

const FAILURE_KINDS: Record<string, UsageFailureKind> = {
  [ErrCodes.NotLoggedIn]: 'notLoggedIn',
  [ErrCodes.InvalidArgument]: 'invalidArgument',
  [ErrCodes.UsageForbidden]: 'forbidden',
  [ErrCodes.UsageInvalidRange]: 'invalidRange',
  [ErrCodes.UsageInvalidCursor]: 'invalidCursor',
  [ErrCodes.UsageUnsupported]: 'unsupported',
  [ErrCodes.UsageTimeout]: 'timeout',
  [ErrCodes.UsageUnavailable]: 'unavailable',
  [ErrCodes.UsageStorage]: 'storage',
  [ErrCodes.UsageInvalidResponse]: 'invalidResponse',
  [ErrCodes.UsageIdentityChanged]: 'identityChanged',
}

// usageFailure 按宿主返回的结构化错误码分类；界面据此区分未登录、无权、不支持、超时
// 与暂不可用，而不是一律写成「暂无数据」。
export function usageFailure(e: unknown): UsageFailure {
  const raw = e instanceof Error ? e.message : String(e)
  let code = ''
  let requestId: string | undefined
  try {
    const o = JSON.parse(raw) as { code?: unknown; details?: { requestId?: unknown } }
    if (o && typeof o === 'object') {
      if (typeof o.code === 'string') code = o.code
      if (typeof o.details?.requestId === 'string') requestId = o.details.requestId
    }
  } catch {
    /* 非结构化错误：按未知失败显示原文 */
  }
  const failure: UsageFailure = { kind: FAILURE_KINDS[code] ?? 'unknown', message: errText(e) }
  if (requestId) failure.requestId = requestId
  return failure
}
