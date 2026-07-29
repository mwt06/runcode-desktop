import type {
  PassportModel,
  PassportStatus,
  PassportTenant,
  StartSessionRequest,
} from './bridge'

export type PassportAccountPhase = 'idle' | 'resolving' | 'ready' | 'logged-out' | 'error'

export type PassportAccountSnapshot = {
  phase: PassportAccountPhase
  status: PassportStatus
  tenants: PassportTenant[]
  eligibleTenants: PassportTenant[]
  tenantId: string
  models: PassportModel[]
  error: string
}

export type PassportAccountDependencies = {
  passportStatus: () => Promise<PassportStatus>
  passportTenants: () => Promise<PassportTenant[] | null>
  activeTenant: () => Promise<string>
  setActiveTenant: (tenantId: string) => Promise<void>
  passportModels: (tenantId: string) => Promise<PassportModel[] | null>
  errorText: (error: unknown) => string
}

export type PassportAccountCoordinator = {
  refresh: (status?: PassportStatus) => Promise<void>
  selectTenant: (tenantId: string) => Promise<void>
  dispose: () => void
}

// passportDisplayName 取登录用户的展示名,给问候语等处用。未登录返回空串,让调用方
// 自行降级(不带名字的问候)。优先真实姓名 > 昵称 > 账号名 > 用户 ID,与账号页
// "已登录：<名字>" 的取值口径一致。
export function passportDisplayName(status: PassportStatus | null | undefined): string {
  if (!status || !status.loggedIn) return ''
  return (status.name || status.nickname || status.userName || status.userId || '').trim()
}

export function initialPassportAccountSnapshot(): PassportAccountSnapshot {
  return {
    phase: 'idle',
    status: { loggedIn: false },
    tenants: [],
    eligibleTenants: [],
    tenantId: '',
    models: [],
    error: '',
  }
}

// A selectable tenant is a node that is not the parent of another returned node.
// Parent IDs outside this response (for example "SuperTenant") and malformed
// self-parent links do not make an otherwise selectable tenant disappear.
export function eligibleLeafTenants(tenants: PassportTenant[]): PassportTenant[] {
  const ids = new Set(tenants.map((tenant) => tenant.id))
  const parents = new Set<string>()
  for (const tenant of tenants) {
    const parentId = tenant.parentId?.trim()
    if (parentId && parentId !== tenant.id && ids.has(parentId)) parents.add(parentId)
  }
  return tenants.filter((tenant, index) =>
    !parents.has(tenant.id) && tenants.findIndex((candidate) => candidate.id === tenant.id) === index,
  )
}

export function resolveEligibleTenant(tenants: PassportTenant[], activeTenantId: string): {
  eligibleTenants: PassportTenant[]
  tenantId: string
  bindingChanged: boolean
} {
  const eligibleTenants = eligibleLeafTenants(tenants)
  const active = activeTenantId.trim()
  const activeIsEligible = eligibleTenants.some((tenant) => tenant.id === active)
  const tenantId = activeIsEligible ? active : eligibleTenants.length === 1 ? eligibleTenants[0].id : ''
  return { eligibleTenants, tenantId, bindingChanged: tenantId !== active }
}

export function canAutoStartPassport(
  initial: Partial<StartSessionRequest>,
  account: PassportAccountSnapshot,
): boolean {
  if (account.phase !== 'ready' || !account.status.loggedIn || initial.provider !== 'passport') return false
  if (!(initial.cwd ?? '').trim() || !(initial.model ?? '').trim() || !account.tenantId) return false
  if (!account.eligibleTenants.some((tenant) => tenant.id === account.tenantId)) return false
  return account.models.some((model) => model.id === initial.model)
}

// shouldShowLoginGate decides whether the start flow shows the full-screen login
// page rather than the tenant/model form. Login is mandatory: an unauthenticated
// user always sees the login page — including after a logout or an expired login —
// UNLESS the 免登录 (skip-login) option is enabled in settings, which lets them
// proceed to the form (e.g. to use a local custom model). A logged-in user never
// sees the gate. skipLogin is a settings option on purpose (not a one-click bypass
// on the login page), so a locked-down deployment cannot be trivially bypassed.
export function shouldShowLoginGate(loggedIn: boolean, skipLogin: boolean): boolean {
  return !loggedIn && !skipLogin
}

