// 主栏顶部的次级状态条（整条也是窗口拖拽区）：侧栏折叠开关 + 运行/空闲指示，
// 右侧是上下文用量计与预览面板开关。
import { Icon } from '@/ui/icons'
import { DRAG, NO_DRAG } from '@/ui/tokens'
import { ContextMeter } from '@/chat/context-meter'

export function StatusBar({ busy, sidebarCollapsed, onToggleSidebar, ctxTokens, ctxBudget, ctxEstimated, compacting, onCompact, onTogglePreview }: {
  busy: boolean
  sidebarCollapsed: boolean
  onToggleSidebar: () => void
  ctxTokens: number
  ctxBudget: number
  ctxEstimated: boolean
  compacting: boolean
  onCompact: () => void
  onTogglePreview: () => void
}) {
  return (
    <header className="h-[52px] flex-none flex items-center justify-between pl-2 pr-1.5 bg-surface border-b border-line2 select-none" style={DRAG}>
      <div className="flex items-center gap-2">
        <button
          onClick={onToggleSidebar}
          title={sidebarCollapsed ? '展开侧栏' : '折叠侧栏'}
          style={NO_DRAG}
          className="flex-none flex items-center justify-center w-8 h-8 rounded-field text-muted hover:text-ink hover:bg-surface2 transition"
        >
          <Icon name="panel-left" size={17} />
        </button>
        <span className={`inline-flex items-center gap-1.5 text-[13px] ${busy ? 'text-green' : 'text-muted'}`}>
          <span className={`w-[7px] h-[7px] rounded-full ${busy ? 'bg-green blip shadow-[0_0_0_3px_rgba(31,157,99,0.16)]' : 'bg-faint'}`} />
          {busy ? '运行中' : '空闲'}
        </span>
      </div>
      <div className="flex items-center gap-3">
        <div className="flex items-center gap-[18px] text-muted text-[13px]" style={NO_DRAG}>
          <ContextMeter used={ctxTokens} budget={ctxBudget} estimated={ctxEstimated} onCompact={onCompact} compacting={compacting} busy={busy} />
        </div>
        <button
          style={NO_DRAG}
          className="text-muted hover:text-ink text-[13px] px-2"
          title="文件预览"
          onClick={onTogglePreview}
        >预览</button>
      </div>
    </header>
  )
}
