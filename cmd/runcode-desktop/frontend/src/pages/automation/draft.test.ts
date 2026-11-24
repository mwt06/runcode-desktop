import { describe, expect, it } from 'vitest'
import type { AutomationSchedule, AutomationTaskDraft } from '@/core/bridge'
import {
  SCHEDULE_MODES,
  applySaveError,
  defaultAutomationDraft,
  unicodeLength,
  validateAutomationDraft,
} from './draft'

function draft(extra: Partial<Omit<AutomationTaskDraft, 'schedule'>> & { schedule?: Partial<AutomationSchedule> } = {}): AutomationTaskDraft {
  const base = defaultAutomationDraft(new Date('2026-11-18T01:07:01Z'), 'Asia/Shanghai', [])
  return {
    ...base,
    name: '每日整理',
    prompt: '整理今天的记录',
    workspace: 'D:\\资料',
    ...extra,
    schedule: { ...base.schedule, ...extra.schedule },
  }
}

describe('automation draft', () => {
  it('counts name and prompt by Unicode code point, including non-BMP and emoji', () => {
    expect(unicodeLength('𠮷')).toBe(1)
    expect(unicodeLength('😀')).toBe(1)
    expect(unicodeLength('😀'.repeat(80))).toBe(80)
    const name80 = '😀'.repeat(80)
    const name81 = '😀'.repeat(81)
    expect(validateAutomationDraft(draft({ name: name80 })).name).toBeUndefined()
    expect(validateAutomationDraft(draft({ name: name81 })).name).toBeTruthy()
    expect(validateAutomationDraft(draft({ name: '  ' })).name).toBeTruthy()

    const promptOk = '文'.repeat(16000)
    const promptOver = '文'.repeat(16001)
    expect(validateAutomationDraft(draft({ prompt: promptOk })).prompt).toBeUndefined()
    expect(validateAutomationDraft(draft({ prompt: promptOver })).prompt).toBeTruthy()
    expect(validateAutomationDraft(draft({ prompt: '  \n' })).prompt).toBeTruthy()
  })

  it('accepts only the seven schedule modes and a positive interval except once', () => {
    expect(SCHEDULE_MODES.map((mode) => mode.value)).toEqual([
      'weekly', 'weekdays', 'daily', 'every_hours', 'every_minutes', 'monthly', 'once',
    ])
    expect(SCHEDULE_MODES.some((mode) => (mode.value as string) === 'custom' || mode.label.includes('自定义'))).toBe(false)

    for (const mode of SCHEDULE_MODES) {
      if (mode.value === 'once') continue
      expect(validateAutomationDraft(draft({ schedule: { mode: mode.value, interval: 1 } })).interval).toBeUndefined()
      expect(validateAutomationDraft(draft({ schedule: { mode: mode.value, interval: 9999 } })).interval).toBeUndefined()
      expect(validateAutomationDraft(draft({ schedule: { mode: mode.value, interval: 0 } })).interval).toBeTruthy()
      expect(validateAutomationDraft(draft({ schedule: { mode: mode.value, interval: 10000 } })).interval).toBeTruthy()
      expect(validateAutomationDraft(draft({ schedule: { mode: mode.value, interval: 1.5 } })).interval).toBeTruthy()
    }
    const once = validateAutomationDraft(draft({ schedule: { mode: 'once', interval: undefined } }))
    expect(once.interval).toBeUndefined()
    expect(validateAutomationDraft(draft({ schedule: { mode: 'once', interval: 1 } })).interval).toBeTruthy()
    expect(validateAutomationDraft(draft({ schedule: { mode: 'custom' as AutomationTaskDraft['schedule']['mode'] } })).schedule).toBeTruthy()
  })

  it('rolls the default clock to the next five-minute boundary and across midnight', () => {
    const morning = defaultAutomationDraft(new Date('2026-11-18T01:07:01Z'), 'Asia/Shanghai', ['D:\\资料'])
    expect(morning.schedule.mode).toBe('daily')
    expect(morning.schedule.startDate).toBe('2026-11-18')
    expect(morning.schedule.timeOfDay).toBe('09:10:00')
    expect(morning.schedule.interval).toBe(1)
    expect(morning.schedule.timeZone).toBe('Asia/Shanghai')
    expect(morning.executionMode).toBe('reuse_session')
    expect(morning.workspace).toBe('D:\\资料')

    const exact = defaultAutomationDraft(new Date('2026-11-18T01:10:00Z'), 'Asia/Shanghai', [])
    expect(exact.schedule.timeOfDay).toBe('09:15:00')

    const late = defaultAutomationDraft(new Date('2026-11-18T15:58:30Z'), 'Asia/Shanghai', [])
    expect(late.schedule.startDate).toBe('2026-11-19')
    expect(late.schedule.timeOfDay).toBe('00:00:00')
    expect(late.schedule.weekdays).toEqual([3])
    expect(late.schedule.monthDay).toBe(18)
  })

  it('uses the workspace saved in settings before the recent-workspace list', () => {
    const current = 'C:\\Users\\yyzxc\\Desktop\\Test-guokai-client'
    const next = defaultAutomationDraft(
      new Date('2026-11-18T01:07:01Z'),
      'Asia/Shanghai',
      ['D:\\workspace\\imagegen-client', 'D:\\workspace\\Bridge'],
      current,
    )
    expect(next.workspace).toBe(current)
  })

  it('keeps the draft when the backend rejects an expired once', () => {
    const current = draft({ name: '只跑一次', schedule: { mode: 'once', interval: undefined } })
    const kept = applySaveError(current, '该一次性计划已经过期')
    expect(kept.draft).toEqual(current)
    expect(kept.errors.schedule).toContain('过期')
  })
})
