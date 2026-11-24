// BlockView 是对话流的单块渲染入口：把一个 Block 画成对应的行——用户消息、助手
// 回复(含思考面板)、系统事件条(错误/警告/提示/压缩/重试)、用量脚注、工具卡。
// 分组类(执行卡/子代理组/编辑组)由上层 groupBlocks 决定，不走这里。
//
// 只有「模型说的话」进 BotRow(助手列)。系统自己的播报一律走 ui/feedback 的
// SystemNote 分割线条——此前错误/警告是助手列里的气泡、提示是居中胶囊、压缩与
// 重试各自手写了一遍同样的分割线，四种形状表达同一件事。
import { GhostBtn } from '@/ui/ghost-btn'
import { Icon } from '@/ui/icons'
import { SystemNote } from '@/ui/feedback'
import { AssistantMarkdown } from './assistant-markdown'
import { WarnTriangle } from '@/ui/glyphs'
import { fmtDuration, fmtTokens } from '@/core/format'
import { useStickToBottom } from '@/hooks/use-stick-to-bottom'
import { type RecordingMark } from '@/recorder/minutes'
import { type Block, retryReasonLabel } from './blocks'
import { BotRow } from './bot-row'
import { AgentTaskCard } from './agent-task'
import { ExecutionCard } from './execution-card'
import { RecordingCard } from './recorder-card'
import {
  displayImageGenerationSizeLabel,
  scrubImageGenerationAssistantText,
} from './image-generation-note'
import type { CodePreviewTarget } from '@/preview/code-preview'

