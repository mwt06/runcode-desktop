// FileBrowser lists the workspace as a shallow read-only tree; clicking a file
// asks to preview it (the parent decides if it is previewable).
import { useState, type ReactNode } from 'react'
import { Icon } from '@/ui/icons'
import { buildFileTree, classifyPreview, fileColor, filterFiles, kindIcon, type FileNode } from './classify'

export function FileBrowser({ files, onPick, autoOpen, onToggleAutoOpen }: { files: string[]; onPick: (relPath: string) => void; autoOpen?: boolean; onToggleAutoOpen?: () => void }) {
  const [query, setQuery] = useState('')
  const tree = buildFileTree(filterFiles(files, query))
  const render = (nodes: FileNode[], depth: number): ReactNode =>
    nodes.map((n) =>
      n.dir ? (
        <div key={n.path}>
          <div className="px-2 py-1 text-[12.5px] text-muted font-medium" style={{ paddingLeft: 8 + depth * 12 }}>{n.name}/</div>
          {n.children && render(n.children, depth + 1)}
        </div>
      ) : (
        <div key={n.path} onClick={() => onPick(n.path)} className="flex items-center gap-1.5 px-2 py-1 text-[12.5px] text-ink hover:bg-surface2 cursor-pointer" style={{ paddingLeft: 8 + depth * 12 }} title={n.path}>
          <span className="flex-none" style={{ color: fileColor(n.path) }}><Icon name={kindIcon(classifyPreview(n.path).kind)} size={13} /></span>
          <span className="truncate">{n.name}</span>
        </div>
      ),
    )
  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="flex-none p-2 border-b border-line2">
        {onToggleAutoOpen && (
          <label className="flex items-center gap-1.5 mb-2 text-[12px] text-muted cursor-pointer select-none">
            <input type="checkbox" checked={!!autoOpen} onChange={onToggleAutoOpen} />
            写完自动预览
          </label>
        )}
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="筛选文件…"
          className="w-full text-[12.5px] bg-inset rounded-md px-2.5 py-1.5 outline-none text-ink placeholder:text-faint"
        />
      </div>
      <div className="flex-1 min-h-0 text-[12.5px] py-1 overflow-auto">{render(tree, 0)}</div>
    </div>
  )
}
