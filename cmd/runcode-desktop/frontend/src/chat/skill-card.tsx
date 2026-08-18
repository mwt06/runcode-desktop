// SkillCard announces that the model loaded a reusable workflow: which skill, what
// it is for, and where it came from. It exists because a skill load is a turning
// point in the conversation — from here the model follows those instructions — and
// the tool call alone shows nothing but a name.
//
// It is deliberately a single quiet row rather than an expandable panel: the skill's
// body is written for the model, not the user, and pasting it into the chat would
// bury the reply it is supposed to serve.
import { Icon } from '@/ui/icons'
import { type ToolEvent } from '@/core/bridge'
import { skillLoad } from './tool-text'

const SOURCE_ZH: Record<string, string> = { user: '用户', project: '项目' }

export function SkillCard({ tool }: { tool: ToolEvent }) {
  const view = skillLoad(tool)
  if (!view) return null
  const scope = SOURCE_ZH[view.source]
  return (
    <div className="anim-rise">
      <div className="flex items-start gap-2.5 bg-surface border border-line2 rounded-card shadow-xs px-3.5 py-2.5">
        <span className={`text-primary flex-none mt-px${view.running ? ' animate-pulse' : ''}`}>
          <Icon name="sparkles" size={16} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-[13px] text-muted flex-none">{view.failed ? '技能未加载' : view.running ? '加载技能' : '已加载技能'}</span>
            <span className="font-mono text-[13px] font-semibold text-ink break-all">{view.name}</span>
            {scope && <span className="text-[11px] text-primaryink bg-primarysoft rounded-full px-2 py-0.5 flex-none">{scope}</span>}
            {view.truncated && <span className="text-[11px] text-amber bg-amber/15 rounded-full px-2 py-0.5 flex-none">正文超长已截断</span>}
          </div>
          {view.description && (
            <div className="text-[13px] text-muted leading-[1.6] mt-1 break-words">{view.description}</div>
          )}
          {view.dir && (
            <div className="text-[12px] text-faint font-mono mt-1 break-all" title={view.dir}>{view.dir}</div>
          )}
        </div>
      </div>
    </div>
  )
}
