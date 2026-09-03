// 权限模式的单一清单：有哪几种、叫什么、当下提供哪几种。
//
// 收在一处是因为「暂时隐藏一种模式」原先要同时改四个地方——工具条的名字表、切换
// 顺序、设置页的下拉、权限页的对比表。四处各写一份的后果不是漏改一处那么简单：
// 权限页的矩阵是**按下标**对齐的，那里漏掉或多算一列，表格看着完全正常，只是每个
// 格子说的是隔壁那个模式的事。

/** PermissionMode 是权限模式的键，与后端 internal/permissions 的取值一致。 */
export type PermissionMode = 'safe' | 'interactive' | 'judge' | 'flight'

/** ALL_PERMISSION_MODES 是全部模式，**按开放程度排序**（切换顺序也用它）。 */
export const ALL_PERMISSION_MODES: { key: PermissionMode; label: string }[] = [
  { key: 'safe', label: '安全模式' },
  { key: 'interactive', label: '交互模式' },
  { key: 'judge', label: '智能模式' },
  { key: 'flight', label: '飞行模式' },
]

/**
 * HIDDEN_PERMISSION_MODES 是暂时不提供给用户选的模式。
 *
 * **清空这个数组即整体恢复**——名字表、切换顺序、设置页下拉、权限页对比表全都读它。
 *
 * 安全模式当前隐藏：它拒绝一切写改与命令，实际能干的事很少，而它排在切换顺序的第一
 * 个，很容易被误切进去，然后表现为"模型什么都不做也不报错"。
 */
export const HIDDEN_PERMISSION_MODES: PermissionMode[] = ['safe']

/** MODE_LABEL 认得**全部**模式，包括隐藏的。 */
export const MODE_LABEL: Record<string, string> =
  Object.fromEntries(ALL_PERMISSION_MODES.map((m) => [m.key, m.label]))

/** DEFAULT_MODE 是没有会话信息时显示的那个，与后端 defaultRequest() 保持一致。 */
export const DEFAULT_MODE: PermissionMode = 'interactive'

/**
 * offeredModes 是当前提供给用户选的模式。
 *
 * current 传入时**一定包含它**，哪怕它是隐藏的：会话可能是旧版本留下来的、或者从
 * 配置文件里读进来的，此时下拉框里没有对应项会显示成空白，用户一动就把模式改掉了。
 * 隐藏的意思是「不主动提供」，不是「假装它不存在」。
 */
export function offeredModes(current?: string): { key: PermissionMode; label: string }[] {
  return ALL_PERMISSION_MODES.filter(
    (m) => m.key === current || !HIDDEN_PERMISSION_MODES.includes(m.key),
  )
}

/**
 * nextMode 是工具条上点一下切到的那个：在 offeredModes 里向后循环一格。
 *
 * current 是隐藏模式时（indexOf 得 -1）落到第一个可选项，这样点一下就能从隐藏模式
 * 出来，而且出来之后再也回不去——正是「暂时隐藏」该有的行为。
 */
export function nextMode(current?: string): PermissionMode {
  const offered = ALL_PERMISSION_MODES.filter((m) => !HIDDEN_PERMISSION_MODES.includes(m.key))
  if (offered.length === 0) return DEFAULT_MODE
  const at = offered.findIndex((m) => m.key === current)
  return offered[(at + 1) % offered.length].key
}
