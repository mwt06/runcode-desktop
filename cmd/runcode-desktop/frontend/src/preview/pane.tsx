// 预览侧栏的外壳：顶部标签条 + 当前标签对应的面板（文件预览或编辑审核）。
import { Icon } from '@/ui/icons'
import { HScroll } from '@/ui/h-scroll'
import { basename } from '@/core/paths'
import { classifyPreview, fileColor, kindIcon } from './classify'
import { tabKey, type PreviewTab } from './tabs'
import { PreviewPanel } from './file-panel'
import { DiffPanel } from './diff-panel'

// PreviewTabs is the tab strip above the preview: one tab per open file/diff. Lines
// are dropped; the active tab reads as a soft rounded background pill. Each file tab
// carries its color-coded type icon (diff tabs use a neutral diff glyph).
export function PreviewTabs({ tabs, active, onSelect, onClose }: { tabs: PreviewTab[]; active: string | null; onSelect: (key: string) => void; onClose: (key: string) => void }) {
  return (
    <HScroll className="flex-none flex items-center h-[38px] px-1.5 bg-surface" rowClassName="gap-0.5">
      {tabs.map((t) => {
        const key = tabKey(t)
        const name = basename(t.relPath)
        const on = key === active
        const iconName = t.kind === 'diff' ? 'diff' : kindIcon(classifyPreview(t.relPath).kind)
        const iconColor = t.kind === 'diff' ? undefined : fileColor(t.relPath)
        return (
          <div
            key={key}
            onClick={() => onSelect(key)}
            title={t.relPath}
            className={`group flex items-center gap-1.5 pl-2 pr-1.5 h-[28px] max-w-[190px] flex-none cursor-pointer rounded-md text-[13px] ${on ? 'bg-surface2 text-ink' : 'text-muted hover:bg-surface2/60 hover:text-ink'}`}
          >
            <span className="flex-none" style={iconColor ? { color: iconColor } : undefined}><Icon name={iconName} size={13} /></span>
            <span className="truncate">{name}</span>
            <button
              className="flex-none w-4 h-4 flex items-center justify-center rounded opacity-0 group-hover:opacity-100 hover:bg-line2 hover:text-ink"
              onClick={(e) => { e.stopPropagation(); onClose(key) }}
            >
              <Icon name="win-close" size={10} />
            </button>
          </div>
        )
      })}
    </HScroll>
  )
}

// PreviewPane composes the tab strip with the active tab's panel (file or diff).
export function PreviewPane({ tabs, active, baseURL, onSelect, onClose, onCloseTab }: { tabs: PreviewTab[]; active: string | null; baseURL: string; onSelect: (key: string) => void; onClose: () => void; onCloseTab: (key: string) => void }) {
  const activeTab = tabs.find((t) => tabKey(t) === active) ?? null
  return (
    <div className="flex flex-col h-full min-h-0">
      <PreviewTabs tabs={tabs} active={active} onSelect={onSelect} onClose={onCloseTab} />
      <div className="flex-1 min-h-0">
        {activeTab?.kind === 'file' && <PreviewPanel key={active} baseURL={baseURL} relPath={activeTab.relPath} onClose={onClose} />}
        {activeTab?.kind === 'diff' && <DiffPanel key={active} snapshotId={activeTab.snapshotId} relPath={activeTab.relPath} onClose={onClose} />}
      </div>
    </div>
  )
}
