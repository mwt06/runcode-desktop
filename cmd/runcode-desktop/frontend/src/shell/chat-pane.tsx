// ChatPane 是对话流本身：顶部的计划进度胶囊、可滚动的消息区（把 groupBlocks 分好
// 的组映射成各类卡片）、空态与"思考中"指示。纯展示——所有交互都经 props 回调。
import { type ReactNode } from 'react'
import { Logo } from '@/ui/icons'
import { BRAND } from '@/core/brand'
import { shortenPath } from '@/core/paths'
import { groupBlocks, type Block } from '@/chat/blocks'
import { type RecordingMark } from '@/recorder/minutes'
import { diffStats, hasDiff } from '@/chat/tool-text'
import { AgentTaskGroup } from '@/chat/agent-task'
import { AnalyzeCard } from '@/chat/analyze-card'
import { SkillCard } from '@/chat/skill-card'
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

export function ChatPane({
  blocks, busy, cwd, userName, plan, planOpen, onPlanToggle, planning,
  harmAllows, revertedEdits, files, tabs,
  scrollRef, onScroll,
  onAnswer, onOpenFile, onReviewEdit, onUndoEdit, resolveFile,
  recorderCard,
  onGenerateMinutes,
}: {
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
}) {
  const groups = groupBlocks(blocks)
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
      <div className="flex-1 overflow-y-auto bg-surface px-6 pt-3 pb-8" ref={scrollRef} onScroll={onScroll}>
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
            g.kind === 'exec' ? (
              <BotRow key={g.id}><ExecutionCard tools={g.tools} harmAllows={harmAllows} /></BotRow>
            ) : g.kind === 'ask' ? (
              <BotRow key={g.id}><AskCard tool={g.tool} busy={busy} onAnswer={onAnswer} /></BotRow>
            ) : g.kind === 'edits' ? (
              <BotRow key={g.id}><EditedCards edits={g.edits} reverted={revertedEdits} onReview={onReviewEdit} onUndo={onUndoEdit} /></BotRow>
            ) : g.kind === 'analyze' ? (
              <BotRow key={g.id}><AnalyzeCard tool={g.tool} /></BotRow>
            ) : g.kind === 'skill' ? (
              <BotRow key={g.id}><SkillCard tool={g.tool} /></BotRow>
            ) : g.kind === 'taskgroup' ? (
              <BotRow key={g.id}><AgentTaskGroup tasks={g.tasks} /></BotRow>
            ) : (
              <div key={g.block.id}>
                <BlockView block={g.block} onOpenFile={onOpenFile} resolveFile={resolveFile} onGenerateMinutes={onGenerateMinutes} />
                {g.block.kind === 'assistant' && (
                  <ReplyArtifacts text={g.block.text} files={files} tabs={tabs} onOpen={onOpenFile} />
                )}
              </div>
            ),
          )}
          {recorderCard && <BotRow>{recorderCard}</BotRow>}
          {busy && (
            <BotRow>
              <div className="inline-flex items-center gap-2.5 text-faint py-1">
                <span className="think-dots"><i /><i /><i /></span>
                <span className="think-label text-[13px] font-medium tracking-wide">思考中</span>
              </div>
            </BotRow>
          )}
        </div>
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
