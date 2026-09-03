// useSession 拥有"哪一个会话"：启动、新建、恢复、切工作区、删除，以及运行中可
// 变的会话设置(权限模式/计划模式/思考强度/模型)。对话内容归 use-conversation，
// 这里只在会话被替换时调它的 reset / applyResumed。
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  deleteSession,
  errText,
  listSessions,
  loadConfig,
  newSession,
  openSession,
  openSessions,
  focusSession,
  closeSession,
  sessionStatus,
  onEnvelope,
  Events,
  pickWorkspaceFolder,
  resumeSession,
  setPermissionMode,
  setPlanMode,
  setReasoningScenario,
  setThinkingEffort,
  startSession,
  switchModel,
  type ResumedSession,
  type SessionInfo,
  type OpenSessionInfo,
  type SessionSummary,
  type StartSessionRequest,
} from '@/core/bridge'
import { type ModelOption } from '@/ui/model-picker'
import { nextMode } from '@/core/permission-modes'


export type Session = ReturnType<typeof useSession>

export function useSession({ busy, conversation, showToast, onEnterChat }: {
  busy: boolean
  conversation: {
    reset: () => void
    applyResumed: (r: ResumedSession, isStale: () => boolean) => Promise<void>
    pushError: (text: string) => void
    // dropSession 丢掉一条会话的对话状态——关掉会话时用。
    dropSession: (id: string) => void
  }
  showToast: (text: string) => void
  onEnterChat: () => void
}) {
  const [info, setInfo] = useState<SessionInfo | null>(null)
  const [started, setStarted] = useState(false)
  const [starting, setStarting] = useState(false)
  const [startError, setStartError] = useState('')
  const [recents, setRecents] = useState<SessionSummary[]>([])
  const [initialReq, setInitialReq] = useState<Partial<StartSessionRequest> | null>(null)

  // Load the persisted start-form values so a restart prefills them.
  useEffect(() => {
    loadConfig()
      .then((r) => setInitialReq(r ?? {}))
      .catch(() => setInitialReq({}))
  }, [])

  async function refreshRecents() {
    try {
      const r = await listSessions()
      setRecents(r ?? [])
    } catch {
      /* 列表加载失败时静默保留旧值 */
    }
  }
  // Refresh the recent list when a session opens and whenever a turn finishes
  // (a turn persists/updates the session file).
  useEffect(() => {
    if (started && !busy) void refreshRecents()
  }, [started, busy])

  // 会话/工作区切换的并发防护：新建/恢复/切工作区都是"发请求→拿结果→整体替换
  // 会话状态"，用户快速连点两个目标时，先发但后落地的旧响应不得覆盖新会话
  // （否则界面静默回跳，且恢复流程第二段 await 的编辑元数据会合并进错误的
  // 会话）。每次发起切换代际 +1，每个 await 之后核对代际，过期响应整体丢弃；
  // switching 供入口回调挡住切换进行中的重复点击。
  const switchGen = useRef(0)
  const [switching, setSwitching] = useState(false)
  function beginSwitch() {
    const gen = ++switchGen.current
    setSwitching(true)
    return () => gen !== switchGen.current
  }
  function endSwitch(isStale: () => boolean) {
    if (!isStale()) setSwitching(false)
  }

  // ---- 多会话：同时开着的那些 ---------------------------------------------

  // openList 是"此刻开着哪几条会话"，**后端是唯一权威**。
  //
  // 前端自己记账迟早对不上：替换式打开（新建 / 切工作区 / 恢复历史）会关掉当前
  // 那条再开一条，失败的打开又不会留下任何东西。每次生命周期动作之后回读一次，
  // 比维护一份影子清单可靠得多，代价只是一次进程内调用。
  const [openList, setOpenList] = useState<OpenSessionInfo[]>([])
  // titles 是会话标题，来自 session:renamed（自动标题每个回合结束后刷新一次）。
  // 后端的 OpenSessionInfo 刻意不带标题——那会变成第二个事实源。
  const [titles, setTitles] = useState<Record<string, string>>({})

  const refreshOpen = useCallback(async () => {
    try {
      setOpenList((await openSessions()) ?? [])
    } catch {
      /* 读不到就保持原样：这只是列表，不该把一次失败变成对话里的错误块 */
    }
  }, [])

  useEffect(() => {
    void refreshOpen()
  }, [refreshOpen])

  useEffect(() => onEnvelope(Events.SessionRenamed, (env) => {
    const r = env.payload
    if (r?.id) setTitles((m) => ({ ...m, [r.id]: r.title }))
  }), [])

  // runSwitch 是三个切换动作共用的骨架：代际防护 + 失败落一条错误块 + 收尾。
  async function runSwitch(action: (isStale: () => boolean) => Promise<void>) {
    const isStale = beginSwitch()
    try {
      await action(isStale)
    } catch (e) {
      if (!isStale()) conversation.pushError(errText(e))
    } finally {
      endSwitch(isStale)
    }
  }

  async function start(req: StartSessionRequest) {
    setStarting(true)
    setStartError('')
    try {
      const i = await startSession(req)
      setInfo(i)
      setInitialReq(req)
      setStarted(true)
      // 首个会话也要进「打开中」列表。漏掉这一句的后果不只是列表空着：
      // 侧栏的「新建对话」曾按 openList 是否为空来决定"加开"还是"替换式打开"，
      // 列表空的时候它会走替换，把正在跑的回合当场关掉。
      void refreshOpen()
      // 回读配置:最近工作区(MRU)是后端在保存本次请求时合并出来的,表单里的 req 不含
      // 刚打开的这个目录。不回读,侧栏的「历史工作区」会一直停在启动前的那份。
      loadConfig().then((c) => { if (c) setInitialReq(c) }).catch(() => {})
    } catch (e) {
      setStartError(errText(e))
    } finally {
      setStarting(false)
    }
  }

  // returnToStart 把界面退回首屏(登录门 / 工作区选择)。退出登录后调用:清空当前会话
  // 的显示与 started 标志,让 App 重新渲染 StartForm,由它按新的登录态决定进登录门还是
  // 表单——登出后自然落到登录门(除非开了免登录)。后端会话不显式关闭(下次启动会新建
  // 或恢复),这里只重置外壳状态,并刷新预填值(登出后连接/模型可能已变)。
  function returnToStart() {
    setStarted(false)
    setInfo(null)
    setStartError('')
    conversation.reset()
    loadConfig().then((c) => setInitialReq(c ?? {})).catch(() => {})
  }

  const newChat = () =>
    runSwitch(async (isStale) => {
      const i = await newSession()
      if (isStale()) return
      setInfo(i)
      conversation.reset()
      void refreshRecents()
      void refreshOpen()
    })

  // openWorkspace 在**另一个目录**开一条会话，已经开着的那些一条都不动。
  //
  // 它以前叫 switchToWorkspace，语义是"把整个应用换到另一个目录"——先关掉当前
  // 会话（正在跑的回合当场没了），再在新目录重开。多工作区并行之后换目录只是
  // 「再开一条，开在别处」，两个项目可以同时挂着。要腾地方就从「打开中」里关，
  // 那是一个明确的动作。
  async function openWorkspace(dir: string) {
    const isStale = beginSwitch()
    try {
      const i = await openSession(dir)
      if (isStale()) return
      setInfo(i)
      onEnterChat()
      // 「最近对话」是按工作区列的，换了目录就是另一份；「历史工作区」(MRU) 由后端
      // 在打开时合并出来，得回读配置才能看到刚加进去的这个目录。
      void refreshRecents()
      void refreshOpen()
      loadConfig().then((c) => { if (c && !isStale()) setInitialReq(c) }).catch(() => {})
    } catch (e) {
      if (isStale()) return
      onEnterChat()
      conversation.pushError(errText(e))
    } finally {
      endSwitch(isStale)
    }
  }

  async function pickWorkspaceAndOpen() {
    try {
      const dir = await pickWorkspaceFolder()
      if (dir) await openWorkspace(dir)
    } catch { /* cancelled */ }
  }

  // 点「最近对话」里的一条：把它**也开起来**，已经开着的那些一条都不动
  // （后端的 ResumeSession 现在是加开一条，不再是替换式打开）。
  //
  // 这份列表与「打开中」天然重合——同一条会话既是工作区里存着的记录，也可能此刻
  // 正开着——所以先按"开着没有"分流：开着就只是切过去。后端也拦了同一件事
  // （focusIfAlreadyOpenHeld），这里拦是为了省掉那趟无谓的往返，顺带省掉一次把
  // 对话状态从引擎历史重放一遍（正在跑的回合尤其亏）。
  const openRecent = (id: string) => {
    if (id === info?.sessionId) return Promise.resolve()
    if (openList.some((s) => s.sessionId === id)) return focusOn(id)
    return runSwitch(async (isStale) => {
      const r = await resumeSession(id)
      if (isStale()) return
      setInfo(r.info)
      await conversation.applyResumed(r, isStale)
      if (isStale()) return
      void refreshRecents()
      void refreshOpen()
    })
  }

  // openAnother 在当前工作区**再开**一条会话，不关掉已经开着的那些。
  // 与 newChat 的区别就在这一句——那个是替换式打开。
  const openAnother = () =>
    runSwitch(async (isStale) => {
      const i = await openSession('')
      if (isStale()) return
      setInfo(i)
      void refreshRecents()
      void refreshOpen()
    })

  // focusOn 切到另一条**已经开着**的会话。
  //
  // 它不重建任何对话状态：状态按会话存着（见 session/conversation-state），切过去
  // 就是换一个键去读。这正是 P1a 那次分流的直接收益——在此之前切会话必须重放历史。
  const focusOn = (id: string) => {
    if (!id || id === info?.sessionId) return Promise.resolve()
    return runSwitch(async (isStale) => {
      const i = await focusSession(id)
      if (isStale()) return
      setInfo(i)
      void refreshOpen()
      // 「最近对话」是**按工作区**列的。切到另一个目录的会话后不重读的话，侧栏还挂着
      // 上一个目录的清单——点它一条就等于拿着甲目录的会话 id 去乙目录里恢复，
      // 结果是开出一条空对话。跨工作区并行之后这是必须的，同目录时它只是一次空转。
      void refreshRecents()
    })
  }

  // closeOne 关掉一条开着的会话。后端会把聚焦顺势落到还活着的另一条上，这里按
  // 回读到的结果采纳。
  //
  // **关掉最后一条时开一条新的空会话，而不是退回起始页。** 起始页是"首屏"——它会
  // 整个重挂载、重跑登录门与工作区选择、清掉界面上的一切。而用户做的只是"把手上
  // 这条对话收掉"，工作区、连接、模型一样都没变，凭什么要他从头再来一遍。这与
  // 浏览器关掉最后一个标签页给你一个新标签页是同一个道理。
  // 只有连新会话都开不出来（没有工作区/没有可用模型，比如还没真正启动过）才回首屏。
  const closeOne = (id: string) =>
    runSwitch(async (isStale) => {
      await closeSession(id)
      conversation.dropSession(id)
      const list = (await openSessions()) ?? []
      if (isStale()) return
      setOpenList(list)
      const next = list.find((s) => s.focused) ?? list[0]
      if (next) {
        setInfo(await sessionStatus(next.sessionId))
        return
      }
      try {
        const fresh = await openSession('')
        if (isStale()) return
        setInfo(fresh)
        void refreshOpen()
      } catch {
        if (isStale()) return
        setInfo(null)
        setStarted(false)
      }
    })

  async function deleteRecent(id: string) {
    try {
      await deleteSession(id)
      void refreshRecents()
      showToast('会话已删除')
    } catch (e) {
      showToast(`删除失败：${errText(e)}`)
    }
  }

  // applyUpdate 是所有"改一个会话开关"的统一骨架：调后端 → 拿到完整状态就整体
  // 采纳 → 失败落一条错误块。计划模式/思考场景/思考强度原先各写了一遍同样的
  // try/catch，现在只有这一处。
  async function applyUpdate(call: () => Promise<SessionInfo | null>) {
    if (!info) return
    try {
      const i = await call()
      if (i?.model) setInfo(i)
    } catch (e) {
      conversation.pushError(errText(e))
    }
  }

  // 这几个都作用于当前这条会话；info 还没到位时用空串，后端会退回聚焦会话。
  const sid = () => info?.sessionId ?? ''
  const togglePlan = () => applyUpdate(() => setPlanMode(sid(), !info?.planMode))
  const chooseReasoning = (scenario: string) => applyUpdate(() => setReasoningScenario(sid(), scenario))
  const chooseThinking = (effort: string) => applyUpdate(() => setThinkingEffort(sid(), effort))

  // pickMode activates a specific permission mode (used by the permissions page's
  // mode cards), keeping the local session info in sync with the backend.
  async function pickMode(mode: string) {
    if (!info || info.permissionMode === mode) return
    try {
      await setPermissionMode(sid(), mode)
      setInfo({ ...info, permissionMode: mode })
    } catch (e) {
      conversation.pushError(errText(e))
    }
  }
  const toggleMode = () => {
    if (!info) return
    // 顺序与「哪几种可选」都在 core/permission-modes：隐藏一种模式时这里不必改。
    void pickMode(nextMode(info.permissionMode))
  }

  async function pickModel(choice: ModelOption) {
    try {
      // Adopt the full returned status: switching to/from a custom model rebuilds the
      // session, which rebinds the preview server to a new port — so previewBaseURL
      // (and sessionId) must be refreshed, not just the model, or the preview iframe
      // keeps loading the dead port and shows “拒绝连接”.
      const st = await switchModel(info?.sessionId ?? '', choice.kind, choice.id)
      setInfo((prev) => (st ? { ...prev, ...st } : prev))
      // The backend persists the switched connection; refresh the cached start-form
      // values so the Settings page (and start page) reflect the new connection
      // instead of the pre-switch one they were opened with.
      loadConfig().then((c) => setInitialReq(c ?? {})).catch(() => {})
    } catch (e) {
      // A cross-connection switch can legitimately fail (not logged in, a turn in
      // flight): say so instead of silently doing nothing, which reads as "I can't
      // switch here, I must start a new session".
      showToast(`切换模型失败：${errText(e)}`)
    }
  }

  // 计划模式的"确认执行"不在这里：退出计划模式、切权限模式、拼执行指令是一个原子
  // 动作，整件事由后端的 PlanApprove 一次做完（见 session/use-plan），这里只负责把
  // 它返回的会话状态采纳进来。

  return {
    info, setInfo, started, starting, startError, recents, initialReq, setInitialReq, switching,
    openList, titles, openAnother, focusOn, closeOne,
    start, newChat, openRecent, openWorkspace, pickWorkspaceAndOpen, deleteRecent, returnToStart,
    togglePlan, chooseReasoning, chooseThinking, pickMode, toggleMode, pickModel,
  }
}
