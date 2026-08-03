// BlockView 是对话流的单块渲染入口：把一个 Block 画成对应的行——用户消息、助手
// 回复(含思考面板)、错误/警告/提示条、压缩与重试分割线、用量脚注、工具卡。
// 分组类(执行卡/子代理组/编辑组)由上层 groupBlocks 决定，不走这里。
import { Icon } from '@/ui/icons'
import { Markdown } from '@/ui/markdown'
import { fmtDuration, fmtTokens } from '@/core/format'
import { useStickToBottom } from '@/hooks/use-stick-to-bottom'
import { type Block, retryReasonLabel } from './blocks'
import { BotRow } from './bot-row'
import { ThinkingPanel } from './thinking-panel'
import { AgentTaskCard } from './agent-task'
import { ExecutionCard } from './execution-card'

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
        <div className="flex items-center gap-3 my-1 anim-rise select-none" title="模型请求中断，正在自动重试（磁盘记录不受影响）">
          <div className="flex-1 h-px bg-[#f0c98a]" />
          <span className="text-[11.5px] text-[#9a6b12] whitespace-nowrap">
            {retryReasonLabel(block.reason)} · 重试 {block.attempt}/{block.maxAttempts}
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
  }
}
