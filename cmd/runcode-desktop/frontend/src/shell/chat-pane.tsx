// ChatPane 是对话流本身：顶部的计划进度胶囊、可滚动的消息区（把 groupBlocks 分好
// 的组映射成各类卡片）、空态与"思考中"指示。纯展示——所有交互都经 props 回调。
import { Logo } from '@/ui/icons'
import { BRAND } from '@/core/brand'
import { shortenPath } from '@/core/paths'
import { groupBlocks, type Block } from '@/chat/blocks'
import { diffStats, hasDiff } from '@/chat/tool-text'
import { AgentTaskGroup } from '@/chat/agent-task'
import { AnalyzeCard } from '@/chat/analyze-card'
import { AskCard, PlanChoiceCard } from '@/chat/ask-card'
import { BlockView } from '@/chat/block-view'
import { BotRow } from '@/chat/bot-row'
import { EditedCards } from '@/chat/edited-card'
import { ExecutionCard } from '@/chat/execution-card'
import { PlanPill } from '@/chat/plan-pill'
import { ReplyArtifacts } from '@/chat/reply-artifacts'
import { type PreviewTab } from '@/preview/tabs'
import { type PlanSnapshot } from '@/core/bridge'

export function ChatPane({
  blocks, busy, cwd, userName, plan, planOpen, onPlanToggle,
  harmAllows, revertedEdits, files, tabs,
  scrollRef, onScroll,
  onAnswer, onExecutePlan, onDismissPlan, onOpenFile, onReviewEdit, onUndoEdit, resolveFile,
}: {
  blocks: Block[]
  busy: boolean
  cwd?: string
  userName?: string
  plan: PlanSnapshot | null
  planOpen: boolean
  onPlanToggle: (open: boolean) => void
  harmAllows: Record<string, string>
  revertedEdits: Set<string>
  files: string[]
  tabs: PreviewTab[]
  scrollRef: React.RefObject<HTMLDivElement>
  onScroll: () => void
  onAnswer: (text: string) => void
  onExecutePlan: (mode: string) => void
  onDismissPlan: () => void
  onOpenFile: (relPath: string) => void
  onReviewEdit: (snapshotId: string, relPath: string) => void
  onUndoEdit: (snapshotId: string) => void
  resolveFile: (token: string) => string | null
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

  return (
    <>
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
              <span className="inline-flex items-center justify-center w-[52px] h-[52px] rounded-[15px] mb-3.5 bg-surface border border-line2 shadow-xs"><Logo size={34} /></span>
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
            ) : g.kind === 'taskgroup' ? (
              <BotRow key={g.id}><AgentTaskGroup tasks={g.tasks} /></BotRow>
            ) : g.block.kind === 'planchoice' ? (
              <BotRow key={g.block.id}><PlanChoiceCard busy={busy} onExecute={onExecutePlan} onDismiss={onDismissPlan} /></BotRow>
            ) : (
              <div key={g.block.id}>
                <BlockView block={g.block} onOpenFile={onOpenFile} resolveFile={resolveFile} />
                {g.block.kind === 'assistant' && (
                  <ReplyArtifacts text={g.block.text} files={files} tabs={tabs} onOpen={onOpenFile} />
                )}
              </div>
            ),
          )}
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
    </>
  )
}
