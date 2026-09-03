// 阶段化计划模式的两块界面：跑阶段时顶部的阶段进度条，以及三个阶段跑完后钉在输入区
// 上方的审批板——步骤在这里可以改写、上下移、增删，确认后才退出计划模式开始执行。
//
// 编辑的纯逻辑全在 chat/plan-draft.ts；这里只负责呈现与交互，别把顺序调整那类判断
// 写回组件里。
import { Icon } from '@/ui/icons'
import { CheckMark, Spinner } from '@/ui/glyphs'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { HScroll } from '@/ui/h-scroll'
import { PlanStages, PlanStates, type PlanDoc, type PlanRun, type PlanStep } from '@/core/bridge'
import { canApprove } from './plan-draft'

// 流水线的四步。前三步由模型跑（对应 plan_write 的 stage），第四步是用户的闸门——
// 它不发模型，所以没有对应的 stage 值。
const STAGES: { key: string; label: string }[] = [
  { key: PlanStages.Understanding, label: '需求理解' },
  { key: PlanStages.Design, label: '方案设计' },
  { key: PlanStages.Review, label: '方案审查' },
  { key: 'approval', label: '用户审批' },
]

// stageProgress 把运行状态换算成"第几步在跑、前几步已完成"。等待审批时前三步全完成、
// 第四步在跑；执行中四步全完成。
function stageProgress(run: PlanRun): { done: number; active: number } {
  if (run.state === PlanStates.AwaitingApproval) return { done: 3, active: 3 }
  if (run.state === PlanStates.Executing) return { done: 4, active: -1 }
  const recorded = STAGES.findIndex((s) => s.key === run.stage)
  return { done: recorded + 1, active: recorded + 1 }
}

// PlanStageBar 是规划期间的进度条：模型走到哪一阶段、下一阶段是什么，一眼可见。
// 模型本该一个回合连跑三个阶段；万一中途收尾了，这里给一个「继续规划」把它接上，
// 而不是让用户自己猜该说什么。
export function PlanStageBar({ run, busy, onResume, onCancel }: {
  run: PlanRun
  busy: boolean
  onResume: () => void
  onCancel: () => void
}) {
  const { done, active } = stageProgress(run)
  const stalled = !busy && run.state === PlanStates.Planning
  return (
    <div className="flex-none flex items-center gap-3 px-6 py-2 border-b border-line2 bg-surface2">
      <span className="text-[13px] text-muted flex-none">计划模式</span>
      <HScroll rowClassName="gap-1.5">
        {STAGES.map((stage, i) => (
          <div key={stage.key} className="flex items-center gap-1.5 flex-none">
            {i > 0 && <span className={`w-4 h-px ${i <= done ? 'bg-primary' : 'bg-line2'}`} />}
            <span
              className={`inline-flex items-center gap-1 text-[13px] px-2 py-0.5 rounded-full border ${
                i < done
                  ? 'border-transparent text-green'
                  : i === active
                    ? 'border-primary text-primaryink bg-primarysoft font-medium'
                    : 'border-line2 text-faint'
              }`}
            >
              {i < done ? <CheckMark size={11} /> : i === active && busy ? <Spinner size={11} /> : null}
              {stage.label}
            </span>
          </div>
        ))}
      </HScroll>
      <div className="flex-1" />
      {stalled && (
        <button onClick={onResume} className="flex-none text-[13px] text-primaryink font-medium hover:brightness-110 cursor-pointer">
          继续规划
        </button>
      )}
      <button onClick={onCancel} className="flex-none text-[13px] text-muted hover:text-ink cursor-pointer">
        取消规划
      </button>
    </div>
  )
}

