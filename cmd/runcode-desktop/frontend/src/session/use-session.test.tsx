// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { SessionInfo, StartSessionRequest } from '@/core/bridge'

const api = vi.hoisted(() => ({
  startSession: vi.fn(), loadConfig: vi.fn(), openSessions: vi.fn(), listSessions: vi.fn(),
  listeners: new Map<string, (env: { payload: SessionInfo }) => void>(),
}))
vi.mock('@/core/bridge', async (original) => ({
  ...await original<typeof import('@/core/bridge')>(), ...api,
  onEnvelope: (name: string, cb: (env: { payload: SessionInfo }) => void) => {
    api.listeners.set(name, cb)
    return () => api.listeners.delete(name)
  },
}))
import { useSession, type Session } from './use-session'

let root: Root, host: HTMLDivElement, session: Session
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  api.listeners.clear()
  api.loadConfig.mockResolvedValue({})
  api.openSessions.mockResolvedValue([])
  api.listSessions.mockResolvedValue([])
  host = document.createElement('div')
  root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()) })

function Harness() {
  session = useSession({ busy: false, conversation: {
    reset: vi.fn(), applyResumed: vi.fn(), pushError: vi.fn(), dropSession: vi.fn(),
  }, showToast: vi.fn(), onEnterChat: vi.fn() })
  return null
}
const info = (sessionId: string, model = 'old') => ({ sessionId, model }) as SessionInfo
async function emit(value: SessionInfo) {
  await act(async () => api.listeners.get('session:status')!({ payload: value }))
}

it('keeps foreground B when background A rebuilds, but adopts updates for B', async () => {
  api.startSession.mockResolvedValue(info('B'))
  await act(async () => root.render(<Harness />))
  await act(async () => session.start({} as StartSessionRequest))
  await emit(info('A', 'background-model'))
  expect(session.info).toEqual(info('B'))
  await emit(info('B', 'updated-model'))
  expect(session.info).toEqual(info('B', 'updated-model'))
})

it('does not select an unsolicited background session before start or after logout', async () => {
  await act(async () => root.render(<Harness />))
  await emit(info('A'))
  expect(session.info).toBeNull()
  api.startSession.mockResolvedValue(info('B'))
  await act(async () => session.start({} as StartSessionRequest))
  await act(async () => session.returnToStart())
  await emit(info('A'))
  expect(session.info).toBeNull()
  expect(session.started).toBe(false)
})
