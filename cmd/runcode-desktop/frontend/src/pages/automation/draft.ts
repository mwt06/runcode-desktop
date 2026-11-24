// 创建表单的纯规则。前端只负责把明显的空值和越界拦在本地；真正能不能存，仍由后端决定。
import type { AutomationSchedule, AutomationTaskDraft } from '@/core/bridge'

export const SCHEDULE_MODES = [
  { value: 'weekly', label: '每周', unit: '周' },
  { value: 'weekdays', label: '周一至周五', unit: '周' },
  { value: 'daily', label: '每天', unit: '天' },
  { value: 'every_hours', label: '每 N 小时', unit: '小时' },
  { value: 'every_minutes', label: '每 N 分钟', unit: '分钟' },
  { value: 'monthly', label: '每月', unit: '月' },
  { value: 'once', label: '仅一次', unit: '' },
] as const

export const EXECUTION_MODES = [
  { value: 'reuse_session', label: '新建会话并复用' },
  { value: 'new_session_each_run', label: '每次新建会话' },
] as const

export const TIME_ZONES = [
  'Asia/Shanghai',
  'UTC',
  'Asia/Hong_Kong',
  'Asia/Tokyo',
  'Asia/Singapore',
  'America/New_York',
  'America/Los_Angeles',
  'Europe/London',
  'Europe/Berlin',
] as const

export type ScheduleMode = (typeof SCHEDULE_MODES)[number]['value']

export function unicodeLength(value: string): number {
  return Array.from(value).length
}

export function knownTimeZone(zone: string): boolean {
  if (!zone) return false
  try {
    Intl.DateTimeFormat(undefined, { timeZone: zone })
    return true
  } catch {
    return false
  }
}

export function resolveTimeZone(candidate?: string): { zone: string; fallback: boolean } {
  const trimmed = candidate?.trim() ?? ''
  if (trimmed && knownTimeZone(trimmed)) return { zone: trimmed, fallback: false }
  return { zone: 'Asia/Shanghai', fallback: true }
}

export function browserTimeZone(): { zone: string; fallback: boolean } {
  let reported = ''
  try {
    reported = Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    reported = ''
  }
  return resolveTimeZone(reported)
}

function pad(value: number): string {
  return String(value).padStart(2, '0')
}

function zonedParts(date: Date, timeZone: string) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    weekday: 'short',
    hourCycle: 'h23',
  }).formatToParts(date)
  const pick = (type: Intl.DateTimeFormatPartTypes) => parts.find((part) => part.type === type)?.value ?? ''
  const weekday: Record<string, number> = { Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6, Sun: 7 }
  return {
    year: pick('year'),
    month: pick('month'),
    day: pick('day'),
    hour: Number(pick('hour')),
    minute: Number(pick('minute')),
    second: Number(pick('second')),
    weekday: weekday[pick('weekday')] ?? 1,
  }
}

function addDays(year: string, month: string, day: string, offset: number) {
  const utc = new Date(Date.UTC(Number(year), Number(month) - 1, Number(day) + offset))
  return {
    year: String(utc.getUTCFullYear()),
    month: pad(utc.getUTCMonth() + 1),
    day: pad(utc.getUTCDate()),
  }
}

export function defaultAutomationDraft(now: Date, timeZone: string | undefined, recent: string[], currentWorkspace = ''): AutomationTaskDraft {
  const { zone } = resolveTimeZone(timeZone)
  const parts = zonedParts(now, zone)
  const seconds = parts.hour * 3600 + parts.minute * 60 + parts.second
  const step = 5 * 60
  let next = Math.floor(seconds / step) * step + step
  let dayOffset = 0
  if (next >= 24 * 3600) {
    next -= 24 * 3600
    dayOffset = 1
  }
  const start = addDays(parts.year, parts.month, parts.day, dayOffset)
  return {
    name: '',
    prompt: '',
    workspace: currentWorkspace.trim() || recent.find((item) => item.trim()) || '',
    executionMode: 'reuse_session',
    schedule: {
      mode: 'daily',
      timeZone: zone,
      startDate: `${start.year}-${start.month}-${start.day}`,
      timeOfDay: `${pad(Math.floor(next / 3600))}:${pad(Math.floor((next % 3600) / 60))}:00`,
      weekdays: [parts.weekday],
      monthDay: Number(parts.day),
      interval: 1,
    },
  }
}

