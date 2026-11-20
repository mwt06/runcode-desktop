// ChatPane 是对话流本身：顶部的计划进度胶囊、可滚动的消息区（把 groupBlocks 分好
// 的组映射成各类卡片）、空态与"思考中"指示。纯展示——所有交互都经 props 回调。
import { useCallback, useMemo, useRef, type ReactNode } from 'react'
import { Logo } from '@/ui/icons'
import { BRAND } from '@/core/brand'
import { shortenPath } from '@/core/paths'
import { groupBlocks, type Block } from '@/chat/blocks'
import { type RecordingMark } from '@/recorder/minutes'
import { diffStats, hasDiff } from '@/chat/tool-text'
import { AgentTaskGroup } from '@/chat/agent-task'
import { AnalyzeCard } from '@/chat/analyze-card'
import { SkillCard } from '@/chat/skill-card'
import { GeneratedImagePreview } from '@/chat/generated-image-preview'
import {
  buildGeneratedImagePresentation,
  isMergedRasterOpenPreview,
} from '@/chat/generated-image-presentation'
import {
  isImageGenerateToolRunning,
  isImageGenerationResult,
  shouldShowImageGenerationStage,
} from '@/core/image-generation'
import { AskCard } from '@/chat/ask-card'
import { BlockView } from '@/chat/block-view'
import { BotRow } from '@/chat/bot-row'
import { EditedCards } from '@/chat/edited-card'
import { ExecutionCard } from '@/chat/execution-card'
import { PlanPill } from '@/chat/plan-pill'
import { PlanBoard, PlanStageBar } from '@/chat/plan-board'
import { ReplyArtifacts } from '@/chat/reply-artifacts'
import { type PreviewTab } from '@/preview/tabs'
import { PlanStates, type PlanSnapshot } from '@/core/bridge'
import { type Plan } from '@/session/use-plan'
import { InlinePreviewBudgetProvider } from '@/chat/inline-preview-budget'
import type { CodePreviewTarget } from '@/preview/code-preview'

