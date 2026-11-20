// 用量统计页：我的平台用量 · 当前租户。身份来自应用登录状态，统计租户只读取得初始值、
// 页内切换只影响本页；未登录、无权、不支持、超时、暂不可用各自说明，不写成「暂无数据」。
import { useState, type ReactNode } from 'react'
import { errText, resetLocalUsageStore, type PassportStatus, type UsageDimensionRow } from '@/core/bridge'
import { passportDisplayName } from '@/core/passport-account'
import { USAGE_TIMEZONE, formatShanghai, type UsageFailure } from '@/core/usage'
import { PageShell, Placeholder } from '@/ui/layout'
import { Banner, InlineError } from '@/ui/feedback'
import { FIELD_CLS, SelectField } from '@/ui/fields'
import { Icon } from '@/ui/icons'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { RangePicker } from './range-picker'
import { UsageMetrics } from './metrics'
import { UsageTrend, type TrendMode } from './trend'
import { UsageRequests } from './requests'
import { UsageDimensions } from './dimensions'
import { useUsageTenants } from './tenant'
import { useUsage, type UsageGroupBy, type UsageOptionCandidate } from './use-usage'

const PAGE_WIDTH = 1120
const PANEL = 'bg-surface border border-line2 rounded-card p-5 flex flex-col gap-3 items-start shadow-xs'

/** 筛选候选：SelectField + 显式搜索与「加载更多」；搜索只改候选，选定才改统计 filters。 */
function OptionFilter({
  kind, value, options, candidate, selectedLabel, disabled, onSelect, onSearch, onLoadMore,
}: {
  kind: UsageGroupBy
  value: string
  options: UsageDimensionRow[]
  candidate: UsageOptionCandidate
  selectedLabel: string
  disabled: boolean
  onSelect: (id: string) => void
  onSearch: (text: string) => void
  onLoadMore: () => void
}) {
  const [draft, setDraft] = useState('')
  const allLabel = kind === 'model' ? '全部模型' : '全部供应商'
  const searchLabel = kind === 'model' ? '搜索模型候选' : '搜索供应商候选'
  const searchAction = kind === 'model' ? '执行模型候选搜索' : '执行供应商候选搜索'
  const moreAction = kind === 'model' ? '加载更多模型候选' : '加载更多供应商候选'
  const selectLabel = kind === 'model' ? '筛选模型' : '筛选供应商'
  const unknownLabel = kind === 'provider' ? '未记录' : selectedLabel
  return (
    <div className="flex flex-col gap-1.5 min-w-[220px]">
      <div className="flex items-center gap-1.5">
        <input
          aria-label={searchLabel}
          value={draft}
          disabled={disabled}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') onSearch(draft) }}
          placeholder="搜索"
          className={`${FIELD_CLS} flex-1 py-1.5 text-[12px]`}
        />
        <button type="button" aria-label={searchAction} className={`${BTN} py-1.5 text-[12px]`} disabled={disabled} onClick={() => onSearch(draft)}>
          搜索
        </button>
      </div>
      <SelectField value={value} onChange={onSelect} disabled={disabled} aria-label={selectLabel}>
        <option value="">{allLabel}</option>
        {value && !options.some((o) => o.id === value) && (
          <option value={value}>{kind === 'provider' && (value === 'unknown' || !selectedLabel) ? unknownLabel : selectedLabel || value}</option>
        )}
        {options.map((o) => (
          <option key={o.id} value={o.id}>
            {kind === 'provider' && (o.id === 'unknown' || !o.name) ? '未记录' : (o.name || o.id)}
          </option>
        ))}
      </SelectField>
      {candidate.hasMore && (
        <button type="button" aria-label={moreAction} className={`${BTN} self-start py-1 text-[12px]`} disabled={disabled} onClick={onLoadMore}>
          加载更多{candidate.totalCount !== '0' ? `（已 ${options.length}/${candidate.totalCount}）` : ''}
        </button>
      )}
    </div>
  )
}

const FAILURE_TITLES: Partial<Record<UsageFailure['kind'], string>> = {
  notLoggedIn: '通行证登录已失效',
  forbidden: '当前账号无权查看所选租户的用量',
  unsupported: '当前数据暂不支持这项统计，未显示的指标不代表零用量',
  timeout: '统计超时：可以缩小时间范围后重试',
  unavailable: '用量服务暂不可用，请稍后重试',
  storage: '本机用量库需要修复',
  invalidResponse: '用量服务返回的数据不完整，已整份丢弃',
  invalidRange: '时间范围或筛选条件无效',
  invalidCursor: '分页已失效，需要回到第一页',
}

/**
 * FailureBody：Banner 已展示语义标题时的正文。
 * 去重规则——trim 后消息非空且与语义标题不同才再显示；warning 外层
 * 「刷新失败，显示上次数据」前缀不参与比较（调用方只传入语义标题）。
 */
function FailureBody({ failure, title }: { failure: UsageFailure; title: string }) {
  const message = (failure.message ?? '').trim()
  const showMessage = message !== '' && message !== title.trim()
  return (
    <>
      {showMessage ? message : null}
      {failure.requestId && <span className="block mt-1 text-[12px] text-muted select-all">请求编号：{failure.requestId}</span>}
    </>
  )
}

