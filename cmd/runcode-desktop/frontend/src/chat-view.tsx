// chat-view 是对话流的渲染层：把 chat.ts 分组后的 Block 序列画成消息卡片——
// 助手回复、工具执行卡（ExecutionCard/ToolDetail）、子代理任务卡（AgentTask*）、
// 提问卡（AskCard）、计划进度（PlanPill）等，全部是纯展示组件：只吃 props、
// 不碰 App 的会话状态。从 App.tsx 原样搬出，行为不变。
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Icon, toolIcon } from './icons'
import { Markdown } from './markdown'
import { type PlanItem, type PlanSnapshot, type ToolEvent } from './bridge'
import { type AgentNested, type Block, type ToolBlock } from './chat'
import { CollapsibleGroup, DiffStat } from './components'
import { ArtifactCard } from './artifact-card'
import { buildFileTree, classifyPreview, extractFilePaths, fileColor, kindIcon, matchWorkspaceFiles, type FileNode } from './preview'
import { type PreviewTab } from './preview-tabs'
import { basename } from './paths'
import { fmtDuration, fmtTokens } from './format'
import { NO_DRAG } from './ui'

const VERB: Record<string, string> = {
  Read: '读取文件', Write: '写入文件', Edit: '编辑', Delete: '删除', Glob: '查找文件', Grep: '搜索项目代码',
  Bash: '运行命令', BashOutput: '后台输出', KillShell: '终止命令', WebFetch: '抓取网页',
  WebSearch: '联网搜索', Wait: '等待', GetCurrentTime: '获取当前时间',
  TodoWrite: '规划任务', Task: '委派子代理', Skill: '加载技能', Remember: '记录记忆', Analyze: '结构化分析',
  open_preview: '预览',
}
const clip = (s: string, n: number) => (s.length > n ? s.slice(0, n) + '…' : s)
// toolInputObj parses a tool call's arguments into an object (live events carry the
// parsed value; resumed sessions carry a JSON string).
function toolInputObj(t: ToolEvent): Record<string, unknown> {
  const raw = (t as ToolEvent & { input?: unknown }).input
  if (raw == null) return {}
  if (typeof raw === 'string') { try { return JSON.parse(raw) } catch { return {} } }
  return typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
}
// toolVerbTarget splits a tool call into its Chinese verb and its most useful target
// (command / pattern / host / file), so a row can style the two differently (verb in
// ink, target in mono).
function toolVerbTarget(t: ToolEvent): { verb: string; target: string } {
  const verb = VERB[t.toolName || ''] || t.toolName || '工具'
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
// text (resumed-session rebuild, titles).
function toolLabel(t: ToolEvent): string {
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
const hasOutput = (t: ToolEvent) => (t.output?.length ?? 0) > 0

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
function failText(t: ToolEvent): string {
  const m = t.message ?? ''
  if (m.startsWith('denied:')) {
    return DENY_REASON[m.slice('denied:'.length)] ?? '权限被拒绝'
  }
  return FAIL[m] ?? (m || '失败')
}

function lineClass(stream?: string): string {
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
// steps, tolerating the live object form and the JSON-string form (resumed). Steps
// carry a human label when the backend enriched the event; otherwise the key stands
// in.
function analyzeSteps(input: unknown): { method?: string; steps: { key: string; label?: string; content: string }[] } {
  let obj: { method?: string; steps?: { key?: string; label?: string; content?: string }[] } = {}
  if (typeof input === 'string') {
    try {
      obj = JSON.parse(input)
    } catch {
      obj = {}
    }
  } else if (input && typeof input === 'object') {
    obj = input as typeof obj
  }
  const steps = Array.isArray(obj.steps)
    ? obj.steps.map((s) => ({ key: String(s?.key ?? ''), label: s?.label ? String(s.label) : undefined, content: String(s?.content ?? '') }))
    : []
  return { method: obj.method ? String(obj.method) : undefined, steps }
}

// askPayload pulls the question and options out of an AskUser tool call's input,
// tolerating both the live object form and the JSON-string form (resumed).
function askPayload(input: unknown): { question: string; options: string[] } {
  let obj: { question?: string; options?: string[] } = {}
  if (typeof input === 'string') {
    try {
      obj = JSON.parse(input)
    } catch {
      /* leave empty */
    }
  } else if (input && typeof input === 'object') {
    obj = input as { question?: string; options?: string[] }
  }
  return { question: obj.question ?? '', options: Array.isArray(obj.options) ? obj.options : [] }
}

// CheckMark is the small tick used by the completed marker and the done footer.
export function CheckMark({ size = 9, className }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={3.2} strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden>
      <path d="M5 12l4.5 4.5L19 7" />
    </svg>
  )
}

// Spinner is a small ring that rotates while a step is in flight (used by the
// progress pill and running tool rows).
function Spinner({ size = 14 }: { size?: number }) {
  return (
    <span
      className="spin-ring inline-block flex-none rounded-full border-2 border-[var(--color-primarysoft)] border-t-[var(--color-primary)]"
      style={{ width: size, height: size }}
    />
  )
}

// PlanPill is the top-center progress board: a compact pill showing the current
// step, files changed, and the running diff totals. Clicking it drops down the full
// task timeline (the same spine list). It stays visible whenever a plan exists.
export function PlanPill({
  plan,
  open,
  onToggle,
  filesChanged,
  adds,
  dels,
  running,
}: {
  plan: PlanSnapshot
  open: boolean
  onToggle: (open: boolean) => void
  filesChanged: number
  adds: number
  dels: number
  running: boolean
}) {
  const allDone = plan.total > 0 && plan.done >= plan.total
  const activeIndex = plan.items.findIndex((it) => it.status === 'in_progress')
  // "第 N / M 步": the running step if one is active, otherwise how many are done.
  const step = activeIndex >= 0 ? activeIndex + 1 : plan.done
  const frontier = activeIndex >= 0 ? activeIndex : plan.done
  const live = running || activeIndex >= 0
  return (
    <>
      <button
        onClick={() => onToggle(!open)}
        className={`relative z-10 inline-flex items-center gap-2.5 pl-3 pr-2.5 py-2 rounded-full bg-surface shadow-card text-[12.5px] cursor-pointer transition border ${open ? 'border-primary' : 'border-line2 hover:border-primary'}`}
      >
        {allDone ? (
          <CheckMark size={13} className="text-green flex-none" />
        ) : live ? (
          <Spinner size={13} />
        ) : (
          <span className="w-2 h-2 rounded-full bg-primary flex-none" />
        )}
        <span className="text-ink whitespace-nowrap">
          第 <b className="font-semibold tabular-nums">{step}</b> <span className="text-faint">/ {plan.total}</span> 步
        </span>
        {filesChanged > 0 && (
          <>
            <span className="w-px h-3.5 bg-line2 flex-none" />
            <span className="text-muted whitespace-nowrap">{filesChanged} 个文件已更改</span>
            {(adds > 0 || dels > 0) && (
              <DiffStat add={adds} del={dels} className="font-mono tabular-nums text-[12px] whitespace-nowrap" />
            )}
          </>
        )}
        <Icon name="chevron-down" size={14} className={`flex-none text-faint transition ${open ? 'rotate-180' : ''}`} />
      </button>
      {open && (
        <div className="absolute top-full left-1/2 -translate-x-1/2 mt-1.5 w-[380px] max-w-[calc(100vw-40px)] z-10">
          <div className="bg-surface border border-line2 rounded-[14px] shadow-card overflow-hidden anim-rise">
            <div className="px-4 py-3 max-h-[52vh] overflow-y-auto">
              {plan.items.map((it, i) => (
                <PlanRow key={i} item={it} last={i === plan.items.length - 1} filled={i < frontier} />
              ))}
            </div>
          </div>
        </div>
      )}
    </>
  )
}

// PlanRow is one step on the timeline: a status marker in the left gutter, a spine
// segment dropping from it to the next marker (primary when `filled`, faint
// otherwise), and the label. The active step prefers its present-continuous
// activeForm and reads in primary; completed steps dim; pending steps stay muted.
function PlanRow({ item, last, filled }: { item: PlanItem; last: boolean; filled: boolean }) {
  const inProgress = item.status === 'in_progress'
  const done = item.status === 'completed'
  const label = inProgress && item.activeForm?.trim() ? item.activeForm : item.content
  const textClass = done ? 'text-faint' : inProgress ? 'text-primaryink font-medium' : 'text-muted'
  return (
    <div className="flex gap-3">
      <div className="w-[13px] flex-none flex flex-col items-center">
        <PlanMark status={item.status} />
        {!last && <span className={`w-[2px] flex-1 min-h-[14px] my-1 rounded-full ${filled ? 'bg-primary' : 'bg-line2'}`} />}
      </div>
      <span className={`min-w-0 break-words text-[13px] leading-[1.5] ${last ? 'pb-1' : 'pb-4'} ${textClass}`}>{label}</span>
    </div>
  )
}

// PlanMark is the per-step glyph: a filled primary check (completed), a primary core
// dot inside a pulsing halo (in progress), or a hollow faint dot (pending).
function PlanMark({ status }: { status: string }) {
  if (status === 'completed') {
    return (
      <span className="w-[13px] h-[13px] flex-none rounded-full bg-primary inline-flex items-center justify-center text-white">
        <CheckMark size={8} />
      </span>
    )
  }
  if (status === 'in_progress') {
    return (
      <span className="relative w-[13px] h-[13px] flex-none inline-flex items-center justify-center">
        <span className="absolute w-[13px] h-[13px] rounded-full plan-pulse" />
        <span className="relative w-[9px] h-[9px] rounded-full bg-primary ring-2 ring-surface" />
      </span>
    )
  }
  return <span className="w-[13px] h-[13px] flex-none rounded-full border-[1.6px] border-line2 bg-surface" />
}

// BotRow wraps an assistant-side entry (message, tool card, notice). The assistant
// output is left-aligned and flows naturally, while user messages are right-aligned flat blocks.
export function BotRow({ children }: { children: ReactNode }) {
  return <div className="anim-rise min-w-0">{children}</div>
}

export function GhostBtn({ children, onClick, title, className }: { children: ReactNode; onClick?: () => void; title?: string; className?: string }) {
  return (
    <button
      className={`border-none bg-transparent text-muted text-[13px] px-2.5 py-1.5 rounded-lg cursor-pointer inline-flex items-center gap-1.5 hover:bg-surface2 hover:text-ink ${className ?? ''}`}
      onClick={onClick}
      title={title}
    >
      {children}
    </button>
  )
}

// ReplyArtifacts renders the regex-matched workspace files mentioned in an assistant
// reply as clickable cards. Memoized so it only recomputes when the reply text or the
// workspace file list changes — not on every streaming re-render.
export function ReplyArtifacts({ text, files, tabs, cwd, onOpen }: { text: string; files: string[]; tabs: PreviewTab[]; cwd: string; onOpen: (relPath: string) => void }) {
  const paths = useMemo(() => matchWorkspaceFiles(extractFilePaths(text), files), [text, files])
  if (paths.length === 0) return null
  return (
    <CollapsibleGroup icon="eye" label="可预览文件" count={paths.length}>
      {paths.map((p) => (
        <ArtifactCard key={p} relPath={p} add={0} del={0} onOpen={onOpen} autoOpened={tabs.some((t) => t.kind === 'file' && t.relPath === p)} />
      ))}
    </CollapsibleGroup>
  )
}


// AskCard renders an AskUser tool call as an interactive question: the user picks
// a suggested option or types a custom reply, which is sent as the next message.
export function AskCard({ tool, busy, onAnswer }: { tool: ToolEvent; busy: boolean; onAnswer: (text: string) => void }) {
  const { question, options } = askPayload((tool as ToolEvent & { input?: unknown }).input)
  const [custom, setCustom] = useState('')
  const [answered, setAnswered] = useState<string | null>(null)
  function answer(text: string) {
    const t = text.trim()
    if (!t || answered || busy) return
    setAnswered(t)
    onAnswer(t)
  }
  return (
    <div className="anim-rise">
      <div className="min-w-0 flex-1 bg-surface border border-primary rounded-[14px] shadow-xs p-4">
        <div className="flex items-center gap-2 mb-2 text-primaryink">
          <Icon name="chat" size={16} />
          <span className="text-[12.5px] font-semibold">需要你的确认</span>
        </div>
        <div className="text-[14px] text-ink whitespace-pre-wrap mb-3">{question || '（无问题内容）'}</div>
        {answered ? (
          <div className="text-[13px] text-muted">已回复：<span className="text-ink">{answered}</span></div>
        ) : (
          <>
            {options.length > 0 && (
              <div className="flex flex-col gap-1.5 mb-2.5">
                {options.map((opt, i) => (
                  <button
                    key={i}
                    onClick={() => answer(opt)}
                    disabled={busy}
                    className="text-left text-[13.5px] text-ink bg-surface2 border border-line2 rounded-[10px] px-3.5 py-2 cursor-pointer hover:border-primary hover:bg-primarysoft disabled:opacity-40"
                  >
                    {opt}
                  </button>
                ))}
              </div>
            )}
            <div className="flex gap-2">
              <input
                value={custom}
                onChange={(e) => setCustom(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') answer(custom)
                }}
                placeholder="或输入你的回答…"
                className="flex-1 text-[13.5px] bg-surface2 text-ink border border-line2 rounded-[10px] px-3 py-2 outline-none focus:border-primary"
              />
              <button
                onClick={() => answer(custom)}
                disabled={!custom.trim() || busy}
                className="text-[13px] font-semibold text-white bg-primary px-3.5 rounded-[10px] cursor-pointer hover:brightness-105 disabled:opacity-40 disabled:cursor-default"
              >
                发送
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// PlanChoiceCard appears after a plan-mode turn: the user picks how to carry out
// the plan (interactive or judge permission mode) or keeps refining it.
export function PlanChoiceCard({ busy, onExecute, onDismiss }: { busy: boolean; onExecute: (mode: string) => void; onDismiss: () => void }) {
  return (
    <div className="anim-rise">
      <div className="min-w-0 flex-1 bg-surface border border-primary rounded-[14px] shadow-xs p-4">
        <div className="flex items-center gap-2 mb-1 text-primaryink">
          <Icon name="compass" size={16} />
          <span className="text-[12.5px] font-semibold">方案已就绪，如何继续？</span>
        </div>
        <div className="text-[12.5px] text-muted mb-3">选择执行模式后将退出计划模式并开始执行；也可先不执行、继续补充想法。</div>
        <div className="flex flex-wrap gap-2">
          <button
            onClick={() => onExecute('interactive')}
            disabled={busy}
            className="text-[13px] font-semibold text-white bg-primary px-3.5 py-2 rounded-[10px] cursor-pointer hover:brightness-105 disabled:opacity-40 disabled:cursor-default"
          >
            交互模式执行
          </button>
          <button
            onClick={() => onExecute('judge')}
            disabled={busy}
            className="text-[13px] font-semibold text-primaryink bg-primarysoft border border-primary px-3.5 py-2 rounded-[10px] cursor-pointer hover:brightness-105 disabled:opacity-40 disabled:cursor-default"
          >
            智能模式执行
          </button>
          <button
            onClick={onDismiss}
            disabled={busy}
            className="text-[13px] text-muted bg-surface2 border border-line2 px-3.5 py-2 rounded-[10px] cursor-pointer hover:text-ink disabled:opacity-40 disabled:cursor-default"
          >
            先不执行 / 继续补充
          </button>
        </div>
      </div>
    </div>
  )
}

// ExecutionCard renders a run of tool calls as a compact, scannable list: one line
// per tool (type icon + verb/target + status), each click-to-expand for its detail.
// A running tool auto-expands so its streaming output stays visible.
// ExecutionCard is the agent's activity panel: a full-width card (no avatar) whose
// rows are the tool calls in one run. Each row shows the tool's icon chip, its verb +
// mono target, a diff badge for edits, and a live/done/failed status; clicking a row
// drills into its parameters and output.
export function ExecutionCard({ tools, harmAllows }: { tools: ToolEvent[]; harmAllows?: Record<string, string> }) {
  const [sel, setSel] = useState<number | null>(null)
  const runningIdx = tools.findIndex((t) => t.type !== 'completed' && t.type !== 'failed')
  const activeIdx = sel != null && sel < tools.length ? sel : runningIdx

  return (
    <div className="anim-rise">
      {tools.length > 1 && (
        <div className="flex items-center gap-1.5 px-1 pb-1.5 text-[12px] text-faint">
          <Icon name="terminal" size={13} className="flex-none" />
          <span className="font-medium">执行过程</span>
          <span className="font-mono">· {tools.length} 步</span>
          {runningIdx >= 0 && <span className="w-[6px] h-[6px] rounded-full bg-primary blip ml-0.5" />}
        </div>
      )}
      {tools.map((t, i) => {
        const st = t.type === 'failed' ? 'failed' : t.type === 'completed' ? 'done' : 'running'
        const active = activeIdx === i
        const { verb, target } = toolVerbTarget(t)
        const { add, del } = diffStats(t)
        const showDiff = add + del > 0
        const allowReason = harmAllows && t.toolUseID ? harmAllows[t.toolUseID] : undefined
        const iconColor = st === 'failed' ? 'text-red' : st === 'running' ? 'text-primary' : 'text-faint'
        const rowBg = active ? 'bg-surface2' : 'hover:bg-surface2'
        return (
          <div key={i}>
            <div
              onClick={() => setSel(sel === i ? null : i)}
              className={`flex items-center gap-2.5 px-2 py-1.5 rounded-lg cursor-pointer select-none ${rowBg}`}
            >
              <span className={`flex-none ${iconColor}`}><Icon name={toolIcon(t.toolName)} size={15} /></span>
              <span className="flex-1 min-w-0 truncate text-[13.5px] text-[#3f4653]">
                {verb}
                {target && <span className="font-mono text-faint"> {target}</span>}
              </span>
              {showDiff && <DiffStat add={add} del={del} className="font-mono text-[11.5px] tabular-nums flex-none" />}
              {allowReason && (
                <span title="智能模式已自动放行，展开查看原因" className="flex-none inline-flex items-center gap-1 text-[10.5px] text-primaryink bg-primarysoft rounded px-1.5 py-0.5">
                  <Icon name="shield" size={11} /> 智能放行
                </span>
              )}
              {st === 'failed' ? (
                <span className="text-[11px] text-red bg-redbg rounded-md px-1.5 py-0.5 flex-none">{failText(t)}</span>
              ) : st === 'running' ? (
                <Spinner size={14} />
              ) : (
                <span className="text-green flex-none"><CheckMark size={14} /></span>
              )}
              <Icon name="chevron-down" size={13} className={`flex-none text-faint transition ${active ? 'rotate-180' : ''}`} />
            </div>
            {active && (
              <div className="px-2 pb-3 pt-0.5">
                <ToolDetail tool={t} />
                {allowReason && (
                  <div className="mt-2 flex items-start gap-1.5 text-[12px] text-muted bg-primarysoft rounded-lg px-2.5 py-2">
                    <span className="flex-none mt-px text-primaryink"><Icon name="shield" size={13} /></span>
                    <span><b className="text-primaryink font-medium">智能放行</b>：{allowReason}</span>
                  </div>
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

// taskMeta extracts a Task call's display fields (sub-agent name, description) from
// its input; live events carry the parsed object, resumed sessions a JSON string.
function taskMeta(tool: ToolEvent, nested?: AgentNested): { sub: string; desc: string } {
  const raw = (tool as ToolEvent & { input?: unknown }).input
  const o: { subagent_type?: string; description?: string } =
    typeof raw === 'string' ? (() => { try { return JSON.parse(raw) } catch { return {} } })() : ((raw as object) ?? {})
  return { sub: o.subagent_type || nested?.agent || '子代理', desc: o.description || '' }
}

// AgentTaskCard renders a Task delegation as a live, observable nested view: the
// sub-agent's streamed text plus its own tool calls (each drillable). It is
// expanded while the sub-agent runs and collapses to a summary when it finishes.
function AgentTaskCard({ tool, nested }: { tool: ToolEvent; nested?: AgentNested }) {
  const running = tool.type !== 'completed' && tool.type !== 'failed'
  const failed = tool.type === 'failed'
  const [open, setOpen] = useState(false)
  const expanded = running || open
  const meta = taskMeta(tool, nested)
  const tools = nested?.tools ?? []
  return (
    <div className="anim-rise flex flex-col gap-2">
      <div className={`flex items-center gap-2 text-[12.5px] ${running ? '' : 'cursor-pointer'}`} onClick={() => !running && setOpen((o) => !o)}>
        <span className="inline-flex items-center text-primary flex-none"><Icon name="bot" size={14} /></span>
        <span className="font-medium text-ink">委派子代理 · {meta.sub}</span>
        <span className="inline-flex items-center gap-1.5 text-faint font-mono flex-none">
          <span className={`w-[6px] h-[6px] rounded-full ${running ? 'bg-primary blip' : failed ? 'bg-red' : 'bg-green'}`} />
          {running ? '运行中…' : failed ? '失败' : `${tools.length} 步`}
        </span>
        {nested?.usage && (nested.usage.inTok > 0 || nested.usage.outTok > 0) && (
          <span className="text-[11px] text-faint font-mono tabular-nums flex-none" title="子代理自身用量与运行时间(与主回复分开计)">
            ↑{fmtTokens(nested.usage.inTok)} ↓{fmtTokens(nested.usage.outTok)}{nested.usage.durMs ? ` · ${fmtDuration(nested.usage.durMs)}` : ''}
          </span>
        )}
        {!running && (
          <button className="ml-1 flex-none text-faint hover:text-ink inline-flex items-center gap-1 cursor-pointer" onClick={(e) => { e.stopPropagation(); setOpen((o) => !o) }}>
            {open ? '收起' : '展开'}
            <Icon name="chevron-down" size={13} className={open ? 'rotate-180 transition' : 'transition'} />
          </button>
        )}
      </div>
      {meta.desc && <div className="text-[12.5px] text-faint break-words">{meta.desc}</div>}
      {expanded && <AgentTaskDetail nested={nested} running={running} />}
    </div>
  )
}

// AgentTaskDetail renders a sub-agent's activity — its child tool calls (each
// drillable) and its streamed text. Shared by the single-Task card and the
// parallel taskgroup rows. The whole view lives in a bounded inner scroll pane:
// streamed text would otherwise keep growing the page and fight the user's own
// scrolling; overscroll-contain stops wheel events from chaining to the chat flow.
function AgentTaskDetail({ nested, running }: { nested?: AgentNested; running: boolean }) {
  const [selTool, setSelTool] = useState<number | null>(null)
  const { ref, onScroll } = useStickToBottom<HTMLDivElement>(nested)
  const tools = nested?.tools ?? []
  // Auto-expand the running child tool (like the top-level exec card) so its
  // streaming output shows live; an explicit click takes over.
  const runningChild = tools.findIndex((ct) => ct.type !== 'completed' && ct.type !== 'failed')
  const activeChild = selTool != null && selTool < tools.length ? selTool : runningChild
  return (
    <div ref={ref} onScroll={onScroll} className="max-h-[340px] overflow-y-auto overscroll-contain flex flex-col gap-2">
      {tools.length > 0 && (
        <div className="flex flex-col gap-0.5">
          {tools.map((ct, i) => {
            const st = ct.type === 'failed' ? 'failed' : ct.type === 'completed' ? 'done' : 'running'
            const active = activeChild === i
            const iconColor = st === 'failed' ? 'text-red' : st === 'running' ? 'text-primary' : 'text-faint'
            return (
              <div key={i}>
                <div onClick={() => setSelTool(active ? null : i)} className={`flex items-center gap-2 text-[13px] px-2 py-1.5 rounded-lg cursor-pointer select-none ${active ? 'bg-surface2' : 'hover:bg-surface2'}`}>
                  <span className={`flex-none ${iconColor}`}><Icon name={toolIcon(ct.toolName)} size={14} /></span>
                  <span className="flex-1 min-w-0 truncate text-[#3f4653]">{toolLabel(ct)}</span>
                  {st === 'running' ? <Spinner size={13} /> : st === 'failed' ? <span className="text-red text-[12px] flex-none">✕</span> : <span className="text-green flex-none"><CheckMark size={13} /></span>}
                  <Icon name="chevron-down" size={12} className={`flex-none text-faint transition ${active ? 'rotate-180' : ''}`} />
                </div>
                {active && <div className="px-2 pb-2 pt-0.5"><ToolDetail tool={ct} /></div>}
              </div>
            )
          })}
        </div>
      )}
      {nested?.text ? (
        <div className="text-[13.5px] text-[#3f4653] leading-[1.7] break-words">
          <Markdown>{nested.text}</Markdown>
          {running && <span className="caret">▍</span>}
        </div>
      ) : running ? (
        <div className="text-faint text-[12.5px]">子代理思考中…</div>
      ) : null}
    </div>
  )
}

// taskActivity summarizes what a running sub-agent is doing right now — its active
// child tool, else the last line it streamed — for the compact taskgroup row.
function taskActivity(nested?: AgentNested): string {
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

// AgentTaskGroup renders a parallel fan-out of Task delegations as one container:
// a header with aggregate progress and one compact row per sub-agent, expandable on
// click. Rows default to collapsed — the opposite of the single-Task card — because
// several force-expanded streaming panes at once are unreadable.
export function AgentTaskGroup({ tasks }: { tasks: ToolBlock[] }) {
  const [open, setOpen] = useState(false)
  const running = tasks.some((t) => t.tool.type !== 'completed' && t.tool.type !== 'failed')
  const failed = tasks.filter((t) => t.tool.type === 'failed').length
  const finished = tasks.length - tasks.filter((t) => t.tool.type !== 'completed' && t.tool.type !== 'failed').length
  const expanded = running || open
  // Aggregate usage: tokens sum across sub-agents; duration is the longest child,
  // since they ran in parallel.
  const usages = tasks.map((t) => t.nested?.usage).filter((u): u is NonNullable<AgentNested['usage']> => !!u)
  const inTok = usages.reduce((s, u) => s + u.inTok, 0)
  const outTok = usages.reduce((s, u) => s + u.outTok, 0)
  const durMs = usages.reduce((m, u) => Math.max(m, u.durMs ?? 0), 0)
  return (
    <div className="anim-rise flex flex-col gap-2">
      <div className={`flex items-center gap-2 text-[12.5px] ${running ? '' : 'cursor-pointer'}`} onClick={() => !running && setOpen((o) => !o)}>
        <span className="inline-flex items-center text-primary flex-none"><Icon name="bot" size={14} /></span>
        <span className="font-medium text-ink">并行子代理 · {tasks.length} 个任务</span>
        <span className="inline-flex items-center gap-1.5 text-faint font-mono flex-none">
          <span className={`w-[6px] h-[6px] rounded-full ${running ? 'bg-primary blip' : failed > 0 ? 'bg-red' : 'bg-green'}`} />
          {running ? `${finished}/${tasks.length} 完成` : failed > 0 ? `${finished - failed} 成功 · ${failed} 失败` : '全部完成'}
        </span>
        {!running && (inTok > 0 || outTok > 0) && (
          <span className="text-[11px] text-faint font-mono tabular-nums flex-none" title="各子代理用量合计;耗时取最长者(并行运行)">
            ↑{fmtTokens(inTok)} ↓{fmtTokens(outTok)}{durMs > 0 ? ` · ${fmtDuration(durMs)}` : ''}
          </span>
        )}
        {!running && (
          <button className="ml-1 flex-none text-faint hover:text-ink inline-flex items-center gap-1 cursor-pointer" onClick={(e) => { e.stopPropagation(); setOpen((o) => !o) }}>
            {open ? '收起' : '展开'}
            <Icon name="chevron-down" size={13} className={open ? 'rotate-180 transition' : 'transition'} />
          </button>
        )}
      </div>
      {expanded && (
        <div className="flex flex-col gap-0.5">
          {tasks.map((t) => <AgentTaskRow key={t.id} block={t} />)}
        </div>
      )}
    </div>
  )
}

// AgentTaskRow is one sub-agent inside an AgentTaskGroup: a compact status line
// (who, what, current activity or final stats) that expands on click to the full
// nested detail view. A resumed Task carries no live nested data, so expanding it
// falls back to the plain tool detail (input + persisted result).
function AgentTaskRow({ block }: { block: ToolBlock }) {
  const [open, setOpen] = useState(false)
  const t = block.tool
  const running = t.type !== 'completed' && t.type !== 'failed'
  const failed = t.type === 'failed'
  const meta = taskMeta(t, block.nested)
  const activity = running ? taskActivity(block.nested) : ''
  const usage = block.nested?.usage
  const steps = block.nested?.tools.length ?? 0
  return (
    <div>
      <div onClick={() => setOpen((o) => !o)} className={`flex items-center gap-2 text-[13px] px-2 py-1.5 rounded-lg cursor-pointer select-none ${open ? 'bg-surface2' : 'hover:bg-surface2'}`}>
        {running ? <span className="flex-none"><Spinner size={13} /></span> : failed ? <span className="text-red text-[12px] flex-none">✕</span> : <span className="text-green flex-none"><CheckMark size={13} /></span>}
        <span className="flex-none font-medium text-ink">{meta.sub}</span>
        <span className="flex-1 min-w-0 truncate text-[#3f4653]">{meta.desc}</span>
        {running && activity && (
          <span className="flex-none max-w-[40%] truncate text-faint text-[12px]">{activity}</span>
        )}
        {!running && (
          <span className="flex-none text-[11px] text-faint font-mono tabular-nums">
            {steps > 0 ? `${steps} 步` : ''}
            {usage && (usage.inTok > 0 || usage.outTok > 0) ? ` · ↑${fmtTokens(usage.inTok)} ↓${fmtTokens(usage.outTok)}` : ''}
            {usage?.durMs ? ` · ${fmtDuration(usage.durMs)}` : ''}
          </span>
        )}
        <Icon name="chevron-down" size={12} className={`flex-none text-faint transition ${open ? 'rotate-180' : ''}`} />
      </div>
      {open && (
        <div className="px-2 pb-2 pt-0.5">
          {block.nested ? <AgentTaskDetail nested={block.nested} running={running} /> : <ToolDetail tool={t} />}
        </div>
      )}
    </div>
  )
}

// formatInput renders a tool call's raw arguments as pretty JSON. Live events
// carry the parsed value; resumed sessions carry a JSON string — both are handled.
function formatInput(input: unknown): string {
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

// useStickToBottom keeps a scroll container pinned to its newest content while it
// streams, unless the user has scrolled up to read — then it leaves them be. dep
// changes (growing text/output) trigger the re-pin.
function useStickToBottom<T extends HTMLElement = HTMLDivElement>(dep: unknown) {
  const ref = useRef<T>(null)
  const stick = useRef(true)
  useEffect(() => {
    const el = ref.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [dep])
  const onScroll = () => {
    const el = ref.current
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48
  }
  return { ref, onScroll }
}

// kvRows renders a compact key→value table for a tool's structured input.
function kvRows(rows: ([string, unknown] | false | null | undefined)[]) {
  const pairs = rows.filter(Boolean) as [string, unknown][]
  return (
    <div className="flex flex-col gap-1.5 bg-surface2 border border-line rounded-[8px] py-2 px-2.5">
      {pairs.map(([k, v], i) => (
        <div key={i} className="flex gap-2.5 text-[12.5px] min-w-0">
          <span className="text-faint flex-none w-[48px]">{k}</span>
          <span className="font-mono text-ink break-all min-w-0">{String(v ?? '')}</span>
        </div>
      ))}
    </div>
  )
}

// RawJson shows a tool's raw arguments as pretty JSON, collapsed by default when
// long so a big payload does not flood the card.
function RawJson({ value }: { value: unknown }) {
  const text = formatInput(value)
  const [open, setOpen] = useState(text.length <= 400)
  if (!text || text === '{}') return <div className="text-faint text-[12.5px] bg-surface2 border border-line rounded-[8px] py-2 px-2.5">（无入参）</div>
  return (
    <div>
      {text.length > 400 && (
        <button onClick={() => setOpen((v) => !v)} className="text-[12px] text-muted hover:text-ink inline-flex items-center gap-1 mb-1.5">
          <Icon name="chevron-down" size={13} className={open ? 'rotate-180 transition' : 'transition'} />
          {open ? '收起原始入参' : `展开原始入参 · ${text.length} 字符`}
        </button>
      )}
      {open && (
        <pre className="m-0 font-mono text-[12px] leading-[1.5] bg-surface2 border border-line rounded-[8px] py-2 px-2.5 max-h-[200px] overflow-auto whitespace-pre-wrap break-all">
          {text.length > 16000 ? text.slice(0, 16000) + '\n… 已截断' : text}
        </pre>
      )}
    </div>
  )
}

// ToolInputView renders a tool's input in a clean, tool-specific shape — a command
// block, a key/value table, etc. — falling back to collapsible raw JSON for tools
// without a tailored view.
function ToolInputView({ tool }: { tool: ToolEvent }) {
  const o = toolInputObj(tool)
  const s = (k: string) => (o[k] != null ? String(o[k]) : '')
  switch (tool.toolName) {
    case 'Bash':
      if (s('command')) return (
        <div>
          <pre className="m-0 font-mono text-[12.5px] leading-[1.5] bg-surface2 border border-line rounded-[8px] py-2 px-2.5 max-h-[160px] overflow-auto whitespace-pre-wrap break-all text-ink">{s('command')}</pre>
          {(o.timeout || o.run_in_background) ? <div className="text-[11.5px] text-faint mt-1">{o.timeout ? `超时 ${s('timeout')}ms` : ''}{o.run_in_background ? (o.timeout ? ' · ' : '') + '后台运行' : ''}</div> : null}
        </div>
      )
      break
    case 'Read':
      if (s('path')) return kvRows([['路径', s('path')], !!(o.offset || o.limit) && ['范围', `第 ${o.offset ?? 0} 行起${o.limit ? `，≤ ${s('limit')} 行` : ''}`], !!o.pages && ['页', s('pages')]])
      break
    case 'Grep':
      if (s('pattern')) return kvRows([['模式', s('pattern')], !!o.path && ['路径', s('path')], !!o.glob && ['glob', s('glob')], !!o.type && ['类型', s('type')], !!o.output_mode && ['输出', s('output_mode')]])
      break
    case 'Glob':
      if (s('pattern')) return kvRows([['模式', s('pattern')], !!o.path && ['路径', s('path')]])
      break
    case 'Write':
    case 'Edit':
    case 'Delete':
      if (s('path')) return kvRows([['路径', s('path')], tool.toolName === 'Delete' && !!o.permanent && ['方式', '永久删除']])
      break
    case 'WebFetch':
      if (s('url')) return kvRows([['URL', s('url')]])
      break
  }
  return <RawJson value={o} />
}

// MatchedFileTree renders a Glob/Grep matched-file list as a collapsible
// directory tree instead of one full path per line: each directory appears once
// (a single-child directory chain collapses into one "a/b/c/" row) with a
// chevron + descendant-file count, and clicking it folds that subtree away —
// a big same-directory result set neither repeats its prefix nor swamps the
// card. Everything starts expanded; the fold state lives with the card.
function MatchedFileTree({ paths }: { paths: string[] }) {
  const tree = useMemo(() => buildFileTree(paths), [paths])
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const toggle = (p: string) =>
    setCollapsed((s) => {
      const next = new Set(s)
      if (next.has(p)) next.delete(p)
      else next.add(p)
      return next
    })
  const countFiles = (n: FileNode): number =>
    n.dir ? (n.children ?? []).reduce((sum, c) => sum + countFiles(c), 0) : 1
  const render = (nodes: FileNode[], depth: number): ReactNode[] =>
    nodes.flatMap((n) => {
      const pad = { paddingLeft: depth * 14 }
      if (!n.dir) {
        return [
          <div key={'f:' + n.path} className="flex items-center gap-1.5 min-w-0" style={pad} title={n.path}>
            <span className="flex-none" style={{ color: fileColor(n.path) }}><Icon name={kindIcon(classifyPreview(n.path).kind)} size={13} /></span>
            <span className="font-mono text-[12px] text-ink truncate">{n.name}</span>
          </div>,
        ]
      }
      let label = n.name
      let cur = n
      while ((cur.children?.length ?? 0) === 1 && cur.children![0].dir) {
        cur = cur.children![0]
        label += '/' + cur.name
      }
      const open = !collapsed.has(n.path)
      return [
        <div
          key={'d:' + n.path}
          onClick={() => toggle(n.path)}
          className="flex items-center gap-1.5 cursor-pointer select-none"
          style={pad}
          title={cur.path}
        >
          <Icon name="chevron-down" size={11} className={`flex-none text-faint transition ${open ? '' : '-rotate-90'}`} />
          <Icon name="folder" size={13} className="flex-none text-faint" />
          <span className="font-mono text-[12px] text-ink truncate">{label}/</span>
          <span className="font-mono text-[11px] text-faint flex-none">· {countFiles(cur)}</span>
        </div>,
        ...(open ? render(cur.children ?? [], depth + 1) : []),
      ]
    })
  return <div className="space-y-1">{render(tree, 0)}</div>
}

// ToolDetail shows one tool call's input arguments and its return content
// (matched files, command/diff output, or a result message).
function ToolDetail({ tool }: { tool: ToolEvent }) {
  const matched = (tool.files ?? []).filter((f) => f.kind === 'matched')
  const out = tool.output ?? []
  const outScroll = useStickToBottom<HTMLPreElement>(out.length)
  const img = tool.image
  const imgSrc = img ? (img.url || (img.data ? `data:${img.media_type || 'image/png'};base64,${img.data}` : '')) : ''
  return (
    <div className="min-w-0 flex flex-col gap-2.5">
      <div>
        <div className="text-[11px] text-faint mb-1 tracking-wide">参数</div>
        <ToolInputView tool={tool} />
      </div>

      <div>
        <div className="text-[11px] text-faint mb-1 tracking-wide">
          输出{imgSrc ? ' · 图片' : matched.length > 0 ? ` · ${matched.length}${tool.filesTotal && tool.filesTotal > matched.length ? `/${tool.filesTotal}` : ''} 个匹配` : out.length > 0 ? ` · ${out.length} 行` : ''}
        </div>
        {imgSrc ? (
        <div className="bg-surface2 border border-line rounded-[8px] p-2 inline-block max-w-full">
          <img src={imgSrc} alt="" className="max-h-[340px] max-w-full rounded-[5px] block" />
        </div>
      ) : matched.length > 0 ? (
        <div className="bg-surface2 border border-line rounded-[8px] py-2 px-2.5 max-h-[360px] overflow-auto">
          <MatchedFileTree paths={matched.slice(0, 400).map((f) => f.path)} />
        </div>
      ) : out.length > 0 ? (
        <pre ref={outScroll.ref} onScroll={outScroll.onScroll} className="m-0 font-mono text-[12px] leading-[1.55] bg-surface2 border border-line rounded-[8px] py-2 max-h-[360px] overflow-auto">
          {out.slice(0, 400).map((l, i) => (
            <div key={i} className={(l.stream || '').startsWith('diff') ? `cl ${l.stream}` : lineClass(l.stream)}>{l.text}</div>
          ))}
          {tool.outputTruncated && <div className="px-2.5 text-faint">… 输出已截断</div>}
        </pre>
      ) : (
        <div className="text-faint text-[12.5px] bg-surface2 border border-line rounded-[8px] py-2 px-2.5">（无返回内容）</div>
      )}
      </div>
    </div>
  )
}

// ToolPreview renders the tool cards with mock data so the styling can be reviewed
// without a live session (mounted via ?preview=tools). Not used in normal flow.
export function ToolPreview() {
  const bash: ToolEvent = {
    type: 'completed', toolName: 'Bash', toolUseID: 'b1',
    input: { command: 'go test ./internal/repl/ -run TestStream -count=1', timeout: 120000 },
    output: [
      { stream: 'stdout', text: 'ok  \tgithub.com/wt68/runcode/internal/repl\t2.81s' },
      { stream: 'info', text: '退出码 0 · 2.83s' },
    ],
  }
  const edit: ToolEvent = {
    type: 'completed', toolName: 'Edit', toolUseID: 'e1',
    input: { path: 'internal/repl/session.go' },
    files: [{ path: 'internal/repl/session.go', kind: 'read' }],
    output: [
      { stream: 'diff_context', text: '  func (s *Session) buildRequest() {' },
      { stream: 'diff_context', text: '    promptOpts.Tools = tools' },
      { stream: 'diff_del', text: '-   promptOpts.Skills = s.skills' },
      { stream: 'diff_add', text: '+   promptOpts.Skills = s.currentSkillsCatalog()' },
      { stream: 'diff_add', text: '+   promptOpts.Agents = s.currentAgentsCatalog()' },
      { stream: 'diff_context', text: '    system, _ := prompt.Build(promptOpts)' },
    ],
  }
  const grep: ToolEvent = {
    type: 'completed', toolName: 'Grep', toolUseID: 'g1',
    input: { pattern: 'StreamDelta', path: 'internal', glob: '*.go', output_mode: 'files_with_matches' },
    // 50 entries on purpose: enough to overflow the list's max-height (a flex-squash
    // bug once hid exactly this), mixing root files + two directories so the
    // MatchedFileTree rendering (collapsed dir chains, indentation) is exercised too.
    files: [
      { path: 'index.html', kind: 'matched' as const },
      { path: 'README.md', kind: 'matched' as const },
      { path: 'projects/matrix_transformations/analysis/image_analysis.csv', kind: 'matched' as const },
      { path: 'projects/matrix_transformations/analysis/slides_outline.md', kind: 'matched' as const },
      { path: 'projects/matrix_transformations/analysis/notes.txt', kind: 'matched' as const },
      ...Array.from({ length: 45 }, (_, i) => ({
        path: `projects/matrix_transformations/svg_output/${String(i + 1).padStart(2, '0')}_矩阵变换_较长文件名示例.svg`,
        kind: 'matched' as const,
      })),
    ],
    filesTotal: 76,
  }
  const running: ToolEvent = {
    type: 'progress', toolName: 'Bash', toolUseID: 'r1',
    input: { command: 'npm run build' },
    output: [
      { stream: 'stdout', text: 'vite v6.4.3 building for production...' },
      { stream: 'stdout', text: '✓ 812 modules transformed' },
    ],
  }
  const readImg: ToolEvent = {
    type: 'completed', toolName: 'Read', toolUseID: 'r2',
    input: { path: 'docs/design.png' }, files: [{ path: 'docs/design.png', kind: 'read' }],
    output: [{ stream: 'stdout', text: '[image: design.png]' }],
    image: { media_type: 'image/svg+xml', url: 'data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIzNjAiIGhlaWdodD0iMjAwIj48cmVjdCB3aWR0aD0iMzYwIiBoZWlnaHQ9IjIwMCIgZmlsbD0iIzBGNzY2RSIvPjxyZWN0IHg9IjI0IiB5PSIyNiIgd2lkdGg9IjExMCIgaGVpZ2h0PSIxMiIgcng9IjQiIGZpbGw9IiNGNTlFMEIiLz48dGV4dCB4PSIyNCIgeT0iMTE1IiBmb250LWZhbWlseT0ic2Fucy1zZXJpZiIgZm9udC1zaXplPSIyNCIgZmlsbD0iI2ZmZmZmZiI+ZGVzaWduLnBuZzwvdGV4dD48dGV4dCB4PSIyNCIgeT0iMTUwIiBmb250LWZhbWlseT0ic2Fucy1zZXJpZiIgZm9udC1zaXplPSIxNSIgZmlsbD0iIzk5RjZFNCI+dGh1bWJuYWlsIHByZXZpZXc8L3RleHQ+PC9zdmc+' },
  }
  return (
    <div className="min-h-screen p-8">
      <div className="max-w-[940px] mx-auto flex flex-col gap-6">
        <h2 className="text-[18px] font-bold tracking-tight">工具卡样式预览</h2>
        <div className="text-[13px] text-muted">折叠态紧凑列表(每行:图标 + 动词 + 目标 + 状态),点行展开详情;运行中的自动展开:</div>
        <ExecutionCard tools={[bash, edit, grep, readImg]} />
        <ExecutionCard tools={[running]} />
        <div className="text-[13px] text-muted">图片 Read 展开(缩略图):</div>
        <div className="bg-surface border border-line2 rounded-[14px] p-4"><ToolDetail tool={readImg} /></div>
        <div className="text-[13px] text-muted">查找/搜索展开(匹配文件列表):</div>
        <div className="bg-surface border border-line2 rounded-[14px] p-4"><ToolDetail tool={grep} /></div>
      </div>
    </div>
  )
}

// ThinkingPreview renders assistant blocks carrying reasoning ("thinking") in each
// state, so the thinking panel's rendering can be verified without a live model
// (mounted via ?preview=thinking). Not used in normal flow.
export function ThinkingPreview() {
  const reason =
    '用户问 9.11 和 9.9 哪个大。先把它们对齐小数位:9.11 = 9.110,9.9 = 9.900。' +
    '比较小数部分 0.110 与 0.900,显然 0.900 更大。所以 9.9 > 9.11。'
  const blocks: Block[] = [
    { kind: 'assistant', id: 't1', text: '', thinking: reason, streaming: true, ts: '' },
    { kind: 'assistant', id: 't2', text: '**9.9 更大**。对齐小数位后 9.900 > 9.110。', thinking: reason, streaming: true, ts: '' },
    { kind: 'assistant', id: 't3', text: '**9.9 更大**。对齐小数位后 9.900 > 9.110。', thinking: reason, streaming: false, ts: '11:42' },
  ]
  const labels = ['① 仅思考中(自动展开)', '② 思考完 + 答案流式(自动折叠)', '③ 完成态(可点开)']
  const noop = () => {}
  return (
    <div className="min-h-screen p-8">
      <div className="w-full max-w-[1280px] mx-auto flex flex-col gap-6">
        <h2 className="text-[18px] font-bold tracking-tight">上下文用量条(不同占用)</h2>
        <div className="flex flex-col gap-3 bg-surface border border-line2 rounded-[12px] p-4">
          {[10000, 96000, 120000, 130000].map((u) => (
            <div key={u} className="flex items-center gap-4 text-[12.5px] text-muted">
              <ContextMeter used={u} budget={128000} onCompact={noop} compacting={false} busy={false} />
            </div>
          ))}
          <div className="flex items-center gap-4 text-[12.5px] text-muted">
            <ContextMeter used={44000} budget={0} onCompact={noop} compacting={false} busy={false} />
          </div>
          <div className="flex items-center gap-4 text-[12.5px] text-muted">
            <ContextMeter used={18000} budget={200000} estimated onCompact={noop} compacting={false} busy={false} />
          </div>
        </div>
        <h2 className="text-[18px] font-bold tracking-tight">结构化思考卡片</h2>
        <AnalyzeCard
          tool={{
            type: 'completed',
            toolName: 'Analyze',
            toolUseID: 'a1',
            input: {
              method: '5 Whys + 假设验证 + 奥卡姆剃刀',
              steps: [
                { key: 'symptom', label: '现象与范围', content: '登录后偶发 401,约 5% 请求,集中在移动端弱网环境。' },
                { key: 'hypotheses', label: '可能假设', content: '1) token 刷新竞态;2) 客户端时钟偏移导致过期误判;3) 网关缓存了旧 token。' },
                { key: 'validation', label: '验证方式', content: '抓包对比 401 前后的 token;核对服务端/客户端时间;灰度关闭网关缓存。' },
                { key: 'root_cause', label: '最可能根因', content: '并发刷新时旧 token 覆盖了新 token(竞态)。' },
                { key: 'fix', label: '修复方案', content: '刷新加互斥锁 + 单飞(single-flight);失败仅重试一次。' },
                { key: 'verification', label: '验证方法', content: '压测并发刷新场景;监控 401 率回落到 0 且无回归。' },
              ],
            },
          }}
        />
        <div className="text-[13px] text-muted">流式中(部分步骤已填、后续为空 + 分析中…):</div>
        <AnalyzeCard
          tool={{
            type: 'progress',
            toolName: 'Analyze',
            toolUseID: 'a2',
            input: {
              method: '5 Whys + 假设验证 + 奥卡姆剃刀',
              steps: [
                { key: 'symptom', label: '现象与范围', content: '登录后偶发 401,约 5% 请求,集中在移动端弱网环境。' },
                { key: 'hypotheses', label: '可能假设', content: '1) token 刷新竞态;2) 客户端时钟偏移导致过期误判;3) 网关缓存了旧 to' },
                { key: 'validation', label: '验证方式', content: '' },
                { key: 'root_cause', label: '最可能根因', content: '' },
                { key: 'fix', label: '修复方案', content: '' },
                { key: 'verification', label: '验证方法', content: '' },
              ],
            },
          }}
        />
        <h2 className="text-[18px] font-bold tracking-tight">思考面板样式预览</h2>
        {blocks.map((b, i) => (
          <div key={b.id} className="flex flex-col gap-2">
            <div className="text-[13px] text-muted">{labels[i]}</div>
            <BlockView block={b} />
          </div>
        ))}
      </div>
    </div>
  )
}

// ThinkingPanel shows the model's streamed reasoning ("chain of thought") in a
// collapsible panel above the answer. It auto-expands while the model is actively
// thinking (before any answer text) and auto-collapses once the answer begins; a
// manual toggle takes over from that point.
function ThinkingPanel({ text, streaming }: { text: string; streaming: boolean }) {
  const [open, setOpen] = useState(streaming)
  const userToggled = useRef(false)
  useEffect(() => {
    if (!userToggled.current) setOpen(streaming)
  }, [streaming])
  const scroll = useStickToBottom(open && streaming ? text.length : null)
  // Compact, dimmed disclosure (Claude-style): a single quiet line that opens into
  // the reasoning as indented, muted text with a left rule — not a boxed panel.
  return (
    <div>
      <button
        onClick={() => {
          userToggled.current = true
          setOpen((v) => !v)
        }}
        className="inline-flex items-center gap-1.5 text-[12.5px] text-faint hover:text-muted transition cursor-pointer select-none"
      >
        <span className={`flex-none${streaming ? ' animate-pulse' : ''}`}>
          <Icon name="sparkles" size={13} />
        </span>
        <span>{streaming ? '正在思考…' : '思考过程'}</span>
        <Icon name="chevron-down" size={12} className={`flex-none transition-transform${open ? ' rotate-180' : ''}`} />
      </button>
      {open && (
        <div
          ref={streaming ? scroll.ref : undefined}
          onScroll={streaming ? scroll.onScroll : undefined}
          className={`mt-1.5 text-[13px] leading-[1.7] text-faint whitespace-pre-wrap break-words${streaming ? ' max-h-[34vh] overflow-y-auto' : ''}`}
        >
          {text}
          {streaming && <span className="caret">▍</span>}
        </div>
      )}
    </div>
  )
}

// AnalyzeCard visualizes the model's structured-thinking protocol (the Analyze
// tool): the method as a badge and each protocol step as a numbered timeline entry
// with its label and the model's task-specific content. Collapsible; shows a live
// "分析中…" hint until the analysis completes.
export function AnalyzeCard({ tool }: { tool: ToolEvent }) {
  const { method, steps } = analyzeSteps(tool.input)
  const running = tool.type === 'started' || tool.type === 'progress'
  const [open, setOpen] = useState(true)
  return (
    <div className="anim-rise">
      <div className="flex-1 min-w-0 bg-surface border border-line2 rounded-[14px] shadow-xs overflow-hidden">
        <button
          onClick={() => setOpen((v) => !v)}
          className="w-full flex items-center gap-2 px-4 py-2.5 text-left hover:bg-surface2 transition cursor-pointer select-none"
        >
          <span className={`text-primary flex-none${running ? ' animate-pulse' : ''}`}><Icon name="sparkles" size={16} /></span>
          <span className="font-semibold text-[13.5px] text-ink flex-none">结构化思考</span>
          {method && (
            <span className="text-[11.5px] text-primaryink bg-primarysoft rounded-full px-2 py-0.5 truncate">{method}</span>
          )}
          {running && <span className="text-[12px] text-faint flex-none">分析中…</span>}
          <span className={`ml-auto flex-none text-faint transition-transform${open ? ' rotate-180' : ''}`}><Icon name="chevron-down" size={14} /></span>
        </button>
        {open &&
          (steps.length > 0 ? (
            <ol className="px-4 pb-3.5 pt-0.5 m-0 list-none">
              {steps.map((s, i) => (
                <li key={s.key || i} className="relative pl-8 pb-3.5 last:pb-0">
                  {i < steps.length - 1 && <span className="absolute left-[11px] top-[23px] bottom-0 w-px bg-line2" />}
                  <span className="absolute left-0 top-0 w-[23px] h-[23px] rounded-full bg-primarysoft text-primaryink text-[11.5px] font-bold inline-flex items-center justify-center">{i + 1}</span>
                  <div className="text-[12.5px] font-semibold text-ink pt-[3px]">{s.label || s.key}</div>
                  <div className="text-[13px] text-muted leading-[1.65] whitespace-pre-wrap break-words mt-1">{s.content || '—'}</div>
                </li>
              ))}
            </ol>
          ) : (
            <div className="px-4 pb-3 pt-1 text-[12.5px] text-faint">（无分析内容）</div>
          ))}
      </div>
    </div>
  )
}

// ContextMeter shows how full the model's context window is — the last turn's
// input tokens against the compaction budget — with a bar that turns amber as it
// nears the 80% auto-compaction threshold, plus a manual "压缩" button. With no
// budget set (auto-compaction off) it just shows the raw occupancy.
export function ContextMeter({
  used,
  budget,
  estimated,
  onCompact,
  compacting,
  busy,
}: {
  used: number
  budget: number
  estimated?: boolean
  onCompact: () => void
  compacting: boolean
  busy: boolean
}) {
  const pct = budget > 0 ? Math.min(100, Math.round((used / budget) * 100)) : 0
  const near = budget > 0 && pct >= 80
  const bar = pct >= 100 ? 'bg-red' : near ? 'bg-[#e0954a]' : 'bg-primary'
  // A leading "≈" marks a resume-time estimate, until the first turn reports the
  // provider's exact count.
  const approx = estimated ? '≈' : ''
  return (
    <div className="inline-flex items-center gap-2" style={NO_DRAG}>
      <span
        className="inline-flex items-center gap-1.5"
        title={
          (estimated ? '（估算值，发送一条消息后即为精确值）\n' : '') +
          (budget > 0
            ? `上下文占用 ${used.toLocaleString()} / ${budget.toLocaleString()} tokens · 达 80% 自动总结压缩`
            : `上下文占用 ${used.toLocaleString()} tokens · 未设预算，自动压缩关闭`)
        }
      >
        <span>上下文</span>
        {budget > 0 ? (
          <>
            <span className="w-[62px] h-[6px] rounded-full bg-surface2 border border-line2 overflow-hidden inline-block align-middle">
              <span className={`block h-full ${bar} transition-[width]`} style={{ width: pct + '%' }} />
            </span>
            <b className={`font-semibold tabular-nums ${near ? 'text-[#b26a1f]' : 'text-ink'}`}>{approx}{pct}%</b>
            <span className="text-faint tabular-nums">{approx}{fmtTokens(used)}/{fmtTokens(budget)}</span>
          </>
        ) : (
          <span className="text-ink font-semibold tabular-nums">{approx}{fmtTokens(used)} <span className="text-faint font-normal">· 未限</span></span>
        )}
      </span>
      <button
        onClick={onCompact}
        disabled={busy || compacting}
        title="压缩上下文：把较早的对话总结成摘要、保留最近几轮原文（磁盘记录保持完整）"
        className="inline-flex items-center gap-1 px-2 py-1 rounded-lg border border-line2 text-[12px] text-muted hover:text-ink hover:bg-surface2 transition cursor-pointer disabled:opacity-40 disabled:cursor-default"
      >
        <Icon name="compress" size={13} /> {compacting ? '压缩中…' : '压缩'}
      </button>
    </div>
  )
}

export function BlockView({ block, onOpenFile, resolveFile }: { block: Block; onOpenFile?: (relPath: string) => void; resolveFile?: (token: string) => string | null }) {
  // While an assistant answer streams, keep it in a fixed-height window pinned to
  // the newest text (consistent with the tool/agent cards); release to full height
  // when done so the finished answer reads normally in the page flow.
  const aScroll = useStickToBottom(block.kind === 'assistant' && block.streaming ? block.text.length : null)
  switch (block.kind) {
    case 'user':
      return (
        <div className="flex justify-end anim-rise">
          <div className="min-w-0 max-w-[82%] rounded-[13px] px-3.5 py-2 text-[13.5px] text-ink leading-[1.55]" style={{ background: '#F4F3FF' }}>
            {block.attachments && block.attachments.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mb-1.5">
                {block.attachments.map((name, i) => (
                  <span key={name + i} className="inline-flex items-center gap-1 bg-surface border border-line2 rounded-[7px] px-2 py-0.5 text-[11.5px] text-muted max-w-[220px]">
                    <Icon name="file" size={12} /> <span className="truncate">{name}</span>
                  </span>
                ))}
              </div>
            )}
            {block.text && <div className="whitespace-pre-wrap break-words">{block.text}</div>}
          </div>
        </div>
      )
    case 'assistant': {
      const hasThinking = (block.thinking ?? '').trim() !== ''
      const hasText = block.text.trim() !== ''
      // The model is still thinking and hasn't begun the answer — the thinking panel
      // carries the live caret, so the (empty) answer area is suppressed until text
      // starts, avoiding a stray second caret.
      const thinkingActive = block.streaming && hasThinking && !hasText
      // A turn may produce an assistant message with no text (tool-only) or just
      // whitespace; don't render an empty bubble — unless there's reasoning to show.
      if (!block.streaming && !hasText && !hasThinking) return null
      return (
        <BotRow>
          <div className="flex flex-col gap-2">
            {hasThinking && <ThinkingPanel text={block.thinking ?? ''} streaming={thinkingActive} />}
            {!thinkingActive && (hasText || block.streaming) && (
              <div
                ref={block.streaming ? aScroll.ref : undefined}
                onScroll={block.streaming ? aScroll.onScroll : undefined}
                className={`text-[15px] text-[#3f4653] leading-[1.75] break-words${block.streaming ? ' max-h-[58vh] overflow-y-auto pr-1' : ''}`}
              >
                <Markdown onOpenFile={onOpenFile} resolveFile={resolveFile}>{block.text}</Markdown>
                {block.streaming && <span className="caret">▍</span>}
              </div>
            )}
          </div>
        </BotRow>
      )
    }
    case 'error':
      return (
        <BotRow>
          <div className="max-w-full px-3.5 py-2.5 rounded-[10px] text-[13.5px] bg-redbg text-red">{block.text}</div>
        </BotRow>
      )
    case 'warning':
      return (
        <BotRow>
          <div className="max-w-full px-3.5 py-2.5 rounded-[10px] text-[13.5px] bg-[#fff7e8] text-[#9a6b12]">{block.text}</div>
        </BotRow>
      )
    case 'notice':
      return (
        <div className="flex justify-center anim-rise">
          <div className="px-3 py-1.5 rounded-full text-[12.5px] bg-surface2 border border-line2 text-muted">
            {block.text}
          </div>
        </div>
      )
    case 'compaction':
      return (
        <div className="flex flex-col items-center gap-1 my-1 anim-rise select-none" title="较早的对话已折叠为一条摘要，仍完整保存在磁盘会话记录中">
          <div className="flex items-center gap-3 w-full">
            <div className="flex-1 h-px bg-line2" />
            <span className="text-[11.5px] text-faint whitespace-nowrap">
              已压缩对话 · {block.before} → {block.after} 条
            </span>
            <div className="flex-1 h-px bg-line2" />
          </div>
          <span className="text-[11px] text-faint font-mono tabular-nums">
            本次压缩 ↑{fmtTokens(block.inTok)} ↓{fmtTokens(block.outTok)} · 当前上下文 ≈{fmtTokens(block.contextTokens)}
          </span>
        </div>
      )
    case 'retry':
      return (
        <div className="flex items-center gap-3 my-1 anim-rise select-none" title="模型请求连接中断，正在自动重试（磁盘记录不受影响）">
          <div className="flex-1 h-px bg-[#f0c98a]" />
          <span className="text-[11.5px] text-[#9a6b12] whitespace-nowrap">
            连接中断：{block.reason} · 重试 {block.attempt}/{block.maxAttempts}
          </span>
          <div className="flex-1 h-px bg-[#f0c98a]" />
        </div>
      )
    case 'usage':
      return (
        <BotRow>
          <div className="flex justify-end -mt-1.5">
            <span
              className="text-[11px] text-faint font-mono tabular-nums select-none"
              title="本轮用量(仅本会话的模型调用;子代理另计)"
            >
              ↑{fmtTokens(block.inTok)} ↓{fmtTokens(block.outTok)}{block.durMs ? ` · ${fmtDuration(block.durMs)}` : ''}
            </span>
          </div>
        </BotRow>
      )
    case 'tool':
      // Live Task with sub-agent activity → nested observable view; a resumed Task
      // (no live nested data) falls back to the normal card showing its result.
      if (block.tool.toolName === 'Task' && block.nested) return <BotRow><AgentTaskCard tool={block.tool} nested={block.nested} /></BotRow>
      return <BotRow><ExecutionCard tools={[block.tool]} /></BotRow>
    case 'planchoice':
      return null // rendered as PlanChoiceCard in the group loop
  }
}
