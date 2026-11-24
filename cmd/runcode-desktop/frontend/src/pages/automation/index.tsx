import { useCallback, useEffect, useState } from 'react'
import { Banner } from '@/ui/feedback'
import type { AutomationController } from '@/session/use-automation'
import { browserTimeZone, defaultAutomationDraft } from './draft'
import { TaskDetail } from './task-detail'
import { TaskForm } from './task-form'
import { TaskList } from './task-list'

export function AutomationManager({ auto, onDirty, onCancelCreate, onOpenSettings, onOpenRun }: {
  auto: AutomationController
  onDirty?: (dirty: boolean) => void
  onCancelCreate?: () => void
  onOpenSettings?: () => void
  onOpenRun: (req: { context: NonNullable<AutomationController['context']>; taskId: string; runId: string }) => Promise<void>
}) {
  const [initial, setInitial] = useState<ReturnType<typeof defaultAutomationDraft> | null>(null)
  const markDirty = useCallback((dirty: boolean) => onDirty?.(dirty), [onDirty])
  const zone = browserTimeZone()

  useEffect(() => {
    if (!auto.createOpen) {
      setInitial(null)
      return
    }
    setInitial((current) => current ?? defaultAutomationDraft(
      new Date(), zone.zone, auto.defaults?.recentWorkspaces ?? [], auto.defaults?.currentWorkspace,
    ))
  }, [auto.createOpen, auto.defaults, zone.zone])

  const modelName = auto.defaults?.model?.name
  const canSave = !!auto.defaults?.available && !!modelName

  return (
    <div className="relative flex-1 min-h-0">
      <div className="h-full overflow-y-auto overscroll-contain">
        {auto.notice && <div className="px-5 pt-4"><Banner tone="warning">{auto.notice}</Banner></div>}
        {auto.defaults?.available && !auto.defaults.backgroundSupported && (
          <div className="px-5 pt-4">
            <Banner tone="warning">当前平台不支持后台驻留，关闭客户端后任务将停止。</Banner>
          </div>
        )}
        <TaskList auto={auto} />
      </div>
      {auto.createOpen && initial && (
        <aside className="automation-drawer absolute inset-y-0 right-0 z-10 bg-surface border-l border-line2 shadow-card overflow-hidden" aria-label="新建自动化任务">
          <TaskForm
            initial={initial}
            recent={auto.defaults?.recentWorkspaces ?? []}
            currentWorkspace={auto.defaults?.currentWorkspace}
            zoneNote={zone.fallback ? '无法识别当前时区，已使用 Asia/Shanghai' : undefined}
            canSave={canSave}
            onSave={auto.saveCreate}
            onCancel={onCancelCreate ?? auto.closeCreate}
            onDirty={markDirty}
            onOpenSettings={onOpenSettings}
          />
        </aside>
      )}
      {auto.selectedTask && auto.context && !auto.createOpen && (
        <aside className="automation-drawer absolute inset-y-0 right-0 z-10 bg-surface border-l border-line2 shadow-card overflow-hidden" aria-label="任务详情">
          <TaskDetail
            task={auto.selectedTask}
            context={auto.context}
            recent={auto.defaults?.recentWorkspaces ?? []}
            currentWorkspace={auto.defaults?.currentWorkspace}
            pending={auto.pendingTaskId === auto.selectedTask.id}
            onSave={(draft, expectedRevision) => auto.updateTask(auto.selectedTask!.id, expectedRevision, draft)}
            onSetPaused={(paused) => auto.setPaused(auto.selectedTask!, paused).then(() => undefined)}
            onRunNow={() => auto.runNow(auto.selectedTask!)}
            onDelete={() => auto.deleteTask(auto.selectedTask!)}
            onClose={onCancelCreate ?? (() => auto.selectTask(''))}
            onDirty={markDirty}
            onOpenRun={(taskId, runId) => onOpenRun({
              context: auto.context!,
              taskId,
              runId,
            })}
          />
        </aside>
      )}
    </div>
  )
}
