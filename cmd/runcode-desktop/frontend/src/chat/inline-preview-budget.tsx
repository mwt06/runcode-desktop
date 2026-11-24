// HTML 活动 iframe 名额：跨消息共享，上限 PREVIEW_LIMITS.liveFrames。
// SVG／Mermaid 不走此名额。视口附近（rootMargin ±300px）才申请；离屏释放。
// 无 IntersectionObserver 时按注册顺序最多挂前 N 个，其余回退源码。
import {
  createContext, useContext, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore,
  type ReactNode, type RefObject,
} from 'react'
import { PREVIEW_LIMITS } from '@/preview/static-detect'

/** 上下各 300px 算「附近」，进入该范围才申请名额。 */
export const HTML_PREVIEW_ROOT_MARGIN = '300px 0px'

type Listener = () => void

/** 名额账本：near 集合 + holders；先到可见者先得，释放后按注册序补位。 */
export class HtmlPreviewBudgetStore {
  readonly limit: number
  private order: string[] = []
  private near = new Set<string>()
  private holders = new Set<string>()
  private listeners = new Set<Listener>()
  private fallback = false // 无 IO 时改走 claimFallback，不再看 near

  constructor(limit: number = PREVIEW_LIMITS.liveFrames) {
    this.limit = limit
  }

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  private emit() {
    for (const fn of this.listeners) fn()
  }

  register(id: string) {
    if (!this.order.includes(id)) this.order.push(id)
  }

  unregister(id: string) {
    this.order = this.order.filter((x) => x !== id)
    this.near.delete(id)
    this.holders.delete(id)
    this.rebalance()
  }

  hasSlot(id: string): boolean {
    return this.holders.has(id)
  }

  /** 有 IntersectionObserver：更新附近状态并重算名额，返回该 id 是否持有名额。 */
  setNear(id: string, near: boolean): boolean {
    this.fallback = false
    this.register(id)
    if (near) this.near.add(id)
    else this.near.delete(id)
    this.rebalance()
    return this.holders.has(id)
  }

  /**
   * 无 IntersectionObserver：按注册顺序最多挂 limit 个。
   * 已持有者保留；新来者仅在有空位时获得。
   */
  claimFallback(id: string): boolean {
    this.fallback = true
    this.register(id)
    if (this.holders.has(id)) return true
    if (this.holders.size < this.limit) {
      this.holders.add(id)
      this.emit()
      return true
    }
    return false
  }

  private rebalance() {
    if (this.fallback) {
      // fallback 模式不按 near 回收，只在 unregister 时释放。
      this.emit()
      return
    }
    for (const id of [...this.holders]) {
      if (!this.near.has(id)) this.holders.delete(id)
    }
    for (const id of this.order) {
      if (this.holders.size >= this.limit) break
      if (this.near.has(id) && !this.holders.has(id)) this.holders.add(id)
    }
    this.emit()
  }

  /** 测试用：当前活动持有者数量。 */
  holderCount(): number {
    return this.holders.size
  }

  /** 测试用：当前持有者 id 列表（注册序）。 */
  holderIds(): string[] {
    return this.order.filter((id) => this.holders.has(id))
  }
}

const BudgetContext = createContext<HtmlPreviewBudgetStore | null>(null)

export function InlinePreviewBudgetProvider({
  children,
  limit = PREVIEW_LIMITS.liveFrames,
  store: external,
}: {
  children: ReactNode
  limit?: number
  /** 测试可注入同一 store 以便断言 holderCount。 */
  store?: HtmlPreviewBudgetStore
}) {
  const owned = useMemo(() => external ?? new HtmlPreviewBudgetStore(limit), [external, limit])
  return <BudgetContext.Provider value={owned}>{children}</BudgetContext.Provider>
}

/**
 * 为单个 HTML 预览申请活动名额。
 * @returns 是否应挂载 iframe；等待名额时为 false（调用方显示占位，不要当成渲染失败）。
 */
export function useHtmlPreviewSlot(ref: RefObject<Element | null>, enabled: boolean): boolean {
  const store = useContext(BudgetContext)
  const id = useId()
  const [, bump] = useState(0)
  const nearRef = useRef(false)

  useEffect(() => {
    if (!store) return
    if (!enabled) {
      store.unregister(id)
      return
    }
    store.register(id)
    const unsub = store.subscribe(() => bump((n) => n + 1))

    if (typeof IntersectionObserver === 'undefined') {
      store.claimFallback(id)
      return () => {
        unsub()
        store.unregister(id)
      }
    }

    const el = ref.current
    const io = new IntersectionObserver(
      (entries) => {
        const near = entries.some((e) => e.isIntersecting)
        nearRef.current = near
        store.setNear(id, near)
        bump((n) => n + 1)
      },
      { root: null, rootMargin: HTML_PREVIEW_ROOT_MARGIN, threshold: 0 },
    )
    if (el) io.observe(el)
    return () => {
      io.disconnect()
      unsub()
      store.unregister(id)
    }
  }, [store, enabled, id, ref])

  return useSyncExternalStore(
    store ? store.subscribe : () => () => {},
    () => (enabled && store ? store.hasSlot(id) : false),
    () => false,
  )
}