export function ChatPane({
  blocks, busy, cwd, userName, plan, planOpen, onPlanToggle, planning, textOnly = false,
  harmAllows, oaBlocked, revertedEdits, files, tabs,
  scrollRef, onScroll,
  onAnswer, onOpenFile, onReviewEdit, onUndoEdit, resolveFile,
  recorderCard,
  onGenerateMinutes, onRetryQuestion, onEditQuestion, questionActionsDisabled,
  onOpenImageGenerationSettings,
  sessionId,
  onRasterImageView,
  onRasterImageAnnotate,
  onOpenCodePreview,
}: {
  textOnly?: boolean
  onRetryQuestion?: (id: string) => void
  onEditQuestion?: (id: string) => void
  questionActionsDisabled?: boolean
  blocks: Block[]
  busy: boolean
  cwd?: string
  userName?: string
  plan: PlanSnapshot | null
  planOpen: boolean
  onPlanToggle: (open: boolean) => void
  // planning 是阶段化计划模式的那条流水线（与上面 TodoWrite 的进度胶囊无关）：
  // 跑阶段时顶部出进度条，三个阶段跑完后输入区上方出审批板。
  planning: Plan
  harmAllows: Record<string, string>
  oaBlocked: Record<string, string>
  revertedEdits: Set<string>
  files: string[]
  tabs: PreviewTab[]
  scrollRef: React.RefObject<HTMLDivElement>
  onScroll: () => void
  onAnswer: (text: string) => void
  onOpenFile: (relPath: string) => void
  onReviewEdit: (snapshotId: string, relPath: string) => void
  onUndoEdit: (snapshotId: string) => void
  resolveFile: (token: string) => string | null
  // recorderCard 钉在对话末尾。它不是一条消息——录音每秒都在变，走消息那条路
  // 等于每秒往历史里塞一条；但它属于这条对话，所以位置在这里而不是浮在别处。
  recorderCard?: ReactNode
  // onGenerateMinutes 由历史里的录音卡片触发「重新生成纪要」。
  onGenerateMinutes?: (mark: RecordingMark) => void
  onOpenImageGenerationSettings?: () => void
  sessionId?: string
  onRasterImageView?: (image: import('@/core/bridge').ImageGenerationArtifact, toolUseId: string) => void
  onRasterImageAnnotate?: (image: import('@/core/bridge').ImageGenerationArtifact, toolUseId: string) => void
  onOpenCodePreview?: (target: CodePreviewTarget) => void
}) {
  const groups = groupBlocks(blocks)
  // 外部生图进行中时，卡片下方已有扫光舞台，不再叠「思考中」三点。
  const imageGenerateRunning = useMemo(
    () => blocks.some((b) => b.kind === 'tool' && isImageGenerateToolRunning(b.tool)),
    [blocks],
  )
  // 栅格生图展示投影：合并指向本条产物的 open_preview，避免执行卡重复占行。
  const imagePresentations = useMemo(() => {
    if (!sessionId) return []
    return buildGeneratedImagePresentation(blocks, cwd ?? '', {
      sessionId,
      sessionEpoch: 0,
      selectedCopies: new Map(),
    })
  }, [blocks, cwd, sessionId])
  // 内联预览高度晚于文本：只在用户本来贴底时跟随。贴底状态由 ChatPane 自己维护，
  // 不经 App.tsx，避免与用量页等其它滚动容器抢同一份逻辑。
  const stick = useRef(true)
  const handleScroll = useCallback(() => {
    const el = scrollRef.current
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48
    onScroll()
  }, [scrollRef, onScroll])
  const onContentHeightChange = useCallback(() => {
    const el = scrollRef.current
    if (el && stick.current) el.scrollTo({ top: el.scrollHeight })
  }, [scrollRef])
  // 计划胶囊上的聚合数字：本会话改过的文件数与累计增删行数。
  const fileSet = new Set<string>()
  let planAdds = 0
  let planDels = 0
  for (const b of blocks) {
    if (b.kind !== 'tool') continue
    if (hasDiff(b.tool) && b.tool.files?.[0]?.path) fileSet.add(b.tool.files[0].path)
    const { add, del } = diffStats(b.tool)
    planAdds += add
    planDels += del
  }

  const planState = planning.run.state
  const showStageBar = planState === PlanStates.Planning || planState === PlanStates.AwaitingApproval
  const showBoard = planState === PlanStates.AwaitingApproval

  return (
    <>
      {showStageBar && (
        <PlanStageBar
          run={planning.run}
          busy={busy}
          onResume={planning.resume}
          onCancel={() => void planning.cancel()}
        />
      )}
      {plan && (
        <div className="flex-none relative flex justify-center pt-3 pb-1 z-20">
          {planOpen && <div className="fixed inset-0 z-0" onClick={() => onPlanToggle(false)} />}
          <PlanPill
            plan={plan}
            open={planOpen}
            onToggle={onPlanToggle}
            filesChanged={fileSet.size}
            adds={planAdds}
            dels={planDels}
            running={busy}
          />
        </div>
      )}
      <div className="flex-1 overflow-y-auto bg-surface px-6 pt-3 pb-8" data-chat-scroll="" ref={scrollRef} onScroll={handleScroll}>
        {/* HTML 内联预览名额挂在本消息区：跨消息共享 liveFrames，切会话随子树卸载释放。 */}
        <InlinePreviewBudgetProvider>
        <div className="mx-auto max-w-[1200px] flex flex-col gap-6">
          {blocks.length === 0 && (
            <div className="mt-[16vh] text-center text-faint">
              {BRAND.greetingMark ? (
                // 插画自带形状与背景,直出不套框(见 core/brand 的 GreetingMark)。
                <img
                  src={BRAND.greetingMark.src}
                  alt={BRAND.greetingMark.alt}
                  width={BRAND.greetingMark.size}
                  height={BRAND.greetingMark.size}
                  draggable={false}
                  className="mx-auto mb-3 object-contain select-none"
                  style={{ width: BRAND.greetingMark.size, height: BRAND.greetingMark.size }}
                />
              ) : (
                <span className="inline-flex items-center justify-center w-[52px] h-[52px] rounded-[15px] mb-3.5 bg-surface border border-line2 shadow-xs"><Logo size={34} /></span>
              )}
              {BRAND.greeting === 'welcome' ? (
                // 未登录(本地自定义模型)时没有用户名,退成不带称呼的问候。
                <p>{userName ? `${userName}老师您好，今天有什么可以帮您？` : '老师您好，今天有什么可以帮您？'}</p>
              ) : (
                <p>让 {BRAND.name} 在 <code className="font-mono bg-surface border border-line2 px-2 py-0.5 rounded-md text-muted">{shortenPath(cwd)}</code> 中探索、修改或运行点什么。</p>
              )}
            </div>
          )}
          {groups.map((g) =>
            g.kind === 'image' ? (() => {
              // image 组只渲染结果预览；分组阶段已保证生图不会进入 ExecutionCard，
              // 但来源 session 仍随每个工具保留，查看器和标注必须绑定原会话。
              const kept = g.tools
                .map((block, i) => ({ tool: block.tool, sessionId: g.sessionIds[i] ?? sessionId ?? '' }))
                .filter(({ tool }) => !isMergedRasterOpenPreview(tool, imagePresentations, cwd ?? '', sessionId ?? ''))
              if (kept.length === 0) return null
              return (
                <BotRow key={g.id}>
                  {kept.filter(({ tool }) => shouldShowImageGenerationStage(tool)).map(({ tool, sessionId: sid }) => {
                    const result = isImageGenerationResult(tool.data) ? tool.data : null
                    const image = result?.status === 'succeeded' ? result.images?.[0] : undefined
                    return (
                      <GeneratedImagePreview
                        key={tool.toolUseID ?? tool.toolName}
                        tool={tool}
                        sessionId={sid || sessionId || ''}
                        image={image}
                        onView={() => { if (image) onRasterImageView?.(image, tool.toolUseID ?? '') }}
                        // 标注入口保留；开关关闭时 onRasterImageAnnotate 为空。
                        onAnnotate={onRasterImageAnnotate
                          ? () => { if (image) onRasterImageAnnotate(image, tool.toolUseID ?? '') }
                          : undefined}
                      />
                    )
                  })}
                </BotRow>
              )
            })() : g.kind === 'exec' ? (() => {
              const kept = g.tools
                .map((tool, i) => ({ tool, sessionId: g.sessionIds[i] ?? sessionId ?? '' }))
                .filter(({ tool }) => !isMergedRasterOpenPreview(tool, imagePresentations, cwd ?? '', sessionId ?? ''))
              if (kept.length === 0) return null
              const tools = kept.map((k) => k.tool)
              return (
                <BotRow key={g.id}>
                  <ExecutionCard tools={tools} harmAllows={harmAllows} oaBlocked={oaBlocked} />
                </BotRow>
              )
            })() : g.kind === 'ask' ? (
              <BotRow key={g.id}><AskCard tool={g.tool} busy={busy} onAnswer={onAnswer} /></BotRow>
            ) : g.kind === 'edits' ? (
              <BotRow key={g.id}><EditedCards edits={g.edits} reverted={revertedEdits} onReview={onReviewEdit} onUndo={onUndoEdit} /></BotRow>
            ) : g.kind === 'analyze' ? (
              <BotRow key={g.id}><AnalyzeCard tool={g.tool} /></BotRow>
            ) : g.kind === 'skill' ? (
              <BotRow key={g.id}><SkillCard tool={g.tool} /></BotRow>
            ) : g.kind === 'taskgroup' ? (
              <BotRow key={g.id}><AgentTaskGroup tasks={g.tasks} sessionId={sessionId} onOpenCodePreview={onOpenCodePreview} /></BotRow>
            ) : (
              <div key={g.block.id}>
                <BlockView textOnly={textOnly} onRetryQuestion={onRetryQuestion} onEditQuestion={onEditQuestion} questionActionsDisabled={questionActionsDisabled} block={g.block} onOpenFile={onOpenFile} onOpenCodePreview={onOpenCodePreview} resolveFile={resolveFile} onGenerateMinutes={onGenerateMinutes} onContentHeightChange={onContentHeightChange} onOpenImageGenerationSettings={onOpenImageGenerationSettings} sessionId={sessionId} />
                {!textOnly && g.block.kind === 'assistant' && (
                  <ReplyArtifacts text={g.block.text} files={files} tabs={tabs} onOpen={onOpenFile} />
                )}
              </div>
            ),
          )}
          {recorderCard && <BotRow>{recorderCard}</BotRow>}
          {busy && !imageGenerateRunning && (
            <BotRow>
              <div className="inline-flex items-center gap-2.5 text-faint py-1">
                <span className="think-dots"><i /><i /><i /></span>
                <span className="think-label text-[13px] font-medium tracking-wide">思考中</span>
              </div>
            </BotRow>
          )}
        </div>
        </InlinePreviewBudgetProvider>
      </div>
      {/* 审批板钉在输入区上方，不随对话滚走：这是一道必须有人应答的闸门。 */}
      {showBoard && (
        <PlanBoard
          run={planning.run}
          draft={planning.draft}
          approving={planning.approving}
          busy={busy}
          actions={planning.actions}
          onApprove={(mode) => void planning.approve(mode)}
          onCancel={() => void planning.cancel()}
        />
      )}
    </>
  )
}
