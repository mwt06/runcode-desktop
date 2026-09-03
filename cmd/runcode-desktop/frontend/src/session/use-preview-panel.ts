// 右侧预览栏的全部界面状态：打开的标签页、文件浏览器开合、可拖拽的宽度，以及
// “写完自动预览”开关。只认工作区相对路径——绝对路径到相对路径的换算由调用方
// 在有 cwd 的地方完成，这个 hook 不需要知道工作区在哪。
import { useEffect, useRef, useState, type PointerEvent } from 'react'
import { usePersistentBool, usePersistentNumber } from '@/hooks/use-persistent-state'
import { clampPreviewWidth } from '@/preview/classify'
import { closeTab, openTab, type PreviewTab } from '@/preview/tabs'

export type PreviewPanelState = ReturnType<typeof usePreviewPanel>

// 标签页按会话存。宽度、自动预览开关、浏览器开合是纯界面偏好，几条会话共用。
//
// 为什么标签必须分开：diff 标签里存的是 snapshotId，而复审是拿它去问**聚焦会话**
// 的编辑存储。共用一份的话，从 A 会话打开的 diff 在切到 B 之后会静默失效——
// 面板还在，内容变成"找不到"，而没有任何东西提示你这属于另一条对话。
const EMPTY_TABS: { tabs: PreviewTab[]; active: string | null } = { tabs: [], active: null }

export function usePreviewPanel(sessionID: string) {
  const [bySession, setBySession] = useState<Record<string, { tabs: PreviewTab[]; active: string | null }>>({})
  const cur = bySession[sessionID] ?? EMPTY_TABS
  const tabs = cur.tabs
  const active = cur.active
  const patch = (next: { tabs: PreviewTab[]; active: string | null }) =>
    setBySession((m) => (sessionID ? { ...m, [sessionID]: next } : m))
  const setActive = (key: string | null) => patch({ tabs, active: key })
  const [browseOpen, setBrowseOpen] = useState(false)
  const [autoOpen, toggleAutoOpen] = usePersistentBool('preview.autoOpen', true)
  const [width, setWidth, commitWidth] = usePersistentNumber('preview.width', (stored) =>
    clampPreviewWidth(stored, window.innerWidth),
  )

  // opens counts every preview opened, from any source — the model's open_preview,
  // an artifact card, the file browser. Auto-preview reads it to tell "nothing has
  // been shown this turn" from "something already has", so it never yanks the panel
  // away from what the user (or the model) deliberately put there. A ref, not
  // state: the reader only samples it at turn boundaries, and a counter that
  // re-rendered the whole app on every open would be pure cost.
  const opens = useRef(0)

  const open = (tab: PreviewTab) => {
    opens.current++
    const r = openTab(tabs, active, tab)
    patch({ tabs: r.tabs, active: r.active })
    setBrowseOpen(false)
  }
  const openFile = (relPath: string) => open({ kind: 'file', relPath })
  const openDiff = (snapshotId: string, relPath: string) => open({ kind: 'diff', snapshotId, relPath })
  const close = (key: string) => {
    const r = closeTab(tabs, active, key)
    patch({ tabs: r.tabs, active: r.active })
  }
  // closeAll 清掉**聚焦这条会话**的标签，别人的不动——它对应面板上的「关闭全部」。
  //
  // 它一度是清掉所有会话的：那时"换工作区"意味着关掉当前会话、在新目录重开一条，
  // 而标签存的是工作区相对路径与编辑快照 id，换了目录就全成了坏引用，所以一把清空。
  // 多工作区并行之后这个前提没了——换目录是**加开**一条会话（OpenSession(workspace)），
  // 留在旧目录的那几条还开着，它们的标签依然有效。会话与目录一一对应且终生不变，
  // 于是"标签失效"这件事只会随会话一起消失（dropSession），不再需要全局清空。
  const closeAll = () => patch({ tabs: [], active: null })
  // dropSession 丢掉一条会话的标签——那条会话被关掉时。
  const dropSession = (id: string) =>
    setBySession((m) => {
      if (!(id in m)) return m
      const next = { ...m }
      delete next[id]
      return next
    })

  // 换聚焦会话就把预览面板收起来。
  //
  // 面板里放的东西是**上一条对话**的上下文：某个文件、某次编辑的 diff。切过去之后它
  // 还杵在那儿，说的却是另一回事——而右侧那么大一块，人会默认它跟着左边走。
  //
  // 清的是**切过去那条**的标签，不是切走那条的：这样"切完面板是关着的"这个结论只取决
  // 于这一次切换，不依赖上一次有没有清干净。（后台会话的回合结束时自动预览会开在
  // 当时聚焦的那条名下，所以标签有可能落到一个你还没去过的会话上；按到达清就连这种
  // 情况一起兜住了。）
  //
  // 挂在 hook 里而不是在切换的那几个调用点各写一遍：开新会话、换工作区、点最近对话、
  // 关掉一条之后顺位聚焦——每一条都会改 sessionID，将来再加一条切换路径也不用记得
  // 补这一行。
  const focused = useRef(sessionID)
  useEffect(() => {
    if (focused.current === sessionID) return
    focused.current = sessionID
    setBrowseOpen(false)
    setBySession((m) => (sessionID in m ? { ...m, [sessionID]: EMPTY_TABS } : m))
  }, [sessionID])

  // 拖左边缘调宽：按下记起点，移动时按位移换算(向左拖变宽)，松手才落盘。
  const drag = useRef<{ startX: number; startW: number } | null>(null)
  const dragHandlers = {
    onPointerDown: (e: PointerEvent) => {
      drag.current = { startX: e.clientX, startW: width }
      ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
    },
    onPointerMove: (e: PointerEvent) => {
      if (!drag.current) return
      const dx = drag.current.startX - e.clientX // dragging the left edge leftward grows the pane
      setWidth(Math.min(Math.max(drag.current.startW + dx, 360), Math.floor(window.innerWidth * 0.6)))
    },
    onPointerUp: () => {
      if (drag.current) {
        commitWidth()
        drag.current = null
      }
    },
  }

  return {
    tabs, active, setActive, browseOpen, setBrowseOpen,
    autoOpen, toggleAutoOpen, width, dragHandlers, opens,
    openFile, openDiff, close, closeAll, dropSession,
  }
}
