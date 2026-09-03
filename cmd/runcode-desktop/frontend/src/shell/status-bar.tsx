// 主栏顶部的次级状态条（整条也是窗口拖拽区）：侧栏折叠开关 + 运行/空闲指示，
// 右侧是上下文用量计与预览面板开关。
import { Icon } from '@/ui/icons'
import { DRAG, NO_DRAG } from '@/ui/tokens'
import { ContextMeter } from '@/chat/context-meter'

export function StatusBar({ busy, sidebarCollapsed, onToggleSidebar, waitingElsewhere, onGoWaiting, ctxTokens, ctxBudget, ctxEstimated, compacting, onCompact, onTogglePreview }: {
  busy: boolean
  sidebarCollapsed: boolean
  onToggleSidebar: () => void
  // waitingElsewhere 是**别的**会话里有几个操作在等人确认。
  waitingElsewhere: number
  onGoWaiting: () => void
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
        {/* 后台会话卡在等授权而界面毫无表示，是并行里最坏的失败：任务停着，用户
            以为它在跑。侧栏有角标，但侧栏可以折叠——所以这里再给一个不会被收起
            的入口，点它直接跳到最早在等的那条会话。 */}
        {waitingElsewhere > 0 && (
          <button
            style={NO_DRAG}
            onClick={onGoWaiting}
            title="有别的会话在等你确认操作，点这里过去"
            className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-amber text-white text-[12px] hover:opacity-90 transition"
          >
            <Icon name="shield" size={13} />
            {waitingElsewhere} 个会话在等你
          </button>
        )}
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
