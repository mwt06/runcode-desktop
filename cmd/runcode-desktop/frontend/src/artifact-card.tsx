import { useState } from 'react'
import { Icon } from './icons'
import { classifyPreview, artifactKindLabel, kindIcon } from './preview'
import { openExternal, revealInFolder, resolveArtifactPath, copyText } from './bridge'

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
      {open && (
        <>
          <div className="fixed inset-0 z-10" onClick={(e) => { e.stopPropagation(); setOpen(false) }} />
          <div className="absolute right-0 mt-1 z-20 min-w-[168px] bg-surface border border-line2 rounded-lg shadow-card py-1 text-[12.5px] text-ink">
            <button className={`${item} ${previewable ? '' : 'text-faint cursor-default'}`} disabled={!previewable} onClick={(e) => { e.stopPropagation(); onPreview(); setOpen(false) }}>预览</button>
            <button className={item} onClick={(e) => { e.stopPropagation(); openExternal(relPath); setOpen(false) }}>用系统默认程序打开</button>
            <button className={item} onClick={(e) => { e.stopPropagation(); revealInFolder(relPath); setOpen(false) }}>在文件夹中显示</button>
            <button className={item} onClick={(e) => { e.stopPropagation(); copyArtifactPath(relPath); setOpen(false) }}>复制路径</button>
          </div>
        </>
      )}
    </div>
  )
}

// ArtifactCard renders one generated/edited file as a clickable card in the
// conversation: type icon, filename, type subtitle + diff, and the open-with menu.
// Clicking the card opens an in-panel preview (when the type is previewable).
export function ArtifactCard({ relPath, add, del, onOpen }: { relPath: string; add: number; del: number; onOpen: (relPath: string) => void }) {
  const { kind } = classifyPreview(relPath)
  const previewable = kind !== 'unsupported'
  const name = relPath.replace(/\\/g, '/').split('/').pop() || relPath
  return (
    <div
      onClick={() => previewable && onOpen(relPath)}
      className={`flex items-center gap-2.5 border border-line2 rounded-xl px-3 py-2 bg-surface ${previewable ? 'cursor-pointer hover:border-primary/50 hover:bg-surface2/40' : ''}`}
    >
      <span className="flex-none w-8 h-8 rounded-lg bg-inset flex items-center justify-center text-muted"><Icon name={kindIcon(kind)} size={16} /></span>
      <div className="flex-1 min-w-0">
        <div className="text-[13px] font-medium text-ink truncate" title={relPath}>{name}</div>
        <div className="text-[11px] text-faint">
          {artifactKindLabel(kind)}
          {add + del > 0 && (
            <span className="ml-2 font-mono"><span className="text-green">+{add}</span> <span className={del > 0 ? 'text-red' : 'text-faint'}>−{del}</span></span>
          )}
        </div>
      </div>
      <OpenWithMenu relPath={relPath} previewable={previewable} onPreview={() => onOpen(relPath)} />
    </div>
  )
}
