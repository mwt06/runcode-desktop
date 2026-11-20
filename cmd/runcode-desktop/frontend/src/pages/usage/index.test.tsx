// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PassportStatus } from '@/core/bridge'
import { UsagePage } from './index'
import { mergeUsageTenants, pickInitialTenant } from './tenant'
import { fixtureOverview, fixtureRequests, fixtureRow } from './fixtures'

const native = vi.hoisted(() => ({
  overview: vi.fn(),
  requests: vi.fn(),
  dimensions: vi.fn(),
  tenants: vi.fn(),
  active: vi.fn(),
  setActive: vi.fn(),
  reset: vi.fn(),
}))
vi.mock('@/core/bridge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/core/bridge')>()),
  getUsageOverview: native.overview,
  getUsageRequests: native.requests,
  getUsageDimensions: native.dimensions,
  getLocalUsageTenants: native.tenants,
  passportTenants: native.tenants,
  activeTenant: native.active,
  setActiveTenant: native.setActive,
  resetLocalUsageStore: native.reset,
}))

let host: HTMLDivElement
let root: Root
const user: PassportStatus = { loggedIn: true, userId: 'u-1', name: '张三' }

async function render(passport: PassportStatus | null, onOpenSettings = () => {}) {
  await act(async () => root.render(<UsagePage passport={passport} onOpenSettings={onOpenSettings} />))
}

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  localStorage.clear()
  for (const fn of Object.values(native)) fn.mockReset()
  native.overview.mockResolvedValue(fixtureOverview())
  native.requests.mockResolvedValue(fixtureRequests())
  native.dimensions.mockResolvedValue({ items: [], groupBy: 'model', page: 1, pageSize: 50, totalCount: '0', hasMore: false, effectiveRange: fixtureOverview().effectiveRange, queryKey: 'qk' })
  native.tenants.mockResolvedValue([{ id: 'tenant-a', name: '租户甲' }])
  native.active.mockResolvedValue('tenant-a')
  native.reset.mockResolvedValue(undefined)
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

describe('pickInitialTenant', () => {
  const list = [{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }]
  it('活动租户优先，其次令牌租户，唯一租户兜底；多租户不确定时要求选择', () => {
    expect(pickInitialTenant(list, 'b', 'a')).toBe('b')
    expect(pickInitialTenant(list, 'zzz', 'a')).toBe('a')
    expect(pickInitialTenant([list[0]], '', '')).toBe('a')
    expect(pickInitialTenant(list, '', '')).toBe('')
    expect(pickInitialTenant([], 'a', 'a')).toBe('')
  })
  it('mergeUsageTenants 把令牌租户并进历史列表且去重', () => {
    expect(mergeUsageTenants([{ id: 'a', name: '甲' }], 'wjtest', 'a')).toEqual([
      { id: 'a', name: '甲' },
      { id: 'wjtest', name: 'wjtest' },
    ])
  })
})

