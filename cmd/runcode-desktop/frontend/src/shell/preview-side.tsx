// 右侧预览栏：一根可拖拽的分隔条 + 面板本体。有打开的标签页时显示标签页，
// 否则显示文件浏览器（"预览"按钮打开的那个）。
import { type PointerEvent } from 'react'
import { PreviewPane } from '@/preview/pane'
import { FileBrowser } from '@/preview/file-browser'
import { type PreviewTab } from '@/preview/tabs'

export function PreviewSide({ tabs, active, baseURL, width, dragHandlers, files, autoOpen, onToggleAutoOpen, onSelect, onCloseTab, onCloseAll, onCloseBrowser, onPickFile }: {
  tabs: PreviewTab[]
  active: string | null
  baseURL: string
  width: number
  dragHandlers: {
    onPointerDown: (e: PointerEvent) => void
    onPointerMove: (e: PointerEvent) => void
    onPointerUp: () => void
  }
  files: string[]
  autoOpen: boolean
  onToggleAutoOpen: () => void
  onSelect: (key: string) => void
  onCloseTab: (key: string) => void
  onCloseAll: () => void
  onCloseBrowser: () => void
  onPickFile: (path: string) => void
}) {
  return (
    <>
      <div
        className="w-[6px] flex-none cursor-col-resize bg-surface2 hover:bg-line2 active:bg-primary/40 transition-colors"
        {...dragHandlers}
      />
      <aside style={{ width, maxWidth: '60%' }} className="flex-none flex flex-col min-h-0 bg-surface">
        {tabs.length > 0 ? (
          <PreviewPane
            tabs={tabs}
            active={active}
            baseURL={baseURL}
            onSelect={onSelect}
            onCloseTab={onCloseTab}
            onClose={onCloseAll}
          />
        ) : (
          <div className="flex flex-col h-full min-h-0">
            <div className="flex-none flex items-center h-[44px] px-3 border-b border-line2">
              <span className="text-[13px] font-medium text-ink flex-1">文件预览</span>
              <button className="text-muted hover:text-ink px-1.5" title="关闭" onClick={onCloseBrowser}>✕</button>
            </div>
            <div className="flex-1 min-h-0">
              <FileBrowser files={files} onPick={onPickFile} autoOpen={autoOpen} onToggleAutoOpen={onToggleAutoOpen} />
            </div>
          </div>
        )}
      </aside>
    </>
  )
}
