// 右侧预览栏的全部界面状态：打开的标签页、文件浏览器开合、可拖拽的宽度，以及
// “写完自动预览”开关。只认工作区相对路径——绝对路径到相对路径的换算由调用方
// 在有 cwd 的地方完成，这个 hook 不需要知道工作区在哪。
import { useRef, useState, type PointerEvent } from 'react'
import { usePersistentBool, usePersistentNumber } from '@/hooks/use-persistent-state'
import { clampPreviewWidth } from '@/preview/classify'
import { closeTab, openTab, type PreviewTab } from '@/preview/tabs'

export type PreviewPanelState = ReturnType<typeof usePreviewPanel>

export function usePreviewPanel() {
  const [tabs, setTabs] = useState<PreviewTab[]>([])
  const [active, setActive] = useState<string | null>(null)
  const [browseOpen, setBrowseOpen] = useState(false)
  const [autoOpen, toggleAutoOpen] = usePersistentBool('preview.autoOpen', true)
  const [width, setWidth, commitWidth] = usePersistentNumber('preview.width', (stored) =>
    clampPreviewWidth(stored, window.innerWidth),
  )

  const open = (tab: PreviewTab) => {
    const r = openTab(tabs, active, tab)
    setTabs(r.tabs)
    setActive(r.active)
    setBrowseOpen(false)
  }
  const openFile = (relPath: string) => open({ kind: 'file', relPath })
  const openDiff = (snapshotId: string, relPath: string) => open({ kind: 'diff', snapshotId, relPath })
  const close = (key: string) => {
    const r = closeTab(tabs, active, key)
    setTabs(r.tabs)
    setActive(r.active)
  }
  const closeAll = () => {
    setTabs([])
    setActive(null)
  }

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
    autoOpen, toggleAutoOpen, width, dragHandlers,
    openFile, openDiff, close, closeAll,
  }
}
