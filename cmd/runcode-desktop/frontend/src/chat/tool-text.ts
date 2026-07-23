// tool-text 是工具事件的纯文本层：把一次工具调用翻译成界面要显示的中文动词、
// 目标、失败原因、增删行数等。全是纯函数(不吃 React、不碰 DOM)，所以能直接单测，
// 也让上层卡片组件只管排版。
import { basename } from '@/core/paths'
import { toolVerb } from '@/core/tool-catalog'
import type { ToolEvent } from '@/core/bridge'
import type { AgentNested } from './blocks'

// parseToolInput 是所有工具入参解析的唯一入口：实时事件带的是已解析对象，恢复
// 出来的历史会话带的是 JSON 字符串，两种形态都要吃。解析失败一律退回空对象，
// 调用方永远拿到可安全取字段的值——原先 4 处各写一遍 try/JSON.parse 的容错就此
// 收敛到这里。
export function parseToolInput<T extends object = Record<string, unknown>>(input: unknown): Partial<T> {
  if (input == null || input === '') return {}
  if (typeof input === 'string') {
    try {
      const parsed: unknown = JSON.parse(input)
      return parsed && typeof parsed === 'object' ? (parsed as Partial<T>) : {}
    } catch {
      return {}
    }
  }
  return typeof input === 'object' ? (input as Partial<T>) : {}
}

// toolInputObj parses a tool call's arguments into a plain object.
export function toolInputObj(t: ToolEvent): Record<string, unknown> {
  return parseToolInput((t as ToolEvent & { input?: unknown }).input)
}

const clip = (s: string, n: number) => (s.length > n ? s.slice(0, n) + '…' : s)

// toolVerbTarget splits a tool call into its Chinese verb and its most useful target
// (command / pattern / host / file), so a row can style the two differently (verb in
// ink, target in mono).
export function toolVerbTarget(t: ToolEvent): { verb: string; target: string } {
  const verb = toolVerb(t.toolName)
  const o = toolInputObj(t)
  let target = ''
  switch (t.toolName) {
    case 'Bash':
      target = clip(String(o.command ?? '').replace(/\s+/g, ' ').trim(), 56)
      break
    case 'Grep':
    case 'Glob':
      target = clip(String(o.pattern ?? ''), 44)
      break
    case 'WebFetch':
      try { target = new URL(String(o.url ?? '')).host } catch { target = clip(String(o.url ?? ''), 44) }
      break
    case 'WebSearch':
      target = clip(String(o.query ?? ''), 44)
      break
    case 'Wait':
      target = o.seconds != null ? `${o.seconds}s` : ''
      break
    default:
      target = basename(t.files?.[0]?.path) || basename(String(o.path ?? ''))
  }
  return { verb, target }
}

// toolTargetPath returns the file a Write/Edit acted on (absolute or workspace-
// relative), for wiring a preview affordance. Returns undefined when there is none.
export function toolTargetPath(t: ToolEvent): string | undefined {
  const fromFiles = t.files?.[0]?.path
  if (fromFiles) return fromFiles
  const p = toolInputObj(t).path
  return typeof p === 'string' && p ? p : undefined
}

// toolLabel is the flat "verb target +adds -dels" string for places that need plain
// text (sub-agent rows, titles).
export function toolLabel(t: ToolEvent): string {
  const { verb, target } = toolVerbTarget(t)
  const { add, del } = diffStats(t)
  const diff = add + del > 0 ? `  +${add} -${del}` : ''
  return (target ? `${verb} ${target}` : verb) + diff
}

export const diffStats = (t: ToolEvent) => {
  let add = 0, del = 0
  for (const l of t.output ?? []) {
    if (l.stream === 'diff_add') add++
    else if (l.stream === 'diff_del') del++
  }
  return { add, del }
}

export const hasDiff = (t: ToolEvent) => {
  const { add, del } = diffStats(t)
  return add + del > 0
}

