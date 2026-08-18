// 权限弹窗能给出哪些答案，由后端说了算（PermissionRequest.allowedDecisions）。
//
// 这不是提示而是限制：没列出的答案发回去会被接受，然后什么都不记——用户选了
// 「不要再问」，下次照问不误，且没有任何东西能解释这件事。所以这里只按后端给的
// 清单渲染按钮。
//
// 两条兜底各有各的理由：
//   · 清单缺失（老后端）= 未声明，给全部四个，这是这个字段出现之前的行为；
//   · 「拒绝」永远补上——被限制的是"允许能记多远"，从来不是"能不能拒绝"，
//     一个没有拒绝按钮的授权弹窗是用户答不了的问题。
export type PermissionDecision = 'allow-session' | 'allow-once' | 'allow-project' | 'deny'

// 显示顺序：推荐项在前，拒绝殿后。过滤只会从中拿掉，不会改变相对次序。
const DECISION_ORDER: PermissionDecision[] = ['allow-session', 'allow-once', 'allow-project', 'deny']

export function decisionOptions(allowed?: string[] | null): PermissionDecision[] {
  if (!allowed || allowed.length === 0) return DECISION_ORDER
  // 清单里有而这里不认识的答案直接丢掉：能画出来的按钮只有这四个，画不出来的
  // 答案留在清单里也没人能选。反过来，认识但没列出的一律不给。
  return DECISION_ORDER.filter((d) => d === 'deny' || allowed.includes(d))
}

// 这次授权能不能被记住。记不住时弹窗里那段"「本次会话」记的是……"的说明必须换掉：
// 它解释的是一个此刻并不存在的选项。
export function canRemember(decisions: PermissionDecision[]): boolean {
  return decisions.includes('allow-session') || decisions.includes('allow-project')
}