// Serializes account refreshes and tenant switches. Every request advances a
// revision; an older operation may finish its current bridge call, but it cannot
// publish stale state or continue into later side effects. Queued obsolete work
// exits immediately when it reaches the serialized tail.
export function createPassportAccountCoordinator(
  dependencies: PassportAccountDependencies,
  onSnapshot: (snapshot: PassportAccountSnapshot) => void,
): PassportAccountCoordinator {
  let snapshot = initialPassportAccountSnapshot()
  let revision = 0
  let disposed = false
  let tail = Promise.resolve()

  const publish = (next: PassportAccountSnapshot, operationRevision: number): boolean => {
    if (disposed || operationRevision !== revision) return false
    snapshot = next
    onSnapshot(next)
    return true
  }
  const current = (operationRevision: number) => !disposed && operationRevision === revision
  const errorMessage = (prefix: string, error: unknown) => `${prefix}：${dependencies.errorText(error)}`

  const enqueue = (run: (operationRevision: number) => Promise<void>): Promise<void> => {
    const operationRevision = ++revision
    const operation = tail.then(async () => {
      if (!current(operationRevision)) return
      await run(operationRevision)
    })
    tail = operation.catch(() => {})
    return operation
  }

  const refresh = (statusHint?: PassportStatus): Promise<void> => {
    // Logout must clear renderer state immediately. Advancing the revision makes
    // every in-flight logged-in refresh stale before it can repopulate the UI.
    if (statusHint && !statusHint.loggedIn) {
      const operationRevision = ++revision
      publish({ ...initialPassportAccountSnapshot(), phase: 'logged-out', status: statusHint }, operationRevision)
      return Promise.resolve()
    }

    return enqueue(async (operationRevision) => {
      const resolvingStatus = statusHint ?? snapshot.status
      if (!publish({ ...snapshot, phase: 'resolving', status: resolvingStatus, models: [], error: '' }, operationRevision)) return

      let status: PassportStatus
      try {
        status = statusHint ?? await dependencies.passportStatus()
      } catch (error) {
        publish({ ...snapshot, phase: 'error', models: [], error: errorMessage('读取登录状态失败', error) }, operationRevision)
        return
      }
      if (!current(operationRevision)) return
      if (!status.loggedIn) {
        publish({ ...initialPassportAccountSnapshot(), phase: 'logged-out', status }, operationRevision)
        return
      }

      let tenants: PassportTenant[]
      let active: string
      try {
        [tenants, active] = await Promise.all([
          dependencies.passportTenants().then((list) => list ?? []),
          dependencies.activeTenant(),
        ])
      } catch (error) {
        publish({ ...snapshot, phase: 'error', status, models: [], error: errorMessage('同步租户失败', error) }, operationRevision)
        return
      }
      if (!current(operationRevision)) return

      const resolved = resolveEligibleTenant(tenants, active)
      if (resolved.bindingChanged) {
        try {
          await dependencies.setActiveTenant(resolved.tenantId)
        } catch (error) {
          publish({
            ...snapshot,
            phase: 'error',
            status,
            tenants,
            eligibleTenants: resolved.eligibleTenants,
            tenantId: active.trim(),
            models: [],
            error: errorMessage('绑定租户失败', error),
          }, operationRevision)
          return
        }
      }
      if (!current(operationRevision)) return

      let models: PassportModel[] = []
      if (resolved.tenantId) {
        try {
          models = (await dependencies.passportModels(resolved.tenantId)) ?? []
        } catch (error) {
          publish({
            phase: 'error',
            status,
            tenants,
            eligibleTenants: resolved.eligibleTenants,
            tenantId: resolved.tenantId,
            models: [],
            error: errorMessage('获取平台模型失败', error),
          }, operationRevision)
          return
        }
      }
      publish({
        phase: 'ready',
        status,
        tenants,
        eligibleTenants: resolved.eligibleTenants,
        tenantId: resolved.tenantId,
        models,
        error: '',
      }, operationRevision)
    })
  }

  const selectTenant = (tenantId: string): Promise<void> => enqueue(async (operationRevision) => {
    const selected = tenantId.trim()
    const eligible = snapshot.eligibleTenants.some((tenant) => tenant.id === selected)
    if (!snapshot.status.loggedIn || !eligible) {
      publish({ ...snapshot, phase: 'error', error: '请选择有效的末级租户' }, operationRevision)
      return
    }

    const previous = snapshot
    if (!publish({ ...previous, phase: 'resolving', models: [], error: '' }, operationRevision)) return
    if (selected !== previous.tenantId) {
      try {
        await dependencies.setActiveTenant(selected)
      } catch (error) {
        publish({ ...previous, phase: 'error', error: errorMessage('绑定租户失败', error) }, operationRevision)
        return
      }
    }
    if (!current(operationRevision)) return

    try {
      const models = (await dependencies.passportModels(selected)) ?? []
      publish({ ...previous, phase: 'ready', tenantId: selected, models, error: '' }, operationRevision)
    } catch (error) {
      publish({
        ...previous,
        phase: 'error',
        tenantId: selected,
        models: [],
        error: errorMessage('获取平台模型失败', error),
      }, operationRevision)
    }
  })

  return {
    refresh,
    selectTenant,
    dispose: () => { disposed = true; revision++ },
  }
}
