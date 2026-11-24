// 管理窗的数据面：订阅在快照之前挂上，列表只展示当前 context 与当前搜索代际
// 接受的结果。这里不启动任务，也不改当前会话。
import { useCallback, useEffect, useReducer, useRef, useState } from 'react'
import {
  Events,
  createAutomationTask,
  deleteAutomationTask,
  errText,
  getAutomationDefaults,
  listAutomationTasks,
  onEnvelope,
  runAutomationTaskNow,
  setAutomationTaskPaused,
  updateAutomationTask,
  type AutomationContext,
  type AutomationDefaults,
  type AutomationTask,
  type AutomationTaskDraft,
  type AutomationTaskResult,
} from '@/core/bridge'
import {
  emptyAutomationState,
  reduceAutomation,
  sameAutomationContext,
  type AutomationFilter,
  type AutomationState,
} from './automation-state'

export type { AutomationFilter }

const PAGE_LIMIT = 50

export interface AutomationController {
  context: AutomationState['context']
  tasks: AutomationTask[]
  selectedTask: AutomationTask | null
  selectedTaskId: string
  pendingTaskId: string
  loading: boolean
  error: string
  unavailable: string
  filter: AutomationFilter
  query: string
  hasMore: boolean
  createOpen: boolean
  defaults: AutomationDefaults | null
  openCreate: () => void
  closeCreate: () => void
  saveCreate: (draft: AutomationTaskDraft) => Promise<void>
  updateTask: (taskId: string, expectedRevision: number, draft: AutomationTaskDraft) => Promise<AutomationTask>
  setPaused: (task: AutomationTask, paused: boolean) => Promise<AutomationTask>
  runNow: (task: AutomationTask) => Promise<void>
  deleteTask: (task: AutomationTask) => Promise<void>
  notice: string
  selectTask: (id: string) => void
  setFilter: (filter: AutomationFilter) => void
  setQuery: (text: string) => void
  refresh: () => void
  loadMore: () => void
}