export function BlockView({ block, onOpenFile, resolveFile, onGenerateMinutes, onRetryQuestion, onEditQuestion, questionActionsDisabled, textOnly = false, onContentHeightChange, onOpenImageGenerationSettings, onOpenMailSettings, onOpenMailDraft, sessionId, onOpenCodePreview }: {
  textOnly?: boolean
  block: Block
  onRetryQuestion?: (id: string) => void
  onEditQuestion?: (id: string) => void
  questionActionsDisabled?: boolean
  onOpenFile?: (relPath: string) => void
  resolveFile?: (token: string) => string | null
  onGenerateMinutes?: (mark: RecordingMark) => void
  onContentHeightChange?: () => void
  onOpenImageGenerationSettings?: () => void
  onOpenMailSettings?: () => void
  onOpenMailDraft?: (draftId: string) => void
  sessionId?: string
  onOpenCodePreview?: (target: CodePreviewTarget) => void
}) {
  // While an assistant answer streams, keep it in a fixed-height window pinned to
  // the newest text (consistent with the tool/agent cards); release to full height
  // when done so the finished answer reads normally in the page flow.
  const aScroll = useStickToBottom(block.kind === 'assistant' && block.streaming ? block.text.length : null)
  switch (block.kind) {
    case 'user':
      return (
        <div className="group flex flex-col items-end gap-1 anim-rise">
          <div className="min-w-0 max-w-[82%] rounded-[13px] px-3.5 py-2 text-[14px] text-ink leading-[1.55] bg-userbg">
            {block.attachments && block.attachments.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mb-1.5">
                {block.attachments.map((name, i) => (
                  <span key={name + i} className="inline-flex items-center gap-1 bg-surface border border-line2 rounded-[7px] px-2 py-0.5 text-[12px] text-muted max-w-[220px]">
                    <Icon name="file" size={12} /> <span className="truncate">{name}</span>
                  </span>
                ))}
              </div>
            )}
            {block.text && <div className="whitespace-pre-wrap break-words">{block.text}</div>}
          </div>          {block.questionId && (onRetryQuestion || onEditQuestion) && (
            <div className="flex items-center opacity-60 group-hover:opacity-100 focus-within:opacity-100">
              <GhostBtn disabled={questionActionsDisabled} onClick={() => onRetryQuestion?.(block.questionId!)} aria-label="重新生成" title="重新生成：从这条提问重新开始；原记录保留，文件不会撤销">
                <Icon name="refresh" size={16} />
              </GhostBtn>
              <GhostBtn disabled={questionActionsDisabled} onClick={() => onEditQuestion?.(block.questionId!)} aria-label="修改后重试" title="修改后重试">
                <Icon name="pencil" size={16} />
              </GhostBtn>
            </div>
          )}
        </div>
      )
    case 'assistant': {
      // thinking 字段仍保留在 block 上（回放/压缩），界面不再渲染思考栏。
      const hasText = block.text.trim() !== ''
      // 无正文的已完成回合不画空气泡（纯工具回合由工具卡承担）。
      if (!block.streaming && !hasText) return null
      return (
        <BotRow>
          <div className="flex flex-col gap-2">
            {(hasText || block.streaming) && (
              <div
                ref={block.streaming ? aScroll.ref : undefined}
                onScroll={block.streaming ? aScroll.onScroll : undefined}
                className={`text-[15px] text-ink2 leading-[1.75] break-words${block.streaming ? ' max-h-[58vh] overflow-y-auto pr-1' : ''}`}
              >
                {hasText && (
                  <AssistantMarkdown
                    messageId={block.id}
                    streaming={block.streaming}
                    textOnly={textOnly}
                    sessionId={sessionId}
                    onOpenFile={onOpenFile}
                    resolveFile={resolveFile}
                    onContentHeightChange={onContentHeightChange}
                    onOpenCodePreview={onOpenCodePreview}
                  >
                    {scrubImageGenerationAssistantText(block.text)}
                  </AssistantMarkdown>
                )}
                {block.streaming && <span className="caret">▍</span>}
              </div>
            )}
            {!block.streaming && block.imageGenerationNote && (
              <div className="text-[12px] text-muted leading-snug flex flex-wrap items-center gap-x-2 gap-y-1">
                <span>
                  当前使用的生图模型：{block.imageGenerationNote.modelLabel}
                  {' · '}
                  生图尺寸：{displayImageGenerationSizeLabel(block.imageGenerationNote.sizeLabel)}
                </span>
                {onOpenImageGenerationSettings ? (
                  <button type="button" onClick={onOpenImageGenerationSettings} className="text-primary hover:underline cursor-pointer bg-transparent border-0 p-0 text-[12px]">
                    修改生图设置
                  </button>
                ) : (
                  <span className="text-faint">在「设置 → 图片生成」中修改生图设置</span>
                )}
              </div>
            )}
            {!block.streaming && block.mailSetupRequired && (
              <div className="text-[12px] text-amberink leading-snug flex flex-wrap items-center gap-x-2 gap-y-1">
                <span>尚未接入邮箱：请先填写邮箱地址与密码后再确认发送。</span>
                {onOpenMailSettings ? (
                  <button type="button" onClick={onOpenMailSettings} className="text-primary hover:underline cursor-pointer bg-transparent border-0 p-0 text-[12px]">
                    前往邮箱设置
                  </button>
                ) : (
                  <span className="text-faint">在「设置 → 邮箱」中接入</span>
                )}
              </div>
            )}
            {!block.streaming && block.mailDraftId && (
              <div className="text-[12px] text-muted leading-snug flex flex-wrap items-center gap-x-2 gap-y-1" data-testid="mail-draft-reply-link">
                <span>可在「邮箱与通信录」中编辑并确认发送。</span>
                {onOpenMailDraft ? (
                  <button
                    type="button"
                    onClick={() => onOpenMailDraft(block.mailDraftId!)}
                    className="text-primary hover:underline cursor-pointer bg-transparent border-0 p-0 text-[12px]"
                  >
                    查看草稿
                  </button>
                ) : (
                  <span className="text-faint">打开「更多 → 邮箱与通信录」查看草稿</span>
                )}
              </div>
            )}
          </div>
        </BotRow>
      )
    }
    // error / warning / notice / compaction / retry are all the app talking, not the
    // model — so they share one centered divider row (SystemNote) rather than sitting
    // in the assistant column as bubbles. Tone carries the severity; error and
    // warning stay selectable because the user may need to copy the reason out.
    case 'error':
      return (
        <SystemNote tone="danger" icon={<WarnTriangle />} selectable>
          {block.text}
        </SystemNote>
      )
    case 'warning':
      return (
        <SystemNote tone="warning" icon={<WarnTriangle />} selectable>
          {block.text}
        </SystemNote>
      )
    case 'recording':
      return <RecordingCard mark={block.mark} onGenerateMinutes={onGenerateMinutes} />
    case 'notice':
      return <SystemNote selectable>{block.text}</SystemNote>
    case 'compaction':
      return (
        <SystemNote
          title="较早的对话已折叠为一条摘要，仍完整保存在磁盘会话记录中"
          sub={
            <span className="text-[11px] text-faint font-mono tabular-nums">
              本次压缩 ↑{fmtTokens(block.inTok)} ↓{fmtTokens(block.outTok)} · 当前上下文 ≈{fmtTokens(block.contextTokens)}
            </span>
          }
        >
          已压缩对话 · {block.before} → {block.after} 条
        </SystemNote>
      )
    case 'retry':
      return (
        <SystemNote title={`自动重试：${retryReasonLabel(block.reason)}（完整历史不受影响）`}>
          刚才没接上，我再试一下
          <span className="font-mono tabular-nums">{block.attempt}/{block.maxAttempts}</span>
        </SystemNote>
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
      if (block.tool.toolName === 'Task' && block.nested) return <BotRow><AgentTaskCard tool={block.tool} nested={block.nested} sessionId={sessionId ?? block.sessionId} onOpenCodePreview={onOpenCodePreview} /></BotRow>
      return <BotRow><ExecutionCard tools={[block.tool]} /></BotRow>
  }
}
