// useSession 拥有"哪一个会话"：启动、新建、恢复、切工作区、删除，以及运行中可
// 变的会话设置(权限模式/计划模式/思考强度/模型)。对话内容归 use-conversation，
// 这里只在会话被替换时调它的 reset / applyResumed。
import { useEffect, useRef, useState } from 'react'
import {
  deleteSession,
  errText,
  listSessions,
  loadConfig,
  newSession,
  pickWorkspaceFolder,
  resumeSession,
  setPermissionMode,
  setPlanMode,
  setReasoningScenario,
  setThinkingEffort,
  startSession,
  switchModel,
  switchWorkspace,
  type ResumedSession,
  type SessionInfo,
  type SessionSummary,
  type StartSessionRequest,
} from '@/core/bridge'
import { type ModelOption } from '@/ui/model-picker'

// 权限模式的循环顺序（工具条上点一下切下一个）。
const MODE_ORDER = ['safe', 'interactive', 'judge', 'flight']

export type Session = ReturnType<typeof useSession>

export function useSession({ busy, conversation, showToast, onEnterChat, onWorkspaceChanged }: {
  busy: boolean
  conversation: {
    reset: () => void
    applyResumed: (r: ResumedSession, isStale: () => boolean) => Promise<void>
    pushError: (text: string) => void
  }
  showToast: (text: string) => void
  onEnterChat: () => void
  // 工作区被换掉时调用。预览标签是"工作区"生命周期的(存的是工作区相对路径 +
  // 编辑快照 id),换工作区后这些引用全部失效——不清掉的话,同名文件会在旧标签里
  // 静默显示成新工作区的内容。新建/恢复会话不触发:那是同一个工作区。
  onWorkspaceChanged: () => void
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
    })

  // Pick a different folder and open a fresh session there (a new workspace).
  // 成功与失败都回到对话视图——失败时那条错误块就在对话里等着看；过期的响应
  // 两件事都不做，由那个更新的切换自己收尾。
  async function switchToWorkspace(dir: string) {
    const isStale = beginSwitch()
    try {
      const i = await switchWorkspace(dir)
      if (isStale()) return
      setInfo(i)
      conversation.reset()
      onWorkspaceChanged()
      onEnterChat()
      void refreshRecents()
      // refresh recent-workspace MRU
      loadConfig().then((c) => { if (c && !isStale()) setInitialReq(c) }).catch(() => {})
    } catch (e) {
      if (isStale()) return
      onEnterChat()
      conversation.pushError(errText(e))
    } finally {
      endSwitch(isStale)
    }
  }

  async function pickWorkspaceAndSwitch() {
    try {
      const dir = await pickWorkspaceFolder()
      if (dir) await switchToWorkspace(dir)
    } catch { /* cancelled */ }
  }

  const openRecent = (id: string) => {
    if (id === info?.sessionId) return Promise.resolve()
    return runSwitch(async (isStale) => {
      const r = await resumeSession(id)
      if (isStale()) return
      setInfo(r.info)
      await conversation.applyResumed(r, isStale)
      if (isStale()) return
      void refreshRecents()
    })
  }

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

  const togglePlan = () => applyUpdate(() => setPlanMode(!info?.planMode))
  const chooseReasoning = (scenario: string) => applyUpdate(() => setReasoningScenario(scenario))
  const chooseThinking = (effort: string) => applyUpdate(() => setThinkingEffort(effort))

  // pickMode activates a specific permission mode (used by the permissions page's
  // mode cards), keeping the local session info in sync with the backend.
  async function pickMode(mode: string) {
    if (!info || info.permissionMode === mode) return
    try {
      await setPermissionMode(mode)
      setInfo({ ...info, permissionMode: mode })
    } catch (e) {
      conversation.pushError(errText(e))
    }
  }
  const toggleMode = () => {
    if (!info) return
    void pickMode(MODE_ORDER[(MODE_ORDER.indexOf(info.permissionMode || 'safe') + 1) % MODE_ORDER.length])
  }

  async function pickModel(choice: ModelOption) {
    try {
      // Adopt the full returned status: switching to/from a custom model rebuilds the
      // session, which rebinds the preview server to a new port — so previewBaseURL
      // (and sessionId) must be refreshed, not just the model, or the preview iframe
      // keeps loading the dead port and shows “拒绝连接”.
      const st = await switchModel(choice.kind, choice.id)
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

  // executePlanAs 退出计划模式、切到选定的权限模式，随后由调用方发出执行指令。
  // 返回 false 表示切换失败(错误已入对话流)，此时不应再发消息。
  async function leavePlanMode(mode: string): Promise<boolean> {
    try {
      await setPermissionMode(mode)
      const i = await setPlanMode(false)
      if (i?.model) setInfo(i)
      else setInfo((prev) => (prev ? { ...prev, permissionMode: mode, planMode: false } : prev))
      return true
    } catch (e) {
      conversation.pushError(errText(e))
      return false
    }
  }

  return {
    info, setInfo, started, starting, startError, recents, initialReq, setInitialReq, switching,
    start, newChat, openRecent, switchToWorkspace, pickWorkspaceAndSwitch, deleteRecent, returnToStart,
    togglePlan, chooseReasoning, chooseThinking, pickMode, toggleMode, pickModel, leavePlanMode,
  }
}
