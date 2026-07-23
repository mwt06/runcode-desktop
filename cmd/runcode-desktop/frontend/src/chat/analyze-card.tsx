// AnalyzeCard visualizes the model's structured-thinking protocol (the Analyze
// tool): the method as a badge and each protocol step as a numbered timeline entry
// with its label and the model's task-specific content. Collapsible; shows a live
// "分析中…" hint until the analysis completes.
import { useState } from 'react'
import { Icon } from '@/ui/icons'
import { type ToolEvent } from '@/core/bridge'
import { analyzeSteps } from './tool-text'

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
