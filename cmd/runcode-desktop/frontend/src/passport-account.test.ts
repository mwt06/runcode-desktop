import { describe, expect, it, vi } from 'vitest'
import {
  canAutoStartPassport,
  createPassportAccountCoordinator,
  eligibleLeafTenants,
  initialPassportAccountSnapshot,
  resolveEligibleTenant,
  type PassportAccountDependencies,
  type PassportAccountSnapshot,
} from './passport-account'
import type { PassportStatus, PassportTenant } from './bridge'

const loggedIn: PassportStatus = { loggedIn: true, userId: 'user-1' }
const tenant = (id: string, parentId?: string): PassportTenant => ({ id, name: id, parentId })

function dependencies(overrides: Partial<PassportAccountDependencies> = {}): PassportAccountDependencies {
  return {
    passportStatus: async () => loggedIn,
    passportTenants: async () => [tenant('only')],
    activeTenant: async () => '',
    setActiveTenant: async () => {},
    passportModels: async () => [{ id: 'model-1', ownedBy: 'test' }],
    errorText: (error) => error instanceof Error ? error.message : String(error),
    ...overrides,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

async function flush() {
  for (let i = 0; i < 8; i++) await Promise.resolve()
}

function last<T>(values: T[]): T | undefined {
  return values[values.length - 1]
}

describe('Passport tenant eligibility', () => {
  it('selects the sole leaf instead of its parent', () => {
    const list = [tenant('parent', 'SuperTenant'), tenant('child', 'parent')]
    expect(eligibleLeafTenants(list).map((item) => item.id)).toEqual(['child'])
    expect(resolveEligibleTenant(list, '')).toMatchObject({ tenantId: 'child', bindingChanged: true })
    expect(resolveEligibleTenant(list, 'parent')).toMatchObject({ tenantId: 'child', bindingChanged: true })
  })

  it('retains a valid leaf and clears an invalid selection when several leaves exist', () => {
    const list = [tenant('parent'), tenant('a', 'parent'), tenant('b', 'parent')]
    expect(resolveEligibleTenant(list, 'b')).toMatchObject({ tenantId: 'b', bindingChanged: false })
    expect(resolveEligibleTenant(list, 'parent')).toMatchObject({ tenantId: '', bindingChanged: true })
    expect(resolveEligibleTenant(list, 'removed')).toMatchObject({ tenantId: '', bindingChanged: true })
  })

  it('keeps orphaned and self-parented records selectable and removes duplicate IDs', () => {
    const list = [tenant('orphan', 'missing'), tenant('self', 'self'), tenant('self', 'self')]
    expect(eligibleLeafTenants(list).map((item) => item.id)).toEqual(['orphan', 'self'])
  })
})

describe('Passport automatic entry', () => {
  const ready: PassportAccountSnapshot = {
    phase: 'ready',
    status: loggedIn,
    tenants: [tenant('only')],
    eligibleTenants: [tenant('only')],
    tenantId: 'only',
    models: [{ id: 'model-1', ownedBy: 'test' }],
    error: '',
  }
  const initial = { provider: 'passport', cwd: 'D:/workspace', model: 'model-1' }

  it('requires a fully resolved eligible tenant and model', () => {
    expect(canAutoStartPassport(initial, ready)).toBe(true)
    expect(canAutoStartPassport(initial, { ...ready, phase: 'resolving' })).toBe(false)
    expect(canAutoStartPassport(initial, { ...ready, tenantId: 'stale' })).toBe(false)
    expect(canAutoStartPassport(initial, { ...ready, models: [] })).toBe(false)
  })

  it('does not treat a custom connection as a Passport auto-entry candidate', () => {
    expect(canAutoStartPassport({ ...initial, provider: 'openai' }, ready)).toBe(false)
  })
})

describe('Passport account coordinator', () => {
  it('persists the sole tenant before loading its models and publishing ready', async () => {
    const binding = deferred<void>()
    const order: string[] = []
    const snapshots: PassportAccountSnapshot[] = []
    const coordinator = createPassportAccountCoordinator(dependencies({
      setActiveTenant: async () => { order.push('bind:start'); await binding.promise; order.push('bind:end') },
      passportModels: async () => { order.push('models'); return [{ id: 'model-1', ownedBy: 'test' }] },
    }), (snapshot) => snapshots.push(snapshot))

    const refresh = coordinator.refresh(loggedIn)
    await flush()
    expect(order).toEqual(['bind:start'])
    expect(last(snapshots)?.phase).toBe('resolving')

    binding.resolve()
    await refresh
    expect(order).toEqual(['bind:start', 'bind:end', 'models'])
    expect(last(snapshots)).toMatchObject({ phase: 'ready', tenantId: 'only' })
  })

  it('does not load models or claim a binding when persistence fails', async () => {
    const passportModels = vi.fn(async () => [])
    const snapshots: PassportAccountSnapshot[] = []
    const coordinator = createPassportAccountCoordinator(dependencies({
      setActiveTenant: async () => { throw new Error('disk full') },
      passportModels,
    }), (snapshot) => snapshots.push(snapshot))

    await coordinator.refresh(loggedIn)
    expect(passportModels).not.toHaveBeenCalled()
    expect(last(snapshots)).toMatchObject({ phase: 'error', tenantId: '', error: '绑定租户失败：disk full' })
  })

  it('keeps the bound tenant but blocks ready when model loading fails', async () => {
    const snapshots: PassportAccountSnapshot[] = []
    const coordinator = createPassportAccountCoordinator(dependencies({
      passportModels: async () => { throw new Error('offline') },
    }), (snapshot) => snapshots.push(snapshot))

    await coordinator.refresh(loggedIn)
    expect(last(snapshots)).toMatchObject({ phase: 'error', tenantId: 'only', models: [], error: '获取平台模型失败：offline' })
  })

  it('drops a stale refresh after a newer logout', async () => {
    const tenants = deferred<PassportTenant[] | null>()
    const snapshots: PassportAccountSnapshot[] = []
    const coordinator = createPassportAccountCoordinator(dependencies({
      passportTenants: () => tenants.promise,
    }), (snapshot) => snapshots.push(snapshot))

    const refresh = coordinator.refresh(loggedIn)
    await flush()
    await coordinator.refresh({ loggedIn: false })
    tenants.resolve([tenant('only')])
    await refresh

    expect(last(snapshots)?.phase).toBe('logged-out')
    expect(snapshots.some((snapshot) => snapshot.phase === 'ready')).toBe(false)
  })

  it('serializes duplicate refreshes and publishes only the newest result', async () => {
    const firstTenants = deferred<PassportTenant[] | null>()
    let tenantCalls = 0
    let concurrent = 0
    let maxConcurrent = 0
    const snapshots: PassportAccountSnapshot[] = []
    const coordinator = createPassportAccountCoordinator(dependencies({
      passportTenants: async () => {
        tenantCalls++
        concurrent++
        maxConcurrent = Math.max(maxConcurrent, concurrent)
        const result = tenantCalls === 1 ? await firstTenants.promise : [tenant('new')]
        concurrent--
        return result
      },
    }), (snapshot) => snapshots.push(snapshot))

    const first = coordinator.refresh(loggedIn)
    await flush()
    const second = coordinator.refresh(loggedIn)
    const third = coordinator.refresh(loggedIn)
    firstTenants.resolve([tenant('old')])
    await Promise.all([first, second, third])

    expect(maxConcurrent).toBe(1)
    expect(tenantCalls).toBe(2)
    expect(snapshots.filter((snapshot) => snapshot.phase === 'ready')).toHaveLength(1)
    expect(last(snapshots)?.tenantId).toBe('new')
  })

  it('rejects selecting a parent without calling the binding command', async () => {
    const setActiveTenant = vi.fn(async () => {})
    const snapshots: PassportAccountSnapshot[] = []
    const coordinator = createPassportAccountCoordinator(dependencies({
      passportTenants: async () => [tenant('parent'), tenant('child', 'parent')],
      setActiveTenant,
    }), (snapshot) => snapshots.push(snapshot))

    await coordinator.refresh(loggedIn)
    setActiveTenant.mockClear()
    await coordinator.selectTenant('parent')

    expect(setActiveTenant).not.toHaveBeenCalled()
    expect(last(snapshots)).toMatchObject({ phase: 'error', error: '请选择有效的末级租户' })
  })

  it('does not publish after disposal', async () => {
    const tenants = deferred<PassportTenant[] | null>()
    const onSnapshot = vi.fn()
    const coordinator = createPassportAccountCoordinator(dependencies({ passportTenants: () => tenants.promise }), onSnapshot)
    const refresh = coordinator.refresh(loggedIn)
    await flush()
    const callsBeforeDispose = onSnapshot.mock.calls.length
    coordinator.dispose()
    tenants.resolve([tenant('only')])
    await refresh
    expect(onSnapshot).toHaveBeenCalledTimes(callsBeforeDispose)
  })

  it('starts from an inert snapshot', () => {
    expect(initialPassportAccountSnapshot()).toMatchObject({ phase: 'idle', tenantId: '', models: [] })
  })
})
