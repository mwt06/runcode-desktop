// 设置页目录的滚动定位与当前项跟随。只碰 PageShell 那一个滚动根：定位用它的偏移、
// 它的 scrollTo，从不调 window.scrollTo 或不限定根的 scrollIntoView——那会连带滚动
// 聊天侧栏乃至整个窗口。不调 bridge、不持有业务状态，也不把位置写进本地存储或 URL：
// 每次进入设置页都从第一节开始。
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import {
  isNarrowLayout,
  resolveActiveSection,
  resolveVisibleSection,
  type SectionPosition,
  type SettingsSectionId,
} from './navigation-model'

// 宽布局：目标标题停在滚动视口顶边下 24px。窄布局：吸顶目录条实测高度 + 12px。
const WIDE_TOP_INSET = 24
const COMPACT_GAP = 12
// 到底判定的容差，见 resolveActiveSection。
const BOTTOM_TOLERANCE = 2
// 定位后标题理论上正好压在定位线上，高 DPI 下 scrollTop 会有亚像素取整；不放这 1px，
// 刚点过的那一节在下一次重算时可能被判成"还没越线"而选中上一节。
const LINE_TOLERANCE = 1
// 认领程序滚动事件、判断"已在目标偏移"时的容差，理由同上。
const OFFSET_TOLERANCE = 1

export function useSettingsNavigation({ visibleIds }: { visibleIds: readonly SettingsSectionId[] }) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const layoutRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const compactNavRef = useRef<HTMLDivElement>(null)
  const sections = useRef(new Map<SettingsSectionId, HTMLElement>())

  // 调用方每次渲染都会给一个新数组；按内容收敛成稳定引用，免得每次渲染都重跑回落。
  const visibleKey = visibleIds.join('|')
  const visible = useMemo(
    () => (visibleKey ? (visibleKey.split('|') as SettingsSectionId[]) : []),
    [visibleKey],
  )
  const visibleRef = useRef(visible)

  const [narrow, setNarrow] = useState(false)
  const narrowRef = useRef(false)
  const [activeId, setActiveId] = useState<SettingsSectionId | null>(() => visible[0] ?? null)

  // 点击目录后高亮钉在点击目标上，直到下一次用户滚动。页尾目标会被滚动上限截住、
  // 标题到不了定位线，按几何算会立刻变成别的节——用户刚点的那项不能被这样改掉。
  const pinned = useRef(false)
  // 本次程序滚动的目标偏移：滚动事件是异步来的，靠它认出"这是我们自己滚的"。
  // 不用定时器锁：多慢的机器上事件都可能晚于任何固定毫秒数。
  const pendingTop = useRef<number | null>(null)
  const frame = useRef<number | null>(null)

  const topInset = useCallback(() => {
    if (!narrowRef.current) return WIDE_TOP_INSET
    const bar = compactNavRef.current
    return (bar ? bar.getBoundingClientRect().height : 0) + COMPACT_GAP
  }, [])

  const applyNarrow = useCallback((width: number) => {
    const next = isNarrowLayout(width)
    if (next === null || next === narrowRef.current) return
    narrowRef.current = next
    setNarrow(next)
  }, [])

  // 测量只读几何、只写目录状态与一个本地 CSS 变量，从不滚动、从不移焦点。
  const measure = useCallback(() => {
    frame.current = null
    const root = scrollRef.current
    if (!root) return
    const layout = layoutRef.current
    if (layout) {
      applyNarrow(layout.clientWidth)
      if (root.clientHeight > 0) layout.style.setProperty('--settings-root-h', `${root.clientHeight}px`)
    }
    if (pinned.current) return
    const rootTop = root.getBoundingClientRect().top
    const positions: SectionPosition[] = []
    for (const id of visibleRef.current) {
      const el = sections.current.get(id)
      if (el) positions.push({ id, top: el.getBoundingClientRect().top - rootTop })
    }
    const atBottom = root.scrollTop + root.clientHeight >= root.scrollHeight - BOTTOM_TOLERANCE
    const next = resolveActiveSection(positions, topInset() + LINE_TOLERANCE, atBottom)
    if (next) setActiveId(next)
  }, [applyNarrow, topInset])

  const schedule = useCallback(() => {
    if (frame.current !== null) return
    frame.current = requestAnimationFrame(measure)
  }, [measure])

  // 首帧前先定宽窄，窄窗打开设置页时不会先闪一帧双列。
  useLayoutEffect(() => {
    applyNarrow(layoutRef.current?.clientWidth ?? 0)
  }, [applyNarrow])

  useEffect(() => {
    const root = scrollRef.current
    if (!root) return
    const onScroll = () => {
      const pending = pendingTop.current
      pendingTop.current = null
      if (pending !== null && Math.abs(root.scrollTop - pending) <= OFFSET_TOLERANCE) return
      pinned.current = false
      schedule()
    }
    root.addEventListener('scroll', onScroll, { passive: true })
    schedule()
    return () => {
      root.removeEventListener('scroll', onScroll)
      if (frame.current !== null) {
        cancelAnimationFrame(frame.current)
        frame.current = null
      }
    }
  }, [schedule])

  // 异步内容增高、错误提示出现、折叠区展开、窗口改尺寸都会落到这几个盒子上。窄目录条
  // 只在窄布局存在，所以随 narrow 重新订阅。
  useEffect(() => {
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => schedule())
    for (const el of [scrollRef.current, layoutRef.current, contentRef.current, compactNavRef.current]) {
      if (el) ro.observe(el)
    }
    return () => ro.disconnect()
  }, [schedule, narrow])

  // 当前项被隐藏（审核能力读取失败/不支持）时按清单顺序回落，只改目录状态。
  useEffect(() => {
    visibleRef.current = visible
    setActiveId((prev) => (prev ? resolveVisibleSection(prev, visible) : visible[0] ?? null))
  }, [visible])

  const registerSection = useCallback((id: SettingsSectionId, node: HTMLElement | null) => {
    if (node) sections.current.set(id, node)
    else sections.current.delete(id)
  }, [])

  const navigateTo = useCallback((id: SettingsSectionId) => {
    const root = scrollRef.current
    const target = sections.current.get(id)
    if (!root || !target || !visibleRef.current.includes(id)) return
    const max = Math.max(0, root.scrollHeight - root.clientHeight)
    const wanted = root.scrollTop + target.getBoundingClientRect().top - root.getBoundingClientRect().top - topInset()
    const top = Math.min(max, Math.max(0, wanted))
    pinned.current = true
    setActiveId(id)
    // 已经在这个偏移就不会有滚动事件；此时若有上一次点击的事件还在路上，它的
    // pendingTop 必然就是这个偏移，留着让它被认领，否则不设，免得吞掉用户的下一次滚动。
    if (Math.abs(root.scrollTop - top) > OFFSET_TOLERANCE) {
      const before = root.scrollTop
      pendingTop.current = top
      if (typeof root.scrollTo === 'function') root.scrollTo({ top, behavior: 'instant' })
      else root.scrollTop = top
      if (Math.abs(root.scrollTop - before) <= OFFSET_TOLERANCE) pendingTop.current = null
    }
    target.focus({ preventScroll: true })
  }, [topInset])

  return { scrollRef, layoutRef, contentRef, compactNavRef, narrow, activeId, registerSection, navigateTo }
}
