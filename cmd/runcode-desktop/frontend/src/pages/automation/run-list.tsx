import { useEffect, useRef, useState } from 'react'
import {
  Events,
  errText,
  listAutomationRuns,
  onEnvelope,
  type AutomationContext,
  type AutomationRun,
} from '@/core/bridge'
import { InlineError } from '@/ui/feedback'
import { CheckMark } from '@/ui/glyphs'
import { Placeholder } from '@/ui/layout'
import { BTN } from '@/ui/tokens'
import { sameAutomationContext } from '@/session/automation-state'

const PAGE_LIMIT = 50

export function mergeRunPage(current: AutomationRun[], page: AutomationRun[], where: 'end' | 'front' = 'end'): AutomationRun[] {
  const map = new Map(current.map((run) => [run.id, run]))
  const order = current.map((run) => run.id)
  for (const run of page) {
    const prev = map.get(run.id)
    if (!prev) {
      map.set(run.id, run)
      if (where === 'front') order.unshift(run.id)
      else order.push(run.id)
      continue
    }
    if ((run.runRevision ?? 0) >= (prev.runRevision ?? 0)) map.set(run.id, run)
  }
  return order.map((id) => map.get(id)!)
}

function runLabel(status: string): string {
  switch (status) {
    case 'succeeded': return '成功'
    case 'failed': return '失败'
    case 'skipped': return '已跳过'
    case 'interrupted': return '已中断'
    case 'timed_out': return '已超时'
    case 'running': return '执行中'
    case 'waiting_permission': return '等待授权'
    case 'queued':
    case 'submitting': return '排队中'
    default: return '未知状态'
  }
}

function formatStamp(iso?: string): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).format(date)
}

export function RunList({ taskId, context, onOpen }: {
  taskId: string
  context: AutomationContext
  onOpen: (taskId: string, runId: string) => void
}) {
  const [runs, setRuns] = useState<AutomationRun[]>([])
  const [cursor, setCursor] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')
  const [openingId, setOpeningId] = useState('')
  const ticket = useRef(0)
  const contextKey = `${context.viewId}:${context.epoch}`

  useEffect(() => {
    const mine = ++ticket.current
    setRuns([])
    setCursor('')
    setLoading(true)
    setError('')
    let cancelled = false
    ;(async () => {
      try {
        const page = await listAutomationRuns({ context, taskId, limit: PAGE_LIMIT })
        if (cancelled || mine !== ticket.current) return
        if (!sameAutomationContext(page.context, context)) return
        setRuns(mergeRunPage([], page.items ?? []))
        setCursor(page.nextCursor || '')
      } catch (e) {
        if (cancelled || mine !== ticket.current) return
        setError(errText(e))
      } finally {
        if (!cancelled && mine === ticket.current) setLoading(false)
      }
    })()
    return () => { cancelled = true }
  }, [context, contextKey, taskId])

  useEffect(() => onEnvelope(Events.AutomationRunChanged, (env) => {
    const payload = env.payload
    if (!payload?.run || payload.run.taskId !== taskId) return
    if (!sameAutomationContext(payload.context, context)) return
    setRuns((current) => mergeRunPage(current, [payload.run], 'front'))
  }), [context, taskId])

  async function loadMore() {
    if (!cursor || loading) return
    const mine = ++ticket.current
    setLoading(true)
    try {
      const page = await listAutomationRuns({ context, taskId, cursor, limit: PAGE_LIMIT })
      if (mine !== ticket.current) return
      if (!sameAutomationContext(page.context, context)) return
      setRuns((current) => mergeRunPage(current, page.items ?? []))
      setCursor(page.nextCursor || '')
    } catch (e) {
      if (mine !== ticket.current) return
      setError(errText(e))
    } finally {
      if (mine === ticket.current) setLoading(false)
    }
  }

  async function copyReason(id: string, reason: string) {
    try {
      await navigator.clipboard.writeText(reason)
      setCopied(id)
    } catch (e) {
      setError(errText(e))
    }
  }

  async function openRun(id: string) {
    if (openingId || !runs.find((item) => item.id === id)?.sessionId) return
    setOpeningId(id)
    setError('')
    try {
      await onOpen(taskId, id)
    } catch (e) {
      setError(errText(e))
    } finally {
      setOpeningId('')
    }
  }

  if (loading && runs.length === 0) return <Placeholder>正在加载运行记录</Placeholder>
  if (error && runs.length === 0) return <InlineError>{error}</InlineError>
  if (runs.length === 0) return <Placeholder>还没有运行记录</Placeholder>

  return (
    <div className="flex flex-col gap-2">
      {error && <InlineError>{error}</InlineError>}
      {runs.map((run) => {
        const success = run.status === 'succeeded'
        const linked = !!run.sessionId
        return (
          <div key={run.id} data-run-id={run.id} data-success={success ? 'true' : 'false'} className="rounded-field border border-line px-3 py-2">
            <button
              type="button"
              disabled={!linked || !!openingId}
              onClick={() => void openRun(run.id)}
              className="w-full text-left flex items-start gap-2 disabled:cursor-default"
            >
              {success && <span className="text-green flex-none"><CheckMark size={14} /></span>}
              <span className="flex-1 min-w-0">
                <span className="block text-[13px] text-ink">
                  {openingId === run.id
                    ? '正在打开历史'
                    : `${run.trigger === 'manual' ? '手动执行 · ' : ''}${runLabel(run.status)} · ${formatStamp(run.scheduledAtUTC)}`}
                </span>
                {run.reason && <span className="block text-[12px] text-muted select-text whitespace-pre-wrap break-words">{run.reason}</span>}
              </span>
            </button>
            {run.status === 'failed' && run.reason && (
              <button type="button" className={`${BTN} mt-2 px-3 py-1 text-[12px]`} onClick={() => void copyReason(run.id, run.reason || '')}>
                {copied === run.id ? '已复制' : '复制原因'}
              </button>
            )}
          </div>
        )
      })}
      {cursor && <button type="button" className={`${BTN} self-center`} onClick={() => void loadMore()}>加载更多</button>}
    </div>
  )
}
