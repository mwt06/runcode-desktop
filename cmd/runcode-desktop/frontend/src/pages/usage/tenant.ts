// 用量页的统计租户：只读取得初始范围，页面内切换只改本页，不碰聊天会话的活动租户
// （不调用 setActiveTenant，也不走账号协调器的 refresh / selectTenant）。
import { useEffect, useState } from 'react'
import { errText, getLocalUsageTenants, type PassportTenant } from '@/core/bridge'

// pickInitialTenant 依次取：应用当前活动租户、令牌自带租户、唯一可用租户；都不确定时返回
// 空串，让用户先选——绝不以空租户扩展成「所有租户」。
export function pickInitialTenant(tenants: PassportTenant[], active: string, tokenTenant: string): string {
  const ids = new Set(tenants.map((t) => t.id))
  if (active && ids.has(active)) return active
  if (tokenTenant && ids.has(tokenTenant)) return tokenTenant
  return tenants.length === 1 ? tenants[0].id : ''
}

// mergeUsageTenants 把令牌／活动租户并入本机历史列表，便于尚无采集记录时也能打开空统计。
export function mergeUsageTenants(history: PassportTenant[], ...hints: string[]): PassportTenant[] {
  const out: PassportTenant[] = []
  const seen = new Set<string>()
  const add = (id: string, name?: string) => {
    const tid = id.trim()
    if (!tid || seen.has(tid)) return
    seen.add(tid)
    out.push({ id: tid, name: (name || tid).trim() || tid })
  }
  for (const t of history) add(t.id, t.name)
  for (const h of hints) add(h)
  return out
}

interface TenantState {
  key: string
  tenants: PassportTenant[]
  selected: string
  loading: boolean
  error: string
}

const EMPTY: TenantState = { key: '', tenants: [], selected: '', loading: false, error: '' }

export function useUsageTenants(identityKey: string, tokenTenant: string) {
  const [state, setState] = useState<TenantState>(EMPTY)
  const [nonce, setNonce] = useState(0)
  useEffect(() => {
    if (!identityKey) { setState(EMPTY); return }
    let alive = true
    setState({ ...EMPTY, key: identityKey, loading: true })
    getLocalUsageTenants()
      .then((list) => {
        if (!alive) return
        const tenants = mergeUsageTenants(list ?? [], tokenTenant)
        // 本机历史租户列表不改变聊天页的活动租户；令牌租户只作为初始选择提示。
        setState({ key: identityKey, tenants, selected: pickInitialTenant(tenants, '', tokenTenant), loading: false, error: '' })
      })
      .catch((e: unknown) => { if (alive) setState({ ...EMPTY, key: identityKey, error: errText(e) }) })
    return () => { alive = false }
  }, [identityKey, tokenTenant, nonce])
  const current = state.key === identityKey ? state : EMPTY
  return {
    ...current,
    select: (id: string) => setState((s) => (s.key === identityKey ? { ...s, selected: id } : s)),
    retry: () => setNonce((n) => n + 1),
  }
}
