// ExecutionCard is the agent's activity panel: a full-width card (no avatar) whose
// rows are the tool calls in one run. Each row shows the tool's icon chip, its verb +
// mono target, a diff badge for edits, and a live/done/failed status; clicking a row
// drills into its parameters and output. A running tool auto-expands so its
// streaming output stays visible.
import { useState } from 'react'
import { Icon, toolIcon } from '@/ui/icons'
import { CheckMark, Spinner } from '@/ui/glyphs'
import { DiffStat } from '@/ui/badges'
import { type ToolEvent } from '@/core/bridge'
import { diffStats, failText, toolVerbTarget } from './tool-text'
import { ToolDetail } from './tool-detail'

export function ExecutionCard({ tools, harmAllows, oaBlocked }: {
  tools: ToolEvent[]
  harmAllows?: Record<string, string>
  /** tool-use id → 即将切去的本地模型。见 conversation-state 的 oaBlocked。 */
  oaBlocked?: Record<string, string>
}) {
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
        // 被 OA 闸门拦下的那一次不算"失败"：调用确实没跑成（模型据此停下来），
        // 但系统正在自动切换并重跑这一轮，红色报错会让用户以为要自己处理。
        const blockedModel = oaBlocked && t.toolUseID ? oaBlocked[t.toolUseID] : undefined
        const iconColor = blockedModel ? 'text-faint' : st === 'failed' ? 'text-red' : st === 'running' ? 'text-primary' : 'text-faint'
        const rowBg = active ? 'bg-surface2' : 'hover:bg-surface2'
        return (
          <div key={i}>
            <div
              onClick={() => setSel(sel === i ? null : i)}
              className={`flex items-center gap-2.5 px-2 py-1.5 rounded-lg cursor-pointer select-none ${rowBg}`}
            >
              <span className={`flex-none ${iconColor}`}><Icon name={toolIcon(t.toolName)} size={15} /></span>
              <span className="flex-1 min-w-0 truncate text-[14px] text-ink2">
                {verb}
                {target && <span className="font-mono text-faint"> {target}</span>}
              </span>
              {showDiff && <DiffStat add={add} del={del} className="font-mono text-[12px] tabular-nums flex-none" />}
              {allowReason && (
                <span title="智能模式已自动放行，展开查看原因" className="flex-none inline-flex items-center gap-1 text-[11px] text-primaryink bg-primarysoft rounded px-1.5 py-0.5">
                  <Icon name="shield" size={11} /> 智能放行
                </span>
              )}
              {blockedModel ? (
                <span
                  title={`OA 数据只能由内网部署的模型处理，正在切换到 ${blockedModel} 并自动重试`}
                  className="text-[11px] text-muted bg-surface2 rounded-md px-1.5 py-0.5 flex-none"
                >
                  已阻止 · 正在切换
                </span>
              ) : st === 'failed' ? (
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