export function useAutomation(enabled: boolean): AutomationController {
  const [state, dispatch] = useReducer(reduceAutomation, undefined, emptyAutomationState)
  const [defaults, setDefaults] = useState<AutomationDefaults | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [notice, setNotice] = useState('')
  const [pendingTaskId, setPendingTaskId] = useState('')
  const [reload, setReload] = useState(0)
  const stateRef = useRef(state)
  const defaultsRef = useRef(defaults)
  const requestIdRef = useRef('')
  stateRef.current = state
  defaultsRef.current = defaults
  const ticketRef = useRef(0)
  const appendTicket = useRef(0)

  const generation = state.list.generation
  const filter = state.list.query.filter
  const queryText = state.list.query.text

  useEffect(() => {
    if (!enabled) return
    return onEnvelope(Events.AutomationTaskChanged, (env) => {
      const payload = env.payload
      if (!payload?.context || !payload.task) return
      dispatch({ type: 'event', context: payload.context, task: payload.task })
    })
  }, [enabled])

  useEffect(() => {
    if (!enabled) return
    return onEnvelope(Events.AutomationContextChanged, (env) => {
      if (!env.payload?.context) return
      dispatch({ type: 'context', context: env.payload.context })
      setReload((n) => n + 1)
    })
  }, [enabled])

  useEffect(() => {
    if (!enabled) return
    return onEnvelope(Events.AutomationServiceChanged, () => {
      setReload((n) => n + 1)
    })
  }, [enabled])

  useEffect(() => {
    if (!enabled) return
    return onEnvelope(Events.SessionRenamed, (env) => {
      const sessionID = env.payload?.id
      if (!sessionID) return
      const linked = Object.values(stateRef.current.tasks).some((task) => task.lastRun?.sessionId === sessionID)
      if (linked) setReload((n) => n + 1)
    })
  }, [enabled])

  useEffect(() => {
    if (!enabled) return
    const ticket = ++ticketRef.current
    appendTicket.current += 1
    let cancelled = false
    const generationAtStart = generation
    const requested = { filter, text: queryText }
    ;(async () => {
      try {
        const nextDefaults = await getAutomationDefaults()
        if (cancelled || ticket !== ticketRef.current) return
        setDefaults(nextDefaults)
        dispatch({ type: 'bind', context: nextDefaults.context })
        if (!nextDefaults.available) {
          dispatch({
            type: 'unavailable',
            generation: generationAtStart,
            context: nextDefaults.context,
            reason: nextDefaults.unavailableReason || '自动化暂不可用',
          })
          return
        }
        const page = await listAutomationTasks({
          context: nextDefaults.context,
          state: requested.filter || undefined,
          query: requested.text.trim() || undefined,
          limit: PAGE_LIMIT,
        })
        if (cancelled || ticket !== ticketRef.current) return
        dispatch({ type: 'snapshot', context: page.context, generation: generationAtStart, page })
      } catch (e) {
        if (cancelled || ticket !== ticketRef.current) return
        dispatch({ type: 'fail', generation: generationAtStart, error: errText(e) })
      }
    })()
    return () => { cancelled = true }
  }, [enabled, generation, filter, queryText, reload])

  const setFilter = useCallback((next: AutomationFilter) => {
    dispatch({ type: 'query', query: { filter: next, text: stateRef.current.list.query.text } })
  }, [])

  const setQuery = useCallback((text: string) => {
    dispatch({ type: 'query', query: { filter: stateRef.current.list.query.filter, text } })
  }, [])

  const selectTask = useCallback((id: string) => dispatch({ type: 'select', id }), [])
  const openCreate = useCallback(() => {
    if (!requestIdRef.current) requestIdRef.current = crypto.randomUUID()
    setCreateOpen(true)
  }, [])
  const closeCreate = useCallback(() => {
    requestIdRef.current = ''
    setCreateOpen(false)
  }, [])
  const saveCreate = useCallback(async (draft: AutomationTaskDraft) => {
    const snap = stateRef.current
    const currentDefaults = defaultsRef.current
    if (!snap.context || !currentDefaults) throw new Error('自动化上下文还没有准备好')
    if (!requestIdRef.current) requestIdRef.current = crypto.randomUUID()
    const result = await createAutomationTask({
      context: snap.context,
      clientRequestId: requestIdRef.current,
      expectedBindingVersion: currentDefaults.bindingVersion,
      draft,
    })
    if (!sameAutomationContext(result.context, stateRef.current.context)) {
      throw new Error('身份已变化，请刷新后重试')
    }
    requestIdRef.current = ''
    dispatch({ type: 'event', context: result.context, task: result.task })
    dispatch({ type: 'select', id: result.task.id })
    setNotice(result.warning || '')
    setCreateOpen(false)
    setReload((n) => n + 1)
  }, [])

  const acceptTask = useCallback((result: AutomationTaskResult, started: AutomationContext) => {
    if (!sameAutomationContext(started, stateRef.current.context) || !sameAutomationContext(result.context, stateRef.current.context)) {
      throw new Error('身份已变化，请刷新后重试')
    }
    dispatch({ type: 'event', context: result.context, task: result.task })
    return result.task
  }, [])

  const updateTask = useCallback(async (taskId: string, expectedRevision: number, draft: AutomationTaskDraft) => {
    const started = stateRef.current.context
    if (!started) throw new Error('自动化上下文还没有准备好')
    try {
      const result = await updateAutomationTask({ context: started, taskId, expectedRevision, draft })
      return acceptTask(result, started)
    } catch (e) {
      if (e instanceof Error && e.message === '身份已变化，请刷新后重试') throw e
      throw new Error(errText(e))
    }
  }, [acceptTask])

  const setPaused = useCallback(async (task: AutomationTask, paused: boolean) => {
    const started = stateRef.current.context
    if (!started) throw new Error('自动化上下文还没有准备好')
    setPendingTaskId(task.id)
    try {
      const result = await setAutomationTaskPaused({
        context: started,
        taskId: task.id,
        expectedRevision: task.revision,
        paused,
      })
      return acceptTask(result, started)
    } catch (e) {
      if (e instanceof Error && e.message === '身份已变化，请刷新后重试') throw e
      throw new Error(errText(e))
    } finally {
      setPendingTaskId('')
    }
  }, [acceptTask])

  const runNow = useCallback(async (task: AutomationTask) => {
    const started = stateRef.current.context
    if (!started) throw new Error('自动化上下文还没有准备好')
    setPendingTaskId(task.id)
    try {
      const result = await runAutomationTaskNow({
        context: started,
        taskId: task.id,
        expectedRevision: task.revision,
        clientRequestId: crypto.randomUUID(),
      })
      if (!sameAutomationContext(result.context, stateRef.current.context)) {
        throw new Error('身份已变化，请刷新后重试')
      }
    } catch (e) {
      if (e instanceof Error && e.message === '身份已变化，请刷新后重试') throw e
      throw new Error(errText(e))
    } finally {
      setPendingTaskId('')
    }
  }, [])

  const deleteTask = useCallback(async (task: AutomationTask) => {
    const started = stateRef.current.context
    if (!started) throw new Error('自动化上下文还没有准备好')
    setPendingTaskId(task.id)
    try {
      const result = await deleteAutomationTask({
        context: started,
        taskId: task.id,
        expectedRevision: task.revision,
      })
      if (!sameAutomationContext(result.context, stateRef.current.context)) {
        throw new Error('身份已变化，请刷新后重试')
      }
      dispatch({ type: 'remove', id: task.id })
    } catch (e) {
      if (e instanceof Error && e.message === '身份已变化，请刷新后重试') throw e
      throw new Error(errText(e))
    } finally {
      setPendingTaskId('')
    }
  }, [])

  const refresh = useCallback(() => setReload((n) => n + 1), [])

  const loadMore = useCallback(() => {
    const snap = stateRef.current
    const cursor = snap.list.nextCursor
    if (!cursor || !snap.context || snap.list.loading) return
    const ticket = ++appendTicket.current
    const generationAtStart = snap.list.generation
    const requested = snap.list.query
    const context = snap.context
    ;(async () => {
      try {
        const page = await listAutomationTasks({
          context,
          state: requested.filter || undefined,
          query: requested.text.trim() || undefined,
          cursor,
          limit: PAGE_LIMIT,
        })
        if (ticket !== appendTicket.current) return
        dispatch({ type: 'append', context: page.context, generation: generationAtStart, page })
      } catch (e) {
        if (ticket !== appendTicket.current) return
        dispatch({ type: 'fail', context, generation: generationAtStart, error: errText(e) })
      }
    })()
  }, [])

  const tasks = state.list.ids.map((id) => state.tasks[id]).filter((item): item is AutomationTask => !!item)
  const selectedTask = state.selectedTaskId ? state.tasks[state.selectedTaskId] ?? null : null

  return {
    context: state.context,
    tasks,
    selectedTask,
    selectedTaskId: state.selectedTaskId,
    pendingTaskId,
    loading: state.list.loading,
    error: state.list.error,
    unavailable: state.unavailable,
    filter,
    query: queryText,
    hasMore: !!state.list.nextCursor,
    createOpen,
    defaults,
    openCreate,
    closeCreate,
    saveCreate,
    updateTask,
    setPaused,
    runNow,
    deleteTask,
    notice,
    selectTask,
    setFilter,
    setQuery,
    refresh,
    loadMore,
  }
}