// Translate the executor's failure message into a short Chinese reason.
const DENY_REASON: Record<string, string> = {
  read_required: '需先读取该文件再写入',
  write_exists: '当前文件已存在',
  read_stale: '文件已变化，请重新读取',
  approval_denied: '已拒绝',
  approval_unavailable: '安全模式下不可执行',
  outside_workspace: '路径在工作区之外',
  denylisted: '已被拒止规则阻止',
  policy_denied: '策略不允许',
  invalid_target: '目标无效',
  invalid_input: '输入无效',
  unknown_tool: '未知工具',
  requires_approval: '需要审批',
}

const FAIL: Record<string, string> = {
  'blocked by hook': '被钩子拦截',
  cancelled: '已取消',
  failed: '执行失败',
  'completed with error': '执行失败',
}

export function failText(t: ToolEvent): string {
  const m = t.message ?? ''
  if (m.startsWith('denied:')) {
    return DENY_REASON[m.slice('denied:'.length)] ?? '权限被拒绝'
  }
  return FAIL[m] ?? (m || '失败')
}

// lineClass styles one line of a tool's captured output by its stream.
export function lineClass(stream?: string): string {
  const base = 'px-2.5 whitespace-pre'
  switch (stream) {
    case 'stderr':
      return base + ' text-red'
    case 'info':
      return base + ' text-faint'
    case 'match':
      return base + ' text-[#957419]'
    default:
      return base + ' text-muted'
  }
}

// analyzeSteps parses an Analyze tool call's input into the method and its filled
// steps. Steps carry a human label when the backend enriched the event; otherwise
// the key stands in.
export function analyzeSteps(input: unknown): { method?: string; steps: { key: string; label?: string; content: string }[] } {
  const obj = parseToolInput<{ method?: string; steps?: { key?: string; label?: string; content?: string }[] }>(input)
  const steps = Array.isArray(obj.steps)
    ? obj.steps.map((s) => ({ key: String(s?.key ?? ''), label: s?.label ? String(s.label) : undefined, content: String(s?.content ?? '') }))
    : []
  return { method: obj.method ? String(obj.method) : undefined, steps }
}

// askPayload pulls the question and options out of an AskUser tool call's input.
export function askPayload(input: unknown): { question: string; options: string[] } {
  const obj = parseToolInput<{ question?: string; options?: string[] }>(input)
  return { question: obj.question ?? '', options: Array.isArray(obj.options) ? obj.options : [] }
}

// taskMeta extracts a Task call's display fields (sub-agent name, description).
export function taskMeta(tool: ToolEvent, nested?: AgentNested): { sub: string; desc: string } {
  const o = parseToolInput<{ subagent_type?: string; description?: string }>((tool as ToolEvent & { input?: unknown }).input)
  return { sub: o.subagent_type || nested?.agent || '子代理', desc: o.description || '' }
}

// taskActivity summarizes what a running sub-agent is doing right now — its active
// child tool, else the last line it streamed — for the compact taskgroup row.
export function taskActivity(nested?: AgentNested): string {
  if (!nested) return ''
  for (let i = nested.tools.length - 1; i >= 0; i--) {
    const ct = nested.tools[i]
    if (ct.type !== 'completed' && ct.type !== 'failed') return toolLabel(ct)
  }
  const lines = nested.text.split('\n')
  for (let i = lines.length - 1; i >= 0; i--) {
    const s = lines[i].trim()
    if (s) return s
  }
  return ''
}

// formatInput renders a tool call's raw arguments as pretty JSON, preserving an
// unparseable string verbatim rather than swallowing it (so RawJson can still show
// what actually arrived).
export function formatInput(input: unknown): string {
  if (input == null || input === '') return ''
  if (typeof input === 'string') {
    try {
      return JSON.stringify(JSON.parse(input), null, 2)
    } catch {
      return input
    }
  }
  try {
    return JSON.stringify(input, null, 2)
  } catch {
    return String(input)
  }
}
