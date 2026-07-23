// 子代理(Task)委派的可观测视图：单个委派卡、并行委派分组卡，以及两者共用的
// 嵌套详情面板(子代理自己的工具调用 + 流式文本)。
import { useState } from 'react'
import { Icon, toolIcon } from '@/ui/icons'
import { CheckMark, Spinner } from '@/ui/glyphs'
import { Markdown } from '@/ui/markdown'
import { fmtDuration, fmtTokens } from '@/core/format'
import { type ToolEvent } from '@/core/bridge'
import { useStickToBottom } from '@/hooks/use-stick-to-bottom'
import { type AgentNested, type ToolBlock } from './blocks'
import { taskActivity, taskMeta, toolLabel } from './tool-text'
import { ToolDetail } from './tool-detail'

// AgentTaskCard renders a Task delegation as a live, observable nested view: the
// sub-agent's streamed text plus its own tool calls (each drillable). It is
// expanded while the sub-agent runs and collapses to a summary when it finishes.
export function AgentTaskCard({ tool, nested }: { tool: ToolEvent; nested?: AgentNested }) {
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
