import { useEffect, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import type { AutomationTaskDraft } from '@/core/bridge'
import { errText } from '@/core/bridge'
import { Banner, InlineError } from '@/ui/feedback'
import { FIELD_CLS, LABEL_CLS, SelectField } from '@/ui/fields'
import { isComposingKey } from '@/ui/keys'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { EXECUTION_MODES, applySaveError, scheduleForSave, validateAutomationDraft } from './draft'
import { ScheduleFields } from './schedule-fields'
import { WorkspacePicker } from './workspace-picker'

export function TaskForm({ initial, recent = [], currentWorkspace, zoneNote, canSave = true, onSave, onCancel, onDirty, onOpenSettings }: {
  initial: AutomationTaskDraft
  recent?: string[]
  currentWorkspace?: string
  zoneNote?: string
  canSave?: boolean
  onSave: (draft: AutomationTaskDraft) => Promise<void>
  onCancel: () => void
  onDirty?: (dirty: boolean) => void
  onOpenSettings?: () => void
}) {
  const [draft, setDraft] = useState(initial)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [saveError, setSaveError] = useState('')
  const [saving, setSaving] = useState(false)
  const dirty = JSON.stringify(draft) !== JSON.stringify(initial)

  useEffect(() => { onDirty?.(dirty) }, [dirty, onDirty])

  async function save() {
    if (saving || !canSave) return
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
    setSaveError('')
    try {
      await onSave(next)
    } catch (e) {
      const kept = applySaveError(draft, errText(e))
      setSaveError(kept.errors.schedule || errText(e))
    } finally {
      setSaving(false)
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
    <form className="flex h-full min-h-0 flex-col overflow-hidden" onSubmit={(e) => { e.preventDefault(); void save() }} onKeyDown={onKeyDown}>
      <div className="flex-none px-4 py-3 border-b border-line text-[15px] font-semibold text-ink">新建自动化任务</div>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 py-4 flex flex-col gap-3">
        <label className={LABEL_CLS}>
          任务名称
          <input aria-label="任务名称" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} className={`${FIELD_CLS} w-full`} />
          {errors.name && <InlineError variant="text">{errors.name}</InlineError>}
        </label>
        <label className={LABEL_CLS}>
          任务内容
          <textarea aria-label="任务内容" value={draft.prompt} rows={5} onChange={(e) => setDraft({ ...draft, prompt: e.target.value })} className={`${FIELD_CLS} w-full resize-y`} />
          {errors.prompt && <InlineError variant="text">{errors.prompt}</InlineError>}
        </label>
        <label className={LABEL_CLS}>
          执行位置
          <SelectField aria-label="执行位置" value={draft.executionMode} onChange={(executionMode) => setDraft({ ...draft, executionMode })}>
            {EXECUTION_MODES.map((mode) => <option key={mode.value} value={mode.value}>{mode.label}</option>)}
          </SelectField>
        </label>
        <div className="flex items-center gap-3 text-[13px] text-muted">
          <span>关联工作区</span>
          <div className="min-w-0 flex-1">
            <WorkspacePicker
              value={draft.workspace}
              current={currentWorkspace}
              recent={recent}
              onChange={(workspace) => setDraft({ ...draft, workspace })}
            />
          </div>
          {errors.workspace && <InlineError variant="text">{errors.workspace}</InlineError>}
        </div>
        <ScheduleFields value={draft.schedule} onChange={(schedule) => setDraft({ ...draft, schedule })} />
        {errors.interval && <InlineError variant="text">{errors.interval}</InlineError>}
        {errors.weekdays && <InlineError variant="text">{errors.weekdays}</InlineError>}
        {errors.schedule && <InlineError variant="text">{errors.schedule}</InlineError>}
        {zoneNote && <Banner tone="warning">{zoneNote}</Banner>}
        {saveError && <Banner tone="danger">{saveError}</Banner>}
        {!canSave && (
          <Banner tone="warning" title="还不能保存">
            当前没有可恢复的模型连接。
            {onOpenSettings && <button type="button" className="ml-2 text-primaryink underline" onClick={onOpenSettings}>打开设置</button>}
          </Banner>
        )}
      </div>
      <div className="flex-none px-4 py-3 border-t border-line flex justify-end gap-2">
        <button type="button" className={BTN} onClick={onCancel}>取消</button>
        <button type="submit" className={`${BTN} ${BTN_PRIMARY}`} disabled={saving || !canSave}>{saving ? '保存中' : '保存'}</button>
      </div>
    </form>
  )
}