describe('UsagePage', () => {
  it('总览契约连接到状态分布与请求状态列，且不展示采集说明', async () => {
    native.overview.mockResolvedValue(fixtureOverview({
      sourceKind: 'local_platform', collectionStartedAt: '2026-11-09T00:00:00+08:00', storageStatus: 'unavailable',
      capabilities: { ...fixtureOverview().capabilities, requestStatus: true },
      statusCounts: { pending: '0', succeeded: '0', failed: '0', cancelled: '0', unknown: '0', saveFailed: '1' },
    }))
    native.requests.mockResolvedValue(fixtureRequests({ items: [fixtureRow({ status: 'save_failed' })] }))
    await render(user)
    expect(host.textContent).not.toContain('开始采集于 2026-11-09 00:00')
    expect(host.textContent).not.toContain('数据读取于')
    expect(host.textContent).not.toContain('存储不可用')
    expect(host.querySelector('[aria-label="请求状态分布"]')?.textContent).toContain('保存失败 1')
    expect(host.querySelector('[aria-label="明细"] tbody')?.textContent).toContain('保存失败')
  })

  it('未登录：显示登录说明与前往账号设置，不发任何统计请求', async () => {
    const onOpenSettings = vi.fn()
    await render({ loggedIn: false }, onOpenSettings)
    expect(host.textContent).toContain('登录通行证后')
    const go = [...host.querySelectorAll('button')].find((b) => b.textContent === '前往账号设置')!
    await act(async () => go.click())
    expect(onOpenSettings).toHaveBeenCalled()
    expect(native.overview).not.toHaveBeenCalled()
    expect(native.tenants).not.toHaveBeenCalled()
  })

  it('已登录：范围条写明账号、租户、时区与本机口径；不修改聊天租户', async () => {
    await render(user)
    const t = host.textContent ?? ''
    expect(t).toContain('张三')
    expect(t).toContain('租户甲')
    expect(t).toContain('Asia/Shanghai')
    expect(t).toContain('本机采集的平台调用')
    expect(t).not.toContain('关闭客户端期间可能有缺口')
    expect(t).not.toContain('数据读取于')
    expect(t).not.toContain('本机采集记录 · 开始采集于')
    expect(t).not.toContain('含起点、不含终点')
    expect(t).toContain('1.1万')
    expect(native.overview).toHaveBeenCalledWith(expect.objectContaining({ tenantId: 'tenant-a', range: 'today' }))
    expect(native.setActive).not.toHaveBeenCalled()
    expect(t).not.toMatch(/成本|费用|价格|¥/)
  })

  it('本机尚无历史租户时用令牌租户打开空统计，不再硬挡', async () => {
    native.tenants.mockResolvedValue([])
    native.overview.mockResolvedValue(fixtureOverview({ empty: true, summary: { ...fixtureOverview().summary, requestCount: '0', totalTokens: '0' } }))
    await render({ ...user, tenantId: 'wjtest' })
    expect(host.textContent).not.toContain('没有可用租户，无法查看用量')
    expect(native.overview).toHaveBeenCalledWith(expect.objectContaining({ tenantId: 'wjtest' }))
  })

  it('多租户且无法确定时先要求选择，不以空租户查询', async () => {
    native.tenants.mockResolvedValue([{ id: 'a', name: '甲' }, { id: 'b', name: '乙' }])
    native.active.mockResolvedValue('')
    await render(user)
    expect(host.textContent).toContain('请选择要统计的租户')
    expect(native.overview).not.toHaveBeenCalled()
  })

  it.each([
    ['usage_forbidden', '无权查看'],
    ['usage_unsupported', '不代表零用量'],
    ['usage_timeout', '缩小时间范围'],
    ['usage_unavailable', '暂不可用'],
  ])('失败 %s 有各自的说明与重试，不显示成零用量', async (code, phrase) => {
    native.overview.mockRejectedValue(new Error(JSON.stringify({ code, message: '服务端说明', details: { requestId: 'req-9' } })))
    await render(user)
    const t = host.textContent ?? ''
    expect(t).toContain(phrase)
    expect(t).toContain('req-9')
    expect(t).not.toContain('暂无记录')
    expect([...host.querySelectorAll('button')].some((b) => b.textContent === '重试')).toBe(true)
  })

  it('本机库损坏时提供重建入口，并保留重试', async () => {
    native.overview.mockRejectedValue(new Error(JSON.stringify({
      code: 'usage_storage', message: '本机用量库已损坏；可重建本地统计（原文件会保留备份）',
    })))
    await render(user)
    expect(host.textContent).toContain('本机用量库需要修复')
    const reset = [...host.querySelectorAll('button')].find((b) => b.textContent === '重建本机用量库')!
    await act(async () => reset.click())
    expect(native.reset).toHaveBeenCalledTimes(1)
    expect(native.overview.mock.calls.length).toBeGreaterThanOrEqual(2)
  })

  it.each([
    ['usage_unavailable', '用量服务暂不可用，请稍后重试'],
    ['usage_forbidden', '当前账号无权查看所选租户的用量'],
  ])('初次失败 %s：标题与消息同文时只出现一次，仍显示请求编号', async (code, same) => {
    native.overview.mockRejectedValue(new Error(JSON.stringify({ code, message: same, details: { requestId: 'req-same' } })))
    await render(user)
    const t = host.textContent ?? ''
    expect(t.split(same).length - 1).toBe(1)
    expect(t).toContain('req-same')
    expect([...host.querySelectorAll('button')].some((b) => b.textContent === '重试')).toBe(true)
  })

  it('刷新失败 warning：同原因不重复；差异详情与编号、重试保留', async () => {
    await render(user)
    expect(host.textContent).toContain('1.1万')

    const same = '用量服务暂不可用，请稍后重试'
    native.overview.mockRejectedValueOnce(new Error(JSON.stringify({
      code: 'usage_unavailable', message: same, details: { requestId: 'req-warn-1' },
    })))
    const refresh = [...host.querySelectorAll('button')].find((b) => b.textContent?.includes('刷新'))!
    await act(async () => refresh.click())
    let t = host.textContent ?? ''
    expect(t).toContain('刷新失败，显示上次数据')
    expect(t).toContain('1.1万')
    expect(t.split(same).length - 1).toBe(1)
    expect(t).toContain('req-warn-1')
    expect([...host.querySelectorAll('button')].some((b) => b.textContent === '重试')).toBe(true)

    native.overview.mockRejectedValueOnce(new Error(JSON.stringify({
      code: 'usage_unavailable', message: '上游返回了额外说明，请缩小范围', details: { requestId: 'req-warn-2' },
    })))
    await act(async () => refresh.click())
    t = host.textContent ?? ''
    expect(t).toContain('刷新失败，显示上次数据')
    expect(t).toContain(same)
    expect(t).toContain('上游返回了额外说明，请缩小范围')
    expect(t).toContain('req-warn-2')
    expect(t).toContain('1.1万')
  })

  it('未知错误标题兜底时仍显示差异正文', async () => {
    native.overview.mockRejectedValue(new Error(JSON.stringify({
      code: 'usage_weird', message: '未分类的宿主说明', details: { requestId: 'req-unk' },
    })))
    await render(user)
    const t = host.textContent ?? ''
    expect(t).toContain('读取用量失败')
    expect(t).toContain('未分类的宿主说明')
    expect(t).toContain('req-unk')
    expect(t.split('读取用量失败').length - 1).toBe(1)
  })

  it('模型候选可搜索与加载更多；长名完整进选项；选中项保留；无前 N 截断文案', async () => {
    const longName = '超级超级超级长的历史模型名称-needle-101'
    const totals = fixtureOverview().summary
    const setInput = (el: HTMLInputElement, value: string) => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(el, value)
      el.dispatchEvent(new Event('input', { bubbles: true }))
    }
    const setSelect = (el: HTMLSelectElement, value: string) => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(el, value)
      el.dispatchEvent(new Event('change', { bubbles: true }))
    }
    native.overview.mockResolvedValue(fixtureOverview({
      capabilities: { providerBreakdown: true, cacheMetrics: true, requestStatus: false, ttft: false },
    }))
    native.dimensions.mockImplementation(async (q: { groupBy: string; page: number; search?: string }) => {
      const base = { effectiveRange: fixtureOverview().effectiveRange, queryKey: 'qk', pageSize: 50 as const }
      if (q.groupBy === 'provider') {
        return { items: [{ id: 'prov-a', name: '供应商甲', totals }], groupBy: 'provider', page: 1, totalCount: '1', hasMore: false, ...base }
      }
      if (q.search === 'needle') {
        return { items: [{ id: 'needle-101', name: longName, totals }], groupBy: 'model', page: 1, totalCount: '1', hasMore: false, ...base }
      }
      if (q.page === 2) {
        return { items: [{ id: 'm-51', name: 'model-51', totals }], groupBy: 'model', page: 2, totalCount: '80', hasMore: false, ...base }
      }
      return {
        items: Array.from({ length: 50 }, (_, i) => ({ id: `m-${i + 1}`, name: `model-${i + 1}`, totals })),
        groupBy: 'model', page: 1, totalCount: '80', hasMore: true, ...base,
      }
    })
    await render(user)

    const modelSearch = host.querySelector('input[aria-label="搜索模型候选"]') as HTMLInputElement
    const modelSearchBtn = [...host.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === '执行模型候选搜索')!
    const loadMore = [...host.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === '加载更多模型候选')!
    expect(modelSearch).toBeTruthy()
    expect(loadMore).toBeTruthy()
    expect(host.textContent).not.toMatch(/前\s*100\s*个|仅列出请求最多/)

    const modelSelect = host.querySelector('select[aria-label="筛选模型"]') as HTMLSelectElement
    await act(async () => setSelect(modelSelect, 'm-1'))
    expect(modelSelect.value).toBe('m-1')

    await act(async () => loadMore.click())
    expect([...modelSelect.options].some((o) => o.value === 'm-51')).toBe(true)
    expect(modelSelect.value).toBe('m-1')

    await act(async () => setInput(modelSearch, 'needle'))
    await act(async () => modelSearchBtn.click())
    const selectAfter = host.querySelector('select[aria-label="筛选模型"]') as HTMLSelectElement
    expect([...selectAfter.options].some((o) => o.textContent === longName)).toBe(true)
    expect([...selectAfter.options].some((o) => o.value === 'm-1')).toBe(true)
    expect(selectAfter.value).toBe('m-1')
    expect(host.querySelector('input[aria-label="搜索供应商候选"]')).toBeTruthy()
  })
})
