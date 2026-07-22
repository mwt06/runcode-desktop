import { useState } from 'react'
import { Icon } from './icons'
import type { EditRecord } from './bridge'
import { CollapsibleGroup, DiffStat } from './components'
import { basename } from './paths'

// EditedCard is the "已编辑" card for one edited file: an edit icon, the filename,
// the accurate +N -N, and 撤销 / 审核 actions. Undo uses an inline confirm (no
// native dialog) — cross-platform and on-brand. A reverted card goes grey.
function EditedCard({ rec, reverted, onReview, onUndo }: { rec: EditRecord; reverted: boolean; onReview: () => void; onUndo: () => void }) {
  const [confirming, setConfirming] = useState(false)
  const name = basename(rec.relPath)
  return (
    <div className={`flex items-center gap-2.5 border border-line2 rounded-lg pl-3 pr-2.5 py-2 bg-surface ${reverted ? 'opacity-60' : ''}`}>
      <span className="flex-none text-muted"><Icon name="file-edit" size={17} /></span>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 text-[13px] font-medium text-ink font-mono truncate" title={rec.relPath}>
          <span className="text-faint font-sans font-normal text-[11.5px] flex-none">已编辑</span>
          {name}
        </div>
        <div className="text-[11px] font-mono">
          <DiffStat add={rec.added} del={rec.removed} />
          {rec.created && <span className="text-faint ml-1.5">新建</span>}
        </div>
      </div>
      {reverted ? (
        <span className="flex-none text-[11px] text-faint">已撤销</span>
      ) : confirming ? (
        <span className="flex-none flex items-center gap-1.5 text-[12px]">
          <span className="text-muted">确认撤销?</span>
          <button className="text-red hover:underline" onClick={() => { setConfirming(false); onUndo() }}>是</button>
          <button className="text-muted hover:underline" onClick={() => setConfirming(false)}>否</button>
        </span>
      ) : (
        <span className="flex-none flex items-center gap-2.5 text-[12px] text-muted">
          <button className="hover:text-ink flex items-center gap-1" onClick={() => setConfirming(true)}>撤销 <Icon name="undo" size={12} /></button>
          <button className="hover:text-ink" onClick={onReview}>审核</button>
        </span>
      )}
    </div>
  )
}

// EditedCards renders one EditedCard per edited file in a turn, collapsing to a
// summary row past two files via the shared CollapsibleGroup. The summary's `extra`
// slot carries the aggregate +N −N across all edited files.
export function EditedCards({ edits, reverted, onReview, onUndo }: { edits: EditRecord[]; reverted: Set<string>; onReview: (snapshotId: string, relPath: string) => void; onUndo: (snapshotId: string) => void }) {
  if (edits.length === 0) return null
  const totalAdded = edits.reduce((s, e) => s + e.added, 0)
  const totalRemoved = edits.reduce((s, e) => s + e.removed, 0)
  return (
    <CollapsibleGroup
      icon="pencil"
      label="已编辑文件"
      count={edits.length}
      extra={<DiffStat add={totalAdded} del={totalRemoved} className="font-mono text-[11.5px] tabular-nums flex-none" />}
    >
      {edits.map((e) => (
        <EditedCard key={e.toolUseId} rec={e} reverted={reverted.has(e.snapshotId)} onReview={() => onReview(e.snapshotId, e.relPath)} onUndo={() => onUndo(e.snapshotId)} />
      ))}
    </CollapsibleGroup>
  )
}