function LoginPrompt({ onOpenSettings, title }: { onOpenSettings: () => void; title: string }) {
  return (
    <div className={PANEL}>
      <div className="text-[14px] font-semibold text-ink">{title}</div>
      <p className="m-0 text-[13px] text-muted">用量统计来自本机记录的平台调用，需要用通行证账号查看。</p>
      <button type="button" className={`${BTN} ${BTN_PRIMARY}`} onClick={onOpenSettings}>前往账号设置</button>
    </div>
  )
}

type Tab = 'requests' | 'model' | 'provider'

export function UsagePage({ passport, onOpenSettings }: { passport: PassportStatus | null; onOpenSettings: () => void }) {
  const identityKey = passport?.loggedIn ? `user:${passport.userId ?? ''}` : ''
  const tenants = useUsageTenants(identityKey, passport?.tenantId ?? '')
  const usage = useUsage(identityKey, tenants.selected)
  const [tab, setTab] = useState<Tab>('requests')
  const [trendMode, setTrendMode] = useState<TrendMode>('tokens')
  const [resetting, setResetting] = useState(false)
  const [resetError, setResetError] = useState('')
  const overview = usage.overview
  const providerBreakdown = !!overview?.capabilities.providerBreakdown

  if (!identityKey) {
    return (
      <PageShell title="用量统计" hint="我的本机用量 · 当前租户" width={PAGE_WIDTH}>
        <LoginPrompt onOpenSettings={onOpenSettings} title="登录通行证后可查看本机采集的平台用量" />
      </PageShell>
    )
  }

  const tenantName = tenants.tenants.find((t) => t.id === tenants.selected)?.name || tenants.selected
  const action = (
    <div className="flex items-center gap-2">
      <label className="inline-flex items-center gap-1.5 text-[12px] text-muted">
        自动刷新
        <select
          value={usage.autoRefresh ? '60' : 'off'}
          onChange={(e) => { if ((e.target.value === '60') !== usage.autoRefresh) usage.toggleAutoRefresh() }}
          className="bg-surface2 border border-line2 rounded-field px-2 py-1 text-[12px] text-ink"
        >
          <option value="off">关闭</option>
          <option value="60">60 秒</option>
        </select>
      </label>
      <button type="button" className={`${BTN} inline-flex items-center gap-1.5 py-1.5 text-[13px]`} disabled={usage.loading || !tenants.selected} onClick={usage.refresh}>
        <Icon name="refresh" size={14} />刷新
      </button>
    </div>
  )

  let body: ReactNode
  if (tenants.loading) body = <Placeholder>正在读取可用租户…</Placeholder>
  else if (tenants.error) {
    body = (
      <div className={PANEL}>
        <InlineError variant="text">无法读取可用租户：{tenants.error}</InlineError>
        <button type="button" className={BTN} onClick={tenants.retry}>重试</button>
      </div>
    )
  } else if (tenants.tenants.length === 0) {
    body = <Placeholder>本机暂无用量记录。登录后发起平台对话或生图，用量会显示在这里。</Placeholder>
  } else if (!tenants.selected) body = <Placeholder>请选择要统计的租户。</Placeholder>
  else if (usage.error?.kind === 'notLoggedIn' && !overview) body = <LoginPrompt onOpenSettings={onOpenSettings} title="通行证登录已失效，请重新登录后查看用量" />
  else if (usage.error && !overview) {
    const title = FAILURE_TITLES[usage.error.kind] ?? '读取用量失败'
    const reset = async () => {
      setResetting(true)
      setResetError('')
      try {
        await resetLocalUsageStore()
        await usage.refresh()
      } catch (e) {
        setResetError(errText(e))
      } finally {
        setResetting(false)
      }
    }
    body = (
      <div className={PANEL}>
        <Banner tone="danger" title={title} className="w-full"><FailureBody failure={usage.error} title={title} /></Banner>
        <div className="flex flex-wrap gap-2">
          <button type="button" className={BTN} onClick={usage.refresh}>重试</button>
          {usage.error.kind === 'storage' && (
            <button type="button" className={`${BTN} ${BTN_PRIMARY}`} disabled={resetting} onClick={() => void reset()}>
              {resetting ? '正在重建…' : '重建本机用量库'}
            </button>
          )}
        </div>
        {resetError && <InlineError variant="text">{resetError}</InlineError>}
      </div>
    )
  } else if (!overview) body = <Placeholder>加载中…</Placeholder>
  else {
    const range = overview.effectiveRange
    const dimensions = usage.dimensions
    const showTab: Tab = tab === 'provider' && !providerBreakdown ? 'requests' : tab
    const refreshReasonTitle = usage.error ? (FAILURE_TITLES[usage.error.kind] ?? '读取用量失败') : ''
    body = (
      <>
        {usage.error && (
          <Banner tone="warning" title={`刷新失败，显示上次数据：${refreshReasonTitle}`}>
            <FailureBody failure={usage.error} title={refreshReasonTitle} />
            <button type="button" className="mt-1.5 text-[12px] text-primaryink hover:underline" onClick={usage.refresh}>
              {usage.error.kind === 'invalidCursor' ? '回到第一页' : '重试'}
            </button>
          </Banner>
        )}
        {usage.hasUpdate && (
          <Banner tone="neutral" title="有新的用量记录">
            当前停留在第 {usage.requestPage} 页，数据已冻结。
            <button type="button" className="ml-2 text-[12px] text-primaryink hover:underline" onClick={usage.refresh}>回到第一页查看</button>
          </Banner>
        )}
        <p className="m-0 text-[12px] text-muted">
          统计范围 {formatShanghai(range.startAt)} — {formatShanghai(range.endAt)}
        </p>
        <UsageMetrics totals={overview.summary} capabilities={overview.capabilities} empty={overview.empty} statusCounts={overview.statusCounts} />
        <UsageTrend buckets={overview.trend ?? []} range={range} mode={trendMode} onModeChange={setTrendMode} coverageStatus={overview.coverageStatus} />
        <section className="flex flex-col gap-3" aria-label="明细">
          <div role="tablist" className="flex gap-1 border-b border-line2">
            {([['requests', '请求日志'], ['model', '模型汇总'], ...(providerBreakdown ? [['provider', '供应商汇总']] : [])] as [Tab, string][]).map(([id, label]) => (
              <button
                key={id}
                type="button"
                role="tab"
                aria-selected={showTab === id}
                onClick={() => {
                  setTab(id)
                  if (id !== 'requests' && dimensions?.groupBy !== id) usage.setGroupBy(id)
                }}
                className={`px-3 py-2 -mb-px text-[13px] border-b-2 ${showTab === id ? 'border-primary text-primaryink font-medium' : 'border-transparent text-muted hover:text-ink'}`}
              >
                {label}
              </button>
            ))}
          </div>
          {showTab === 'requests' && usage.requests && (
            <UsageRequests
              page={usage.requests}
              pageIndex={usage.requestPage}
              pageSize={usage.pageSize}
              providerColumn={providerBreakdown}
              statusColumn={overview.capabilities.requestStatus}
              loading={usage.loading}
              onPrev={() => void usage.prevPage()}
              onNext={() => void usage.nextPage()}
              onPageSize={usage.setPageSize}
            />
          )}
          {showTab !== 'requests' && dimensions && dimensions.groupBy === showTab && (
            <UsageDimensions page={dimensions} loading={usage.loading} onPage={usage.setDimensionPage} />
          )}
          {showTab !== 'requests' && (!dimensions || dimensions.groupBy !== showTab) && <Placeholder>加载中…</Placeholder>}
        </section>
      </>
    )
  }

  const models = usage.modelOptions
  const providers = usage.providerOptions
  const selectedModel = usage.filters.model ?? ''
  const selectedProvider = usage.filters.providerId ?? ''
  const selectedProviderName = providers.find((p) => p.id === selectedProvider)?.name ?? selectedProvider
  return (
    <PageShell title="用量统计" hint="我的本机用量 · 当前租户" width={PAGE_WIDTH} action={action}>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[12px] text-muted bg-surface2 border border-line2 rounded-field px-3 py-2">
        <span>账号：<b className="font-medium text-ink">{passportDisplayName(passport) || '已登录用户'}</b></span>
        <span className="inline-flex items-center gap-1.5">
          租户：
          {tenants.tenants.length > 1 ? (
            <select
              aria-label="统计租户"
              value={tenants.selected}
              onChange={(e) => tenants.select(e.target.value)}
              className="bg-surface border border-line2 rounded-md px-1.5 py-0.5 text-[12px] text-ink"
            >
              {!tenants.selected && <option value="">请选择</option>}
              {tenants.tenants.map((t) => <option key={t.id} value={t.id}>{t.name || t.id}</option>)}
            </select>
          ) : <b className="font-medium text-ink">{tenantName || '—'}</b>}
        </span>
        <span>时区：{USAGE_TIMEZONE}（UTC+8）</span>
        <span>来源：本机采集的平台调用（不含自定义直连模型）</span>
      </div>
      {tenants.selected && (
        <div className="flex flex-wrap items-end gap-3">
          {providerBreakdown && (
            <OptionFilter
              kind="provider"
              value={selectedProvider}
              options={providers}
              candidate={usage.providerCandidate}
              selectedLabel={selectedProviderName}
              disabled={usage.loading}
              onSelect={usage.setProvider}
              onSearch={(text) => void usage.searchOptions('provider', text)}
              onLoadMore={() => void usage.loadMoreOptions('provider')}
            />
          )}
          <OptionFilter
            kind="model"
            value={selectedModel}
            options={models}
            candidate={usage.modelCandidate}
            selectedLabel={selectedModel}
            disabled={usage.loading}
            onSelect={usage.setModel}
            onSearch={(text) => void usage.searchOptions('model', text)}
            onLoadMore={() => void usage.loadMoreOptions('model')}
          />
          <RangePicker value={usage.selection} onApply={usage.setSelection} disabled={usage.loading && !overview} />
        </div>
      )}
      {body}
    </PageShell>
  )
}
