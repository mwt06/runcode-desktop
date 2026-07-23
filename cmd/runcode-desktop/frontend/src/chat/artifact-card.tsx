import { useState } from 'react'
import { Icon } from '@/ui/icons'
import { classifyPreview, artifactKindLabel, kindIcon } from '@/preview/classify'
import { openExternal, revealInFolder, resolveArtifactPath, copyText } from '@/core/bridge'
import { basename } from '@/core/paths'
import { DiffStat } from '@/ui/badges'
import { Popover } from '@/ui/popover'

async function copyArtifactPath(relPath: string) {
  try {
    const abs = await resolveArtifactPath(relPath)
    await copyText(abs)
  } catch {
    /* best-effort: clipboard/resolve failure is non-fatal */
  }
}

// OpenWithMenu is the "打开方式" dropdown: preview (in-panel), open with the OS
// default app, reveal in the file manager, copy the absolute path.
function OpenWithMenu({ relPath, previewable, onPreview }: { relPath: string; previewable: boolean; onPreview: () => void }) {
  const [open, setOpen] = useState(false)
  const item = 'w-full text-left px-3 py-1.5 hover:bg-surface2 whitespace-nowrap'
  return (
    <div className="relative flex-none">
      <button
        onClick={(e) => { e.stopPropagation(); setOpen((v) => !v) }}
        className="flex items-center gap-1 text-[12px] text-muted hover:text-ink border border-line2 rounded-md px-2 py-1"
      >
        打开方式 <Icon name="chevron-down" size={12} />
      </button>
      <Popover open={open} onClose={() => setOpen(false)} placement="down-right" className="min-w-[168px] text-[12.5px] text-ink">
        <button className={`${item} ${previewable ? '' : 'text-faint cursor-default'}`} disabled={!previewable} onClick={(e) => { e.stopPropagation(); onPreview(); setOpen(false) }}>预览</button>
        <button className={item} onClick={(e) => { e.stopPropagation(); openExternal(relPath).catch(() => {}); setOpen(false) }}>用系统默认程序打开</button>
        <button className={item} onClick={(e) => { e.stopPropagation(); revealInFolder(relPath).catch(() => {}); setOpen(false) }}>在文件夹中显示</button>
        <button className={item} onClick={(e) => { e.stopPropagation(); copyArtifactPath(relPath); setOpen(false) }}>复制路径</button>
      </Popover>
    </div>
  )
}

// ArtifactCard renders one generated/edited file as a clickable type-rail card:
// a kind-colored left rail, a type icon, a monospace filename, the diff, and the
// open-with menu. Clicking opens an in-panel preview when the kind is previewable.
export function ArtifactCard({ relPath, add, del, onOpen, autoOpened }: { relPath: string; add: number; del: number; onOpen: (relPath: string) => void; autoOpened?: boolean }) {
  const { kind } = classifyPreview(relPath)
  const previewable = kind !== 'unsupported'
  const name = basename(relPath)
  return (
    <div
      onClick={() => previewable && onOpen(relPath)}
      className={`group flex items-center gap-2.5 border border-line2 rounded-lg pl-3 pr-2.5 py-2 bg-surface ${previewable ? 'cursor-pointer hover:bg-surface2/40' : ''}`}
    >
      <span className="flex-none text-muted"><Icon name={kindIcon(kind)} size={17} /></span>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 text-[13px] font-medium text-ink font-mono truncate" title={relPath}>
          {name}
          {autoOpened && <span className="flex-none text-[10px] font-semibold rounded px-1.5 py-0.5 text-muted bg-inset">已预览</span>}
        </div>
        <div className="text-[11px] text-faint font-mono">
          {artifactKindLabel(kind)}
          {add + del > 0 && <DiffStat add={add} del={del} className="ml-1.5" />}
        </div>
      </div>
      <span className="flex-none text-[11px] text-faint font-mono opacity-0 group-hover:opacity-100 transition-opacity">打开 →</span>
      <OpenWithMenu relPath={relPath} previewable={previewable} onPreview={() => onOpen(relPath)} />
    </div>
  )
}
