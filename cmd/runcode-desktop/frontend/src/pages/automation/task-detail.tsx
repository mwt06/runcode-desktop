import { useEffect, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { errText, type AutomationContext, type AutomationTask, type AutomationTaskDraft } from '@/core/bridge'
import { Banner, InlineError } from '@/ui/feedback'
import { ConfirmDialog } from '@/ui/confirm-dialog'
import { FIELD_CLS, LABEL_CLS, SelectField } from '@/ui/fields'
import { Icon } from '@/ui/icons'
import { isComposingKey } from '@/ui/keys'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { EXECUTION_MODES, draftFromTask, scheduleForSave, validateAutomationDraft } from './draft'
import { RunList } from './run-list'
import { ScheduleFields } from './schedule-fields'
import { formatNextRun, runIsActive, taskStatusLine } from './task-list'
import { WorkspacePicker } from './workspace-picker'

const ICON_BTN = 'flex-none w-8 h-8 inline-flex items-center justify-center rounded-btn border border-line text-muted hover:text-ink hover:bg-surface2 disabled:opacity-40 disabled:cursor-default'

export function TaskDetail({
  task, context, recent, currentWorkspace, pending, onSave, onSetPaused, onRunNow, onDelete,
  onClose, onDirty, onOpenRun,
}: {
  task: AutomationTask
  context: AutomationContext
  recent: string[]
  currentWorkspace?: string
  pending: boolean
  onSave: (draft: AutomationTaskDraft, expectedRevision: number) => Promise<AutomationTask>
  onSetPaused: (paused: boolean) => Promise<void>
  onRunNow: () => Promise<void>
  onDelete: () => Promise<void>
  onClose: () => void
  onDirty?: (dirty: boolean) => void
  onOpenRun: (taskId: string, runId: string) => void
}) {
  const [tab, setTab] = useState<'rules' | 'runs'>('rules')
  const [draft, setDraft] = useState(() => draftFromTask(task))
  const [saved, setSaved] = useState(() => draftFromTask(task))
  const [baseline, setBaseline] = useState(task.revision)
  const [conflict, setConflict] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [action, setAction] = useState<'' | 'pause' | 'run' | 'delete'>('')
  const [moreOpen, setMoreOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved)
  const active = runIsActive(task.lastRun?.status)
  const locked = active
  const linkedRunId = task.lastLinkedRunId || (task.lastRun?.sessionId ? task.lastRun.id : '')

  useEffect(() => { onDirty?.(dirty) }, [dirty, onDirty])

  useEffect(() => {
    if (task.revision === baseline) return
    if (dirty) {
      setConflict(true)
      return
    }
    const next = draftFromTask(task)
    setDraft(next)
    setSaved(next)
    setBaseline(task.revision)
    setConflict(false)
  }, [task, baseline, dirty])

  async function save() {
    if (saving || locked || pending || !dirty) return
    const next: AutomationTaskDraft = {
      ...draft,
      name: draft.name.trim(),
      prompt: draft.prompt.trim(),
      workspace: draft.workspace.trim(),
      schedule: scheduleForSave(draft.schedule),
    }
    const found = validateAutomationDraft(next)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    setSaving(true)
    setError('')
    try {
      const updated = await onSave(next, baseline)
      const fresh = draftFromTask(updated)
      setDraft(fresh)
      setSaved(fresh)
      setBaseline(updated.revision)
      setConflict(false)
    } catch (e) {
      const text = errText(e)
      setError(text)
      if (text.includes('请刷新')) setConflict(true)
    } finally {
      setSaving(false)
    }
  }

  function discard() {
    setDraft(saved)
    setErrors({})
    setError('')
  }

  function refresh() {
    const next = draftFromTask(task)
    setDraft(next)
    setSaved(next)
    setBaseline(task.revision)
    setConflict(false)
    setError('')
  }

  async function togglePaused() {
    if (pending || action || task.state === 'ended') return
    setAction('pause')
    setError('')
    try {
      await onSetPaused(task.state === 'enabled')
    } catch (e) {
      setError(errText(e))
    } finally {
      setAction('')
    }
  }

  async function runNow() {
    if (pending || action || active || task.state === 'ended') return
    setMoreOpen(false)
    setAction('run')
    setError('')
    try {
      await onRunNow()
    } catch (e) {
      setError(errText(e))
    } finally {
      setAction('')
    }
  }

  async function confirmDelete() {
    setDeleteOpen(false)
    setAction('delete')
    setError('')
    try {
      await onDelete()
    } catch (e) {
      setError(errText(e))
      setAction('')
    }
  }

  function onKeyDown(e: ReactKeyboardEvent) {
    if (isComposingKey(e)) return
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      void save()
    }
  }

  return (
    <>
      <form className="flex h-full min-h-0 flex-col overflow-hidden" onKeyDown={onKeyDown} onSubmit={(e) => { e.preventDefault(); void save() }}>
        <div className="flex-none border-b border-line">
          <div className="flex items-center gap-2 px-4 pt-2.5">
            <div className="flex min-w-0 flex-1 gap-4" role="tablist">
              <button type="button" role="tab" aria-selected={tab === 'rules'} className={tabClass(tab === 'rules')} onClick={() => setTab('rules')}>规则</button>
              <button type="button" role="tab" aria-selected={tab === 'runs'} className={tabClass(tab === 'runs')} onClick={() => setTab('runs')}>任务运行记录</button>
            </div>
            <div className="relative flex items-center gap-1.5">
              <button
                type="button"
                className={ICON_BTN}
                disabled={pending || !!action || task.state === 'ended'}
                title={task.state === 'paused' ? '恢复任务' : task.state === 'enabled' ? '暂停任务' : '当前任务已结束'}
                aria-label={task.state === 'paused' ? '恢复任务' : '暂停任务'}
                onClick={() => void togglePaused()}
              >
                <span className="w-5 h-5 rounded-full border border-current inline-flex items-center justify-center">
                  <Icon name={task.state === 'paused' ? 'play' : 'pause'} size={10} />
                </span>
              </button>
              <button
                type="button"
                className={ICON_BTN}
                title="更多操作"
                aria-label="更多操作"
                aria-expanded={moreOpen}
                disabled={pending || !!action}
                onClick={() => setMoreOpen((open) => !open)}
              >
                <Icon name="more" size={16} />
              </button>
              {moreOpen && (
                <>
                  <div className="fixed inset-0 z-10" onClick={() => setMoreOpen(false)} />
                  <div className="absolute right-0 top-full z-20 mt-1 w-36 rounded-btn border border-line2 bg-surface py-1 shadow-card">
                    <button
                      type="button"
                      disabled={active || task.state === 'ended' || pending}
                      className="block w-full px-3 py-2 text-left text-[13px] text-ink hover:bg-surface2 disabled:text-faint"
                      onClick={() => void runNow()}
                    >
                      立即执行
                    </button>
                    <button
                      type="button"
                      disabled={active || pending}
                      className="block w-full px-3 py-2 text-left text-[13px] text-red hover:bg-redbg disabled:text-faint"
                      onClick={() => { setMoreOpen(false); setDeleteOpen(true) }}
                    >
                      删除任务
                    </button>
                  </div>
                </>
              )}
              <button type="button" className={ICON_BTN} title="关闭任务详情" aria-label="关闭任务详情" onClick={onClose}>
                <Icon name="close" size={15} />
              </button>
            </div>
          </div>
          <div className="px-4 pb-3 pt-2">
            <h3 className="m-0 truncate text-[17px] font-semibold text-ink">{task.name}</h3>
            <p className="m-0 mt-0.5 text-[12px] text-muted" data-task-status>{detailStatus(task)}</p>
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 py-4">
          {error && <Banner tone="danger" className="mb-3">{error}</Banner>}
          {tab === 'runs' ? (
            <RunList taskId={task.id} context={context} onOpen={onOpenRun} />
          ) : (
            <div className="flex flex-col gap-3">
              {task.state === 'paused' && active && <Banner tone="warning">已暂停 · 本次执行继续完成</Banner>}
              {locked && <Banner tone="warning">任务正在执行，规则暂时不能修改。暂停只停止以后的调度。</Banner>}
              {conflict && (
                <Banner tone="warning" title="任务已发生变化，请刷新后重试">
                  <button type="button" className="text-primaryink underline" onClick={refresh}>刷新</button>
                </Banner>
              )}
              <label className={LABEL_CLS}>
                自动化任务名称
                <input aria-label="任务名称" disabled={locked} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} className={`${FIELD_CLS} w-full`} />
                {errors.name && <InlineError variant="text">{errors.name}</InlineError>}
              </label>
              <label className={LABEL_CLS}>
                自动化任务内容
                <textarea aria-label="任务内容" disabled={locked} rows={5} value={draft.prompt} onChange={(e) => setDraft({ ...draft, prompt: e.target.value })} className={`${FIELD_CLS} w-full resize-y`} />
                {errors.prompt && <InlineError variant="text">{errors.prompt}</InlineError>}
              </label>
              <label className={LABEL_CLS}>
                执行位置
                <SelectField aria-label="执行位置" disabled={locked} value={draft.executionMode} onChange={(executionMode) => setDraft({ ...draft, executionMode })}>
                  {EXECUTION_MODES.map((mode) => <option key={mode.value} value={mode.value}>{mode.label}</option>)}
                </SelectField>
              </label>
              <div className="flex items-center gap-3 text-[13px] text-muted">
                <span className="flex-none">关联工作区</span>
                <div className="min-w-0 flex-1">
                  <WorkspacePicker
                    value={draft.workspace}
                    current={currentWorkspace}
                    recent={recent}
                    disabled={locked}
                    onChange={(workspace) => setDraft({ ...draft, workspace })}
                  />
                </div>
              </div>
              {errors.workspace && <InlineError variant="text">{errors.workspace}</InlineError>}
              <ScheduleFields value={draft.schedule} disabled={locked} onChange={(schedule) => setDraft({ ...draft, schedule })} />
              {errors.interval && <InlineError variant="text">{errors.interval}</InlineError>}
              {errors.weekdays && <InlineError variant="text">{errors.weekdays}</InlineError>}
              {errors.schedule && <InlineError variant="text">{errors.schedule}</InlineError>}
              <div className="mt-2 flex items-center gap-3 border-t border-line pt-3">
                <div className="min-w-0 flex-1">
                  <div className="text-[13px] font-medium text-ink">关联会话</div>
                  <div data-linked-task-title className="truncate text-[11px] text-muted">{task.name}</div>
                </div>
                <button
                  type="button"
                  disabled={!linkedRunId || pending}
                  title={linkedRunId ? `打开关联会话：${task.linkedSessionTitle || '未命名对话'}` : '暂无关联会话'}
                  className="flex min-w-0 max-w-[190px] items-center gap-1 rounded-field border border-line2 bg-surface2 px-2.5 py-1.5 text-[12px] text-ink2 disabled:text-faint"
                  onClick={() => linkedRunId && onOpenRun(task.id, linkedRunId)}
                >
                  <span data-linked-session-title className="truncate">
                    {linkedRunId ? task.linkedSessionTitle || '未命名对话' : '暂无关联会话'}
                  </span>
                  <Icon name="chevron-down" size={12} className="-rotate-90 flex-none" />
                </button>
              </div>
            </div>
          )}
        </div>

        <footer className="flex flex-none justify-end gap-2 border-t border-line px-4 py-3">
          <button type="button" className={BTN} disabled={!dirty || saving || locked || pending} onClick={discard}>放弃</button>
          <button type="submit" className={`${BTN} ${dirty ? BTN_PRIMARY : ''}`} disabled={!dirty || saving || locked || pending}>
            {saving ? '保存中' : '保存修改'}
          </button>
        </footer>
      </form>
      {deleteOpen && (
        <ConfirmDialog
          title="删除自动化任务？"
          message="删除后不再自动执行。已有运行记录和历史会话会保留。"
          confirmLabel="删除"
          onConfirm={() => void confirmDelete()}
          onCancel={() => setDeleteOpen(false)}
        />
      )}
    </>
  )
}

