// 自动化任务列表的纯状态。窗口先订阅进程事件，再取列表快照：事件可能早于
// 快照到达，晚到的旧 context 或旧搜索也不能把已经换过的画面盖回去。
import type { AutomationContext, AutomationTask, AutomationTaskPage } from '@/core/bridge'

export type AutomationFilter = '' | 'enabled' | 'paused' | 'ended'

export interface AutomationListQuery {
  filter: AutomationFilter
  text: string
}

export interface AutomationListView {
  generation: number
  query: AutomationListQuery
  ids: string[]
  nextCursor?: string
  loading: boolean
  error: string
}

export interface AutomationState {
  context: AutomationContext | null
  tasks: Record<string, AutomationTask>
  /** early 是还没有 context 快照时先到的任务事件。对上 context 后再按 revision 合并。 */
  early: { context: AutomationContext; task: AutomationTask }[]
  list: AutomationListView
  selectedTaskId: string
  unavailable: string
}

export type AutomationAction =
  | { type: 'bind'; context: AutomationContext }
  | { type: 'context'; context: AutomationContext }
  | { type: 'query'; query: AutomationListQuery }
  | { type: 'snapshot'; context: AutomationContext; generation: number; page: AutomationTaskPage }
  | { type: 'append'; context: AutomationContext; generation: number; page: AutomationTaskPage }
  | { type: 'event'; context: AutomationContext; task: AutomationTask }
  | { type: 'remove'; id: string }
  | { type: 'fail'; generation: number; error: string; context?: AutomationContext }
  | { type: 'unavailable'; generation: number; context: AutomationContext; reason: string }
  | { type: 'select'; id: string }

export function emptyAutomationState(): AutomationState {
  return {
    context: null,
    tasks: {},
    early: [],
    list: {
      generation: 0,
      query: { filter: 'enabled', text: '' },
      ids: [],
      loading: true,
      error: '',
    },
    selectedTaskId: '',
    unavailable: '',
  }
}

export function sameAutomationContext(a?: AutomationContext | null, b?: AutomationContext | null): boolean {
  return !!a && !!b && a.viewId === b.viewId && a.epoch === b.epoch
}

export function taskMatchesQuery(task: AutomationTask, query: AutomationListQuery): boolean {
  if (query.filter && task.state !== query.filter) return false
  const text = query.text.trim().toLowerCase()
  if (!text) return true
  return task.name.toLowerCase().includes(text) || task.prompt.toLowerCase().includes(text)
}

function mergeTask(tasks: Record<string, AutomationTask>, task: AutomationTask): Record<string, AutomationTask> {
  const prev = tasks[task.id]
  if (prev) {
    if (prev.revision > task.revision) return tasks
    if (prev.revision === task.revision) {
      // 任务配置没变时 revision 不变，但执行状态会随 run revision 推进。
      // 新一轮的 runRevision 会从 1 重新开始，不能跨 run 比较该字段；
      // 不同 run 以 scheduledAtUTC（服务端列表排序）判定先后。
      const prevRun = prev.lastRun
      const nextRun = task.lastRun
      if (!prevRun) {
        if (!nextRun) return tasks
      } else if (!nextRun) {
        return tasks
      } else if (prevRun.id !== nextRun.id) {
        const prevAt = Date.parse(prevRun.scheduledAtUTC)
        const nextAt = Date.parse(nextRun.scheduledAtUTC)
        if (nextAt < prevAt || (nextAt === prevAt && nextRun.id <= prevRun.id)) return tasks
      } else {
        const prevRevision = prevRun.runRevision ?? 0
        const nextRevision = nextRun.runRevision ?? 0
        if (nextRevision < prevRevision) return tasks
        if (
          nextRevision === prevRevision
          && task.linkedSessionTitle === prev.linkedSessionTitle
          && task.lastLinkedRunId === prev.lastLinkedRunId
          && task.activeRunId === prev.activeRunId
        ) return tasks
      }
    }
  }
  return { ...tasks, [task.id]: task }
}

function absorbEarly(early: AutomationState['early'], context: AutomationContext, tasks: Record<string, AutomationTask>) {
  let next = tasks
  for (const item of early) {
    if (!sameAutomationContext(item.context, context)) continue
    next = mergeTask(next, item.task)
  }
  return next
}

function listIds(page: AutomationTaskPage): string[] {
  const ids: string[] = []
  for (const item of page.items ?? []) {
    if (!ids.includes(item.id)) ids.push(item.id)
  }
  return ids
}

function withHigherTasks(tasks: Record<string, AutomationTask>, page: AutomationTaskPage) {
  let next = tasks
  for (const item of page.items ?? []) next = mergeTask(next, item)
  return next
}