export function scheduleForSave(schedule: AutomationSchedule): AutomationSchedule {
  const next: AutomationSchedule = {
    mode: schedule.mode,
    timeZone: schedule.timeZone,
    startDate: schedule.startDate,
    timeOfDay: schedule.timeOfDay,
  }
  if (schedule.mode !== 'once' && schedule.interval != null) next.interval = schedule.interval
  if (schedule.mode === 'weekly' && schedule.weekdays?.length) {
    next.weekdays = [...new Set(schedule.weekdays)].sort((a, b) => a - b)
  }
  if (schedule.mode === 'monthly' && schedule.monthDay != null) next.monthDay = schedule.monthDay
  return next
}

export function validateAutomationDraft(draft: AutomationTaskDraft): Record<string, string> {
  const errors: Record<string, string> = {}
  const name = draft.name.trim()
  if (!name) errors.name = '请填写任务名称'
  else if (unicodeLength(name) > 80) errors.name = '任务名称不能超过 80 个字符'

  const prompt = draft.prompt.trim()
  if (!prompt) errors.prompt = '请填写任务内容'
  else if (unicodeLength(prompt) > 16000) errors.prompt = '任务内容不能超过 16000 个字符'

  if (!draft.workspace.trim()) errors.workspace = '请选择工作区'
  if (draft.executionMode !== 'reuse_session' && draft.executionMode !== 'new_session_each_run') {
    errors.executionMode = '请选择执行位置'
  }

  const schedule = draft.schedule
  const known = SCHEDULE_MODES.some((mode) => mode.value === schedule.mode)
  if (!known) errors.schedule = '请选择七种运行时间之一'
  if (!/^\d{4}-\d{2}-\d{2}$/.test(schedule.startDate || '')) errors.startDate = '请选择日期'
  if (!/^\d{2}:\d{2}:\d{2}$/.test(schedule.timeOfDay || '')) errors.timeOfDay = '时间需要精确到秒'
  if (!knownTimeZone(schedule.timeZone)) errors.timeZone = '无法识别该时区，请改用 Asia/Shanghai'

  if (schedule.mode === 'once') {
    if (schedule.interval != null) errors.interval = '仅一次不需要重复间隔'
  } else if (known) {
    const interval = schedule.interval
    if (typeof interval !== 'number' || !Number.isInteger(interval) || interval < 1 || interval > 9999) {
      errors.interval = '重复间隔须为 1 到 9999 的整数'
    }
  }
  if (schedule.mode === 'weekly') {
    const days = schedule.weekdays ?? []
    if (days.length === 0 || days.some((day) => !Number.isInteger(day) || day < 1 || day > 7)) {
      errors.weekdays = '请至少选择一个星期'
    }
  }
  if (schedule.mode === 'monthly') {
    const day = schedule.monthDay
    if (typeof day !== 'number' || !Number.isInteger(day) || day < 1 || day > 31) errors.monthDay = '每月日期须在 1 到 31 之间'
  }
  return errors
}

export function draftFromTask(task: { name: string; prompt: string; workspace: string; executionMode: string; schedule: AutomationSchedule }): AutomationTaskDraft {
  return {
    name: task.name,
    prompt: task.prompt,
    workspace: task.workspace,
    executionMode: task.executionMode,
    schedule: {
      ...task.schedule,
      weekdays: task.schedule.weekdays ? [...task.schedule.weekdays] : undefined,
    },
  }
}

export function applySaveError(draft: AutomationTaskDraft, message: string): { draft: AutomationTaskDraft; errors: Record<string, string> } {
  return { draft, errors: { schedule: message } }
}