// PlanBoard 是审批板：目标、可编辑的步骤清单、审查发现与风险，以及两种执行模式的确认
// 按钮。它钉在输入区上方而不是随对话滚走——这是一道必须有人应答的闸门。
export function PlanBoard({
  run, draft, approving, busy, actions, onApprove, onCancel,
}: {
  run: PlanRun
  draft: PlanDoc | null
  approving: boolean
  busy: boolean
  actions: {
    move: (index: number, delta: number) => void
    remove: (index: number) => void
    insertAfter: (index: number) => void
    patch: (index: number, patch: Partial<PlanStep>) => void
    flush: () => void
  }
  onApprove: (mode: string) => void
  onCancel: () => void
}) {
  const steps = draft?.steps ?? []
  const ready = canApprove(draft) && !approving && !busy
  return (
    <div className="flex-none border-t border-primary bg-surface max-h-[52vh] overflow-y-auto">
      <div className="mx-auto max-w-[1200px] px-6 py-4">
        <div className="flex items-center gap-2 mb-3 text-primaryink">
          <Icon name="compass" size={16} />
          <span className="text-[13px] font-semibold">方案待确认</span>
          <span className="text-[13px] text-muted">
            可以改写、增删、调整顺序；确认后才会退出计划模式开始执行。
          </span>
        </div>

        {draft?.title && <div className="text-[14px] font-semibold text-ink mb-1.5">{draft.title}</div>}
        {draft?.goal && (
          <Field label="目标">
            <span className="whitespace-pre-wrap">{draft.goal}</span>
          </Field>
        )}
        {draft?.nonGoals?.length ? <Bullets label="不做" items={draft.nonGoals} /> : null}

        <div className="mt-3 mb-1.5 flex items-center gap-2">
          <span className="text-[13px] font-semibold text-ink">执行步骤</span>
          <span className="text-[12px] text-faint">{steps.length} 步</span>
        </div>
        <div className="flex flex-col gap-1.5">
          {steps.map((step, i) => (
            <StepRow
              key={step.id || i}
              step={step}
              index={i}
              last={i === steps.length - 1}
              actions={actions}
            />
          ))}
        </div>
        <button
          onClick={() => actions.insertAfter(steps.length - 1)}
          className="mt-2 text-[13px] text-primaryink hover:brightness-110 cursor-pointer inline-flex items-center gap-1"
        >
          <Icon name="plus" size={13} /> 新增步骤
        </button>

        {draft?.reviewNotes?.length ? <Bullets label="审查发现" items={draft.reviewNotes} /> : null}
        {draft?.risks?.length ? <Bullets label="风险" items={draft.risks} /> : null}
        {draft?.questions?.length ? (
          <Bullets label="需要你拍板" items={draft.questions} accent />
        ) : null}

        <div className="mt-4 flex flex-wrap items-center gap-2">
          <button onClick={() => onApprove('interactive')} disabled={!ready} className={`${BTN} ${BTN_PRIMARY} text-[13px] py-2`}>
            确认并执行（交互模式）
          </button>
          <button onClick={() => onApprove('judge')} disabled={!ready} className={`${BTN} text-[13px] py-2`}>
            确认并执行（智能模式）
          </button>
          <button onClick={onCancel} disabled={approving} className={`${BTN} text-[13px] py-2 !border-transparent !text-muted`}>
            取消
          </button>
          {!canApprove(draft) && (
            <span className="text-[12px] text-faint">清单为空，至少留一条步骤才能执行</span>
          )}
          {run.edited && canApprove(draft) && (
            <span className="text-[12px] text-faint">已按你的修改执行</span>
          )}
        </div>
      </div>
    </div>
  )
}

// StepRow 是一条可编辑步骤：序号、标题输入、详情输入，以及上移/下移/插入/删除。
function StepRow({ step, index, last, actions }: {
  step: PlanStep
  index: number
  last: boolean
  actions: {
    move: (index: number, delta: number) => void
    remove: (index: number) => void
    insertAfter: (index: number) => void
    patch: (index: number, patch: Partial<PlanStep>) => void
    flush: () => void
  }
}) {
  return (
    <div className="group flex gap-2 items-start bg-surface2 border border-line2 rounded-btn px-2.5 py-2 focus-within:border-primary">
      <span className="mt-1.5 w-5 flex-none text-[12px] text-faint tabular-nums text-right">{index + 1}</span>
      <div className="flex-1 min-w-0 flex flex-col gap-1">
        <input
          value={step.title}
          onChange={(e) => actions.patch(index, { title: e.target.value })}
          onBlur={actions.flush}
          placeholder="这一步做什么"
          className="w-full text-[14px] text-ink bg-transparent outline-none placeholder:text-faint"
        />
        <textarea
          value={step.detail ?? ''}
          onChange={(e) => actions.patch(index, { detail: e.target.value })}
          onBlur={actions.flush}
          rows={step.detail ? 2 : 1}
          placeholder="补充说明（可留空）"
          className="w-full resize-y text-[13px] text-muted bg-transparent outline-none placeholder:text-faint"
        />
        {step.files?.length ? (
          <div className="text-[12px] text-faint font-mono truncate">{step.files.join('  ')}</div>
        ) : null}
      </div>
      <div className="flex-none flex items-center gap-0.5 opacity-60 group-hover:opacity-100 transition">
        <IconBtn label="上移" disabled={index === 0} onClick={() => actions.move(index, -1)} name="chevron-down" flip />
        <IconBtn label="下移" disabled={last} onClick={() => actions.move(index, 1)} name="chevron-down" />
        <IconBtn label="在下方插入" onClick={() => actions.insertAfter(index)} name="plus" />
        <IconBtn label="删除" onClick={() => actions.remove(index)} name="trash" />
      </div>
    </div>
  )
}

// IconBtn 是一个纯图标的小按钮；flip 把图标上下翻转（上移复用下移的箭头，图标集里
// 只有 chevron-down 一个方向）。
function IconBtn({ name, label, onClick, disabled, flip }: {
  name: string
  label: string
  onClick: () => void
  disabled?: boolean
  flip?: boolean
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      className="p-1 rounded text-muted hover:text-ink hover:bg-surface disabled:opacity-25 disabled:cursor-default cursor-pointer"
    >
      <Icon name={name} size={14} className={flip ? 'rotate-180' : undefined} />
    </button>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="text-[13px] text-muted mt-1">
      <span className="text-faint">{label}：</span>
      {children}
    </div>
  )
}

function Bullets({ label, items, accent }: { label: string; items: string[]; accent?: boolean }) {
  return (
    <div className="mt-3">
      <div className={`text-[13px] font-semibold mb-1 ${accent ? 'text-primaryink' : 'text-ink'}`}>{label}</div>
      <ul className="flex flex-col gap-0.5">
        {items.map((item, i) => (
          <li key={i} className="text-[13px] text-muted flex gap-1.5">
            <span className="text-faint flex-none">·</span>
            <span className="whitespace-pre-wrap min-w-0">{item}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