function placeEvent(ids: string[], task: AutomationTask, query: AutomationListQuery): string[] {
  const visible = taskMatchesQuery(task, query)
  const has = ids.includes(task.id)
  if (visible && !has) return [task.id, ...ids]
  if (!visible && has) return ids.filter((id) => id !== task.id)
  return ids
}

export function reduceAutomation(state: AutomationState, action: AutomationAction): AutomationState {
  switch (action.type) {
    case 'bind': {
      if (state.context && !sameAutomationContext(state.context, action.context)) {
        return {
          ...state,
          context: action.context,
          tasks: absorbEarly(state.early, action.context, {}),
          early: [],
          selectedTaskId: '',
          unavailable: '',
          list: { ...state.list, ids: [], nextCursor: undefined, loading: true, error: '' },
        }
      }
      return {
        ...state,
        context: action.context,
        tasks: absorbEarly(state.early, action.context, state.tasks),
        early: [],
        unavailable: '',
      }
    }
    case 'context': {
      if (sameAutomationContext(state.context, action.context)) return state
      return {
        ...state,
        context: action.context,
        tasks: {},
        early: [],
        selectedTaskId: '',
        unavailable: '',
        list: { ...state.list, ids: [], nextCursor: undefined, loading: true, error: '' },
      }
    }
    case 'query': {
      const next = action.query
      if (next.filter === state.list.query.filter && next.text === state.list.query.text) return state
      return {
        ...state,
        list: {
          ...state.list,
          generation: state.list.generation + 1,
          query: next,
          ids: [],
          nextCursor: undefined,
          loading: true,
          error: '',
        },
      }
    }
    case 'snapshot': {
      if (action.generation !== state.list.generation) return state
      if (state.context && !sameAutomationContext(state.context, action.context)) return state
      const context = state.context ?? action.context
      const tasks = absorbEarly(state.early, context, withHigherTasks(state.context ? state.tasks : {}, action.page))
      return {
        ...state,
        context,
        tasks,
        early: [],
        unavailable: '',
        list: {
          ...state.list,
          ids: listIds(action.page),
          nextCursor: action.page.nextCursor || undefined,
          loading: false,
          error: '',
        },
      }
    }
    case 'append': {
      if (action.generation !== state.list.generation) return state
      if (!state.context || !sameAutomationContext(state.context, action.context)) return state
      const tasks = withHigherTasks(state.tasks, action.page)
      const ids = [...state.list.ids]
      for (const id of listIds(action.page)) {
        if (!ids.includes(id)) ids.push(id)
      }
      return {
        ...state,
        tasks,
        list: {
          ...state.list,
          ids,
          nextCursor: action.page.nextCursor || undefined,
          loading: false,
          error: '',
        },
      }
    }
    case 'event': {
      if (!state.context) return { ...state, early: [...state.early, { context: action.context, task: action.task }] }
      if (!sameAutomationContext(state.context, action.context)) return state
      if (action.task.deletedAtUtc) {
        const tasks = { ...state.tasks }
        delete tasks[action.task.id]
        return {
          ...state,
          tasks,
          selectedTaskId: state.selectedTaskId === action.task.id ? '' : state.selectedTaskId,
          list: { ...state.list, ids: state.list.ids.filter((id) => id !== action.task.id) },
        }
      }
      const tasks = mergeTask(state.tasks, action.task)
      if (tasks === state.tasks) return state
      const stored = tasks[action.task.id]
      return {
        ...state,
        tasks,
        list: { ...state.list, ids: placeEvent(state.list.ids, stored, state.list.query) },
      }
    }
    case 'remove': {
      const tasks = { ...state.tasks }
      delete tasks[action.id]
      return {
        ...state,
        tasks,
        selectedTaskId: state.selectedTaskId === action.id ? '' : state.selectedTaskId,
        list: { ...state.list, ids: state.list.ids.filter((id) => id !== action.id) },
      }
    }
    case 'fail': {
      if (action.generation !== state.list.generation) return state
      if (action.context && state.context && !sameAutomationContext(action.context, state.context)) return state
      return { ...state, list: { ...state.list, loading: false, error: action.error } }
    }
    case 'unavailable': {
      if (action.generation !== state.list.generation) return state
      if (state.context && !sameAutomationContext(state.context, action.context)) return state
      return {
        ...state,
        context: action.context,
        tasks: {},
        list: { ...state.list, ids: [], nextCursor: undefined, loading: false, error: '' },
        unavailable: action.reason,
      }
    }
    case 'select':
      return state.selectedTaskId === action.id ? state : { ...state, selectedTaskId: action.id }
    default:
      return state
  }
}
