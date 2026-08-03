// 审批区那份"可编辑清单"的纯逻辑：增删步骤、上下移、改写、以及能不能提交。
// 与渲染分开是因为顺序调整和增删是这功能里最容易写错、也最值得单测的部分——
// 组件里混着 DOM 事件写这些，边界（第一条上移、最后一条下移、删到空）就没人守了。
import type { PlanDoc, PlanStep } from '@/core/bridge'

// draftKey 给新加的步骤一个前端本地 id：后端只给已记录的步骤发 id，新步骤要等
// 保存后才拿到正式 id，但 React 的列表 key 现在就需要一个稳定值。
let localSeq = 0
export const newStepID = () => `new${++localSeq}`

// blankStep 是"新增步骤"按钮插入的空步骤。
export const blankStep = (): PlanStep => ({ id: newStepID(), title: '' })

// moveStep 把第 index 步移动 delta 格（-1 上移 / +1 下移）。越界时原样返回，
// 调用方因此可以无条件调用，按钮的禁用只是视觉提示。
export function moveStep(steps: PlanStep[], index: number, delta: number): PlanStep[] {
  const to = index + delta
  if (index < 0 || index >= steps.length || to < 0 || to >= steps.length) return steps
  const out = [...steps]
  const [moved] = out.splice(index, 1)
  out.splice(to, 0, moved)
  return out
}

// removeStep 删掉第 index 步；越界原样返回。
export function removeStep(steps: PlanStep[], index: number): PlanStep[] {
  if (index < 0 || index >= steps.length) return steps
  return steps.filter((_, i) => i !== index)
}

// insertStepAfter 在第 index 步之后插入一个空步骤（index 为 -1 时插到最前）。
export function insertStepAfter(steps: PlanStep[], index: number): PlanStep[] {
  const at = Math.max(-1, Math.min(index, steps.length - 1)) + 1
  const out = [...steps]
  out.splice(at, 0, blankStep())
  return out
}

// patchStep 改写第 index 步的某些字段。
export function patchStep(steps: PlanStep[], index: number, patch: Partial<PlanStep>): PlanStep[] {
  if (index < 0 || index >= steps.length) return steps
  return steps.map((s, i) => (i === index ? { ...s, ...patch } : s))
}

// cleanDoc 是提交前的整理：去掉标题空白的步骤（用户加了一条又没写就直接丢弃，
// 而不是拿一条空步骤去烦模型），逐字段 trim。
export function cleanDoc(doc: PlanDoc): PlanDoc {
  const steps = (doc.steps ?? [])
    .map((s) => ({
      ...s,
      title: (s.title ?? '').trim(),
      detail: (s.detail ?? '').trim() || undefined,
      files: (s.files ?? []).map((f) => f.trim()).filter(Boolean),
    }))
    .filter((s) => s.title !== '')
    .map((s) => ({ ...s, files: s.files && s.files.length ? s.files : undefined }))
  return { ...doc, steps }
}

// canApprove 报告这份草稿能不能提交执行：至少要有一条有标题的步骤。空清单点确认
// 只会让模型无所适从，不如在按钮上就拦住。
export function canApprove(doc: PlanDoc | null | undefined): boolean {
  return !!doc && (doc.steps ?? []).some((s) => (s.title ?? '').trim() !== '')
}

// dirty 报告草稿与后端那一版是否已经不同（决定要不要在确认前先存一次编辑）。
// 只比较真正会下发给模型的内容，忽略 id 这种前端记账字段。
export function dirty(draft: PlanDoc | null, saved: PlanDoc | null | undefined): boolean {
  if (!draft) return false
  if (!saved) return true
  return payloadOf(draft) !== payloadOf(saved)
}

function payloadOf(doc: PlanDoc): string {
  return JSON.stringify(
    (cleanDoc(doc).steps ?? []).map((s) => [s.title, s.detail ?? '', (s.files ?? []).join(',')]),
  )
}
