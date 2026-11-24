import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import type { AutoSaveChannel } from './autosave-channel'

export type SettingsSaveError = {
  key: string
  label: string
  message: string
}

type Registered = {
  label: string
  channel: AutoSaveChannel<object>
  unsubscribe: () => void
}

export type SettingsSaveRegistry = {
  getChannel<T extends object>(key: string): AutoSaveChannel<T> | undefined
  register<T extends object>(key: string, label: string, channel: AutoSaveChannel<T>): () => void
  flushAll(): Promise<boolean>
  errors: readonly SettingsSaveError[]
}

const SettingsSaveContext = createContext<SettingsSaveRegistry | null>(null)

function asObjectChannel<T extends object>(channel: AutoSaveChannel<T>): AutoSaveChannel<object> {
  return channel as unknown as AutoSaveChannel<object>
}

export function SettingsSaveProvider({ children }: { children: ReactNode }) {
  const channels = useRef(new Map<string, Registered>())
  const [errors, setErrors] = useState<SettingsSaveError[]>([])

  const syncError = useCallback((key: string, label: string, channel: AutoSaveChannel<object>) => {
    const snapshot = channel.getSnapshot()
    const message = snapshot.phase === 'error' || snapshot.phase === 'invalid' ? snapshot.error : ''
    setErrors((current) => {
      const without = current.filter((error) => error.key !== key)
      if (!message) return without.length === current.length ? current : without
      const next = { key, label, message }
      const existing = current.find((error) => error.key === key)
      if (existing && existing.label === label && existing.message === message) return current
      return [...without, next]
    })
  }, [])

  const register = useCallback(<T extends object>(key: string, label: string, channel: AutoSaveChannel<T>) => {
    const existing = channels.current.get(key)
    existing?.unsubscribe()
    const typed = asObjectChannel(channel)
    const registered: Registered = {
      label,
      channel: typed,
      unsubscribe: channel.subscribe(() => syncError(key, label, typed)),
    }
    channels.current.set(key, registered)
    // 注册发生在子组件渲染期；错误同步推迟到微任务，避免在渲染期间更新 Provider。
    queueMicrotask(() => {
      if (channels.current.get(key)?.channel === typed) syncError(key, label, typed)
    })
    return () => {
      const current = channels.current.get(key)
      if (!current || current.channel !== typed) return
      current.unsubscribe()
      channels.current.delete(key)
      setErrors((items) => items.filter((error) => error.key !== key))
    }
  }, [syncError])

  const getChannel = useCallback(<T extends object>(key: string) => (
    channels.current.get(key)?.channel as AutoSaveChannel<T> | undefined
  ), [])

  const flushAll = useCallback(async () => {
    const pending = Array.from(channels.current.values(), (entry) => entry.channel.flush())
    const results = await Promise.all(pending)
    return results.every(Boolean)
  }, [])

  const value = useMemo<SettingsSaveRegistry>(() => ({
    getChannel,
    register,
    flushAll,
    errors,
  }), [errors, flushAll, getChannel, register])

  return <SettingsSaveContext.Provider value={value}>{children}</SettingsSaveContext.Provider>
}

export function useSettingsSaveRegistry(): SettingsSaveRegistry {
  const value = useContext(SettingsSaveContext)
  if (!value) throw new Error('useSettingsSaveRegistry must be used inside SettingsSaveProvider')
  return value
}