function tabClass(active: boolean): string {
  return `border-b-2 pb-2 text-[13px] ${active ? 'border-primary text-primaryink font-semibold' : 'border-transparent text-muted'}`
}

function detailStatus(task: AutomationTask): string {
  const status = task.lastRun?.status || ''
  if (runIsActive(status)) return taskStatusLine(task)
  if (task.state === 'paused') return '已暂停'
  if (task.state === 'ended') return '已结束'
  if (task.nextRunAtUTC) {
    const absolute = formatNextRun(task.nextRunAtUTC, task.schedule?.timeZone || 'Asia/Shanghai').replace(/^下次：/, '')
    const relative = relativeRunTime(task.nextRunAtUTC)
    return `下次运行 ${absolute}${relative ? `（${relative}）` : ''}`
  }
  return taskStatusLine(task)
}

function relativeRunTime(iso: string): string {
  const timestamp = new Date(iso).getTime()
  if (!Number.isFinite(timestamp)) return ''
  const minutes = Math.round((timestamp - Date.now()) / 60000)
  if (minutes <= 0) return '即将运行'
  if (minutes < 60) return `${minutes} 分钟后`
  if (minutes < 24 * 60) return `${Math.round(minutes / 60)} 小时后`
  return `${Math.round(minutes / (24 * 60))} 天后`
}
