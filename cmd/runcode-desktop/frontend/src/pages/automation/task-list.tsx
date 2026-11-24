import type { AutomationSchedule, AutomationTask } from '@/core/bridge'
import { basename } from '@/core/paths'
import { Banner, InlineError } from '@/ui/feedback'
import { FIELD_CLS } from '@/ui/fields'
import { Icon } from '@/ui/icons'
import { Placeholder } from '@/ui/layout'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import type { AutomationController } from '@/session/use-automation'

const TABS: { id: AutomationController['filter']; label: string }[] = [
  { id: 'enabled', label: '已开启' },
  { id: 'paused', label: '已暂停' },
  { id: 'ended', label: '已结束' },
  { id: '', label: '全部' },
]

const WEEKDAY = ['', '周一', '周二', '周三', '周四', '周五', '周六', '周日']
const ACTIVE_RUN = new Set(['queued', 'submitting', 'running', 'waiting_permission'])

export function runIsActive(status?: string): boolean {
  return !!status && ACTIVE_RUN.has(status)
}

export function scheduleSummary(schedule?: AutomationSchedule): string {
  if (!schedule?.mode) return '时间规则不可用'
  const n = schedule.interval && schedule.interval > 0 ? schedule.interval : 1
  switch (schedule.mode) {
    case 'weekly': {
      const days = (schedule.weekdays ?? []).filter((day) => day >= 1 && day <= 7).map((day) => WEEKDAY[day]).join('、')
      const head = n === 1 ? '每周' : `每 ${n} 周`
      return days ? `${head} ${days}` : head
    }
    case 'weekdays':
      return n === 1 ? '周一至周五' : `每 ${n} 周的工作日`
    case 'daily':
      return n === 1 ? '每天' : `每 ${n} 天`
    case 'every_hours':
      return `每 ${n} 小时`
    case 'every_minutes':
      return `每 ${n} 分钟`
    case 'monthly':
      return n === 1 ? `每月${schedule.monthDay ? ` ${schedule.monthDay} 日` : ''}` : `每 ${n} 个月`
    case 'once':
      return '仅一次'
    default:
      return '时间规则不可用'
  }
}

export function formatNextRun(iso: string | undefined, timeZone: string): string {
  if (!iso) return '没有下次执行'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '没有下次执行'
  let zone = timeZone || 'Asia/Shanghai'
  try {
    Intl.DateTimeFormat(undefined, { timeZone: zone })
  } catch {
    zone = 'Asia/Shanghai'
  }
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(date)
  const pick = (type: Intl.DateTimeFormatPartTypes) => parts.find((part) => part.type === type)?.value ?? ''
  return `下次：${pick('month')}月${pick('day')}日 ${pick('hour')}:${pick('minute')}`
}

export function taskStatusLine(task: AutomationTask): string {
  const run = task.lastRun
  if (task.state === 'paused' && run && runIsActive(run.status)) return '已暂停 · 本次执行继续完成'
  if (task.blockedReason) return task.blockedReason
  if (run?.status === 'waiting_permission') return '等待授权'
  if (run && runIsActive(run.status) && run.status !== 'waiting_permission') return '执行中'
  if (task.state === 'enabled') return '已开启'
  if (task.state === 'paused') return '已暂停'
  if (task.state === 'ended') return run?.reason || '已结束'
  return '状态未知'
}

function emptyCopy(filter: AutomationController['filter'], query: string): string {
  if (query.trim()) return '没有匹配的自动化任务'
  if (filter === 'enabled') return '没有已开启的任务'
  if (filter === 'paused') return '没有已暂停的任务'
  if (filter === 'ended') return '没有已结束的任务'
  return '还没有自动化任务'
}

export function TaskList({ auto }: { auto: AutomationController }) {
  const showRowCreate = auto.tasks.length > 0
  const showCenterCreate = !auto.loading && !auto.error && !auto.unavailable && auto.tasks.length === 0
  return (
    <div className="flex flex-col gap-3 px-5 py-4 min-h-full">
      <div className="flex flex-wrap gap-1.5" role="tablist" aria-label="任务状态">
        {TABS.map((tab) => {
          const selected = auto.filter === tab.id
          return (
            <button
              key={tab.label}
              type="button"
              role="tab"
              aria-selected={selected}
              onClick={() => auto.setFilter(tab.id)}
              className={`px-3 py-1.5 rounded-full text-[13px] cursor-pointer border ${
                selected ? 'bg-primarysoft text-primaryink border-primary/30 font-semibold' : 'bg-surface text-muted border-transparent hover:bg-surface2'
              }`}
            >
              {tab.label}
            </button>
          )
        })}
      </div>
      <div className="flex items-center gap-2">
        <input
          aria-label="搜索自动化任务"
          value={auto.query}
          placeholder="搜索自动化任务"
          onChange={(e) => auto.setQuery(e.target.value)}
          className={`${FIELD_CLS} flex-1 min-w-0`}
        />
        {showRowCreate && (
          <button type="button" className={`${BTN} ${BTN_PRIMARY} flex-none`} onClick={auto.openCreate}>
            新建自动化任务
          </button>
        )}
      </div>
      {auto.unavailable && <Banner tone="warning">{auto.unavailable}</Banner>}
      {auto.error && <InlineError>{auto.error}</InlineError>}
      {auto.loading && !auto.error && <Placeholder>正在加载自动化任务</Placeholder>}
      {!auto.loading && !auto.error && auto.tasks.length > 0 && (
        <div className="flex flex-col gap-1">
          {auto.tasks.map((task) => {
            const selected = task.id === auto.selectedTaskId
            const folder = basename(task.workspace)
            return (
              <button
                key={task.id}
                type="button"
                data-task-id={task.id}
                onClick={() => auto.selectTask(task.id)}
                className={`w-full text-left flex items-start gap-3 px-3 py-2.5 rounded-field border cursor-pointer ${
                  selected ? 'bg-primarysoft border-primary/30' : 'bg-surface border-transparent hover:bg-surface2'
                }`}
              >
                <span className="flex-none text-faint mt-0.5"><Icon name="clock" size={16} /></span>
                <span className="flex-1 min-w-0">
                  <span className="block text-[14px] text-ink truncate">{task.name}</span>
                  <span className="block text-[12px] text-muted truncate" title={task.workspace}>
                    {scheduleSummary(task.schedule)} · {folder}
                  </span>
                </span>
                <span className="flex-none text-right">
                  <span className="block text-[12px] text-ink2">{formatNextRun(task.nextRunAtUTC, task.schedule?.timeZone || 'Asia/Shanghai')}</span>
                  <span className="block text-[12px] text-muted">{taskStatusLine(task)}</span>
                </span>
              </button>
            )
          })}
          {auto.hasMore && (
            <button type="button" className={`${BTN} self-center mt-2`} onClick={auto.loadMore}>加载更多</button>
          )}
        </div>
      )}
      {showCenterCreate && (
        <div className="flex-1 flex flex-col items-center justify-center gap-3 py-10">
          <p className="m-0 text-[13px] text-muted">{emptyCopy(auto.filter, auto.query)}</p>
          <button type="button" className={`${BTN} ${BTN_PRIMARY}`} onClick={auto.openCreate}>新建自动化任务</button>
        </div>
      )}
    </div>
  )
}
