// usePlan 拥有阶段化计划模式的前端状态：后端那份运行状态（阶段进度、计划文档），
// 以及用户在审批区里正在编辑的草稿。它不碰对话——确认后要发出去的那条执行指令由
// 后端拼好返回，交给调用方走既有的 send 路径，这样 busy、用户气泡、回合生命周期
// 全部复用原链路，不在这里另起一套。
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Events,
  PlanStates,
  errText,
  onEnvelope,
  planApprove,
  planCancel,
  planStatus,
  planUpdate,
  type PlanDoc,
  type PlanRun,
  type PlanStep,
  type SessionInfo,
} from '@/core/bridge'
import { cleanDoc, dirty, insertStepAfter, moveStep, patchStep, removeStep } from '@/chat/plan-draft'

const IDLE: PlanRun = { state: PlanStates.Idle }

export type Plan = ReturnType<typeof usePlan>

export function usePlan({ sessionId, onSend, onApproved, showToast }: {
  sessionId?: string
  // onSend 发出后端拼好的执行指令(确认方案后的第一条消息)。
  onSend: (text: string) => void
  // onApproved 采纳审批带来的会话变化(计划模式已关、权限模式已切)。
  onApproved: (info: SessionInfo) => void
  showToast: (text: string) => void
}) {
  const [run, setRun] = useState<PlanRun>(IDLE)
  const [draft, setDraft] = useState<PlanDoc | null>(null)
  const [approving, setApproving] = useState(false)

  // 草稿的基线：模型每记一个阶段就以新文档为准重置草稿（它刚刚推翻了旧的）。
  // 但我们自己保存的回声不能重置——保存前会 cleanDoc，用户刚点出来还没写字的空步骤
  // 会被剔掉，拿回声覆盖草稿就等于在用户眼皮底下把那一行删了。run.edited 正是"这一版
  // 是用户的"这个标记，据此区分两种来源。用 updatedAt 判变化而不是对象引用：事件与
  // 命令返回值是两个不同的对象，描述的却是同一次变更。
  const baseAt = useRef<string>('')
  useEffect(() => {
    const at = run.updatedAt ?? ''
    if (at === baseAt.current) return
    baseAt.current = at
    if (run.edited) return // 我们自己那次保存的回声
    setDraft(run.doc ? { ...run.doc, steps: [...(run.doc.steps ?? [])] } : null)
  }, [run])

  // 事件订阅只注册一次：引擎/外壳事件是进程级的，重订阅会漏事件。
  //
  // 只收**当前这条会话**的计划更新。计划板是后端权威状态（切会话时下面那个
  // effect 会回读一次），所以前端不必再按会话缓存一份；但必须挡住别的会话——
  // 后台会话记一个阶段就把前台的板子盖掉，用户会看到一份不属于这条对话的方案。
  const sidRef = useRef(sessionId)
  sidRef.current = sessionId
  useEffect(() => onEnvelope(Events.PlanUpdated, (env) => {
    if ((env.sessionId ?? '') !== (sidRef.current ?? '')) return
    setRun(env.payload ?? IDLE)
  }), [])

  // 换会话（含恢复历史会话）时回读一次：等待审批的计划能跨重启活下来，界面必须
  // 跟着回到那道闸门前。
  useEffect(() => {
    let stale = false
    planStatus(sessionId ?? '')
      .then((r) => { if (!stale) { baseAt.current = ''; setRun(r ?? IDLE) } })
      .catch(() => { if (!stale) setRun(IDLE) })
    return () => { stale = true }
  }, [sessionId])

  // commit 把结构性改动（增删、移动）立刻落到后端，文本改动则在失焦时落。审批区
  // 可能停留很久，中途关掉应用不该丢掉用户已经排好的顺序。
  const commit = useCallback((next: PlanDoc) => {
    if (!dirty(next, run.doc)) return
    planUpdate(sidRef.current ?? '', cleanDoc(next)).catch((e) => showToast(`计划保存失败：${errText(e)}`))
  }, [run.doc, showToast])

  // edit 是所有编辑动作的统一入口：先更新本地草稿（输入不能有延迟），需要时同步后端。
  const edit = useCallback((fn: (steps: PlanStep[]) => PlanStep[], persist: boolean) => {
    setDraft((prev) => {
      const base = prev ?? { steps: [] }
      const next = { ...base, steps: fn(base.steps ?? []) }
      if (persist) commit(next)
      return next
    })
  }, [commit])

  const actions = {
    move: (index: number, delta: number) => edit((s) => moveStep(s, index, delta), true),
    remove: (index: number) => edit((s) => removeStep(s, index), true),
    insertAfter: (index: number) => edit((s) => insertStepAfter(s, index), false),
    patch: (index: number, patch: Partial<PlanStep>) => edit((s) => patchStep(s, index, patch), false),
    // flush 在文本框失焦时调用：把这一轮输入落到后端。
    flush: () => { if (draft) commit(draft) },
  }

  // approve 跨过审批闸门：后端存下用户这一版、退出计划模式、切到选定的权限模式，
  // 并把执行指令拼好返回；这里只负责把它发出去。
  async function approve(permissionMode: string) {
    if (!draft || approving) return
    setApproving(true)
    try {
      const res = await planApprove(sidRef.current ?? '', { doc: cleanDoc(draft), permissionMode })
      // 先采纳状态再发消息：计划模式必须在这一轮开始前就是关闭的，否则工具栏会闪一下
      // 「计划模式」，后端也会把这条执行指令当成新一轮规划的开端。
      if (res?.info) onApproved(res.info)
      if (res?.executionPrompt) onSend(res.executionPrompt)
    } catch (e) {
      showToast(`确认方案失败：${errText(e)}`)
    } finally {
      setApproving(false)
    }
  }

  // resume 催模型把没跑完的流水线接着跑完。模型本该在一个回合里连跑三个阶段（每次
  // plan_write 的返回值就是下一阶段的指令），但它也可能中途收尾——那时界面停在半截，
  // 用户需要一个明确的"接着来"，而不是自己揣摩该说什么。
  function resume() {
    onSend('继续按阶段推进方案规划：接着调用 plan_write 记录下一阶段，直到方案审查完成。')
  }

  async function cancel() {
    try {
      setRun(await planCancel(sidRef.current ?? ''))
    } catch (e) {
      showToast(`取消计划失败：${errText(e)}`)
    }
  }

  // 新建会话 / 切工作区不需要显式复位：sessionId 一变，上面的回读就会把新会话
  // （空的）运行状态取回来。
  return { run, draft, approving, actions, approve, cancel, resume }
}
