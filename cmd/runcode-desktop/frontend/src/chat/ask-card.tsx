// 两张需要用户作答的卡片：AskUser 工具的提问卡，以及计划模式回合结束后的
// 「怎么继续」选择卡。都是对话流里就地交互，答完即定格。
import { useState } from 'react'
import { Icon } from '@/ui/icons'
import { isComposingKey } from '@/ui/keys'
import { type ToolEvent } from '@/core/bridge'
import { askPayload } from './tool-text'

// AskCard renders an AskUser tool call as an interactive question: the user picks
// a suggested option or types a custom reply, which is sent as the next message.
export function AskCard({ tool, busy, onAnswer }: { tool: ToolEvent; busy: boolean; onAnswer: (text: string) => void }) {
  const { question, options } = askPayload((tool as ToolEvent & { input?: unknown }).input)
  const [custom, setCustom] = useState('')
  const [answered, setAnswered] = useState<string | null>(null)
  function answer(text: string) {
    const t = text.trim()
    if (!t || answered || busy) return
    setAnswered(t)
    onAnswer(t)
  }
  return (
    <div className="anim-rise">
      <div className="min-w-0 flex-1 bg-surface border border-primary rounded-[14px] shadow-xs p-4">
        <div className="flex items-center gap-2 mb-2 text-primaryink">
          <Icon name="chat" size={16} />
          <span className="text-[12.5px] font-semibold">需要你的确认</span>
        </div>
        <div className="text-[14px] text-ink whitespace-pre-wrap mb-3">{question || '（无问题内容）'}</div>
        {answered ? (
          <div className="text-[13px] text-muted">已回复：<span className="text-ink">{answered}</span></div>
        ) : (
          <>
            {options.length > 0 && (
              <div className="flex flex-col gap-1.5 mb-2.5">
                {options.map((opt, i) => (
                  <button
                    key={i}
                    onClick={() => answer(opt)}
                    disabled={busy}
                    className="text-left text-[13.5px] text-ink bg-surface2 border border-line2 rounded-[10px] px-3.5 py-2 cursor-pointer hover:border-primary hover:bg-primarysoft disabled:opacity-40"
                  >
                    {opt}
                  </button>
                ))}
              </div>
            )}
            <div className="flex gap-2">
              <input
                value={custom}
                onChange={(e) => setCustom(e.target.value)}
                onKeyDown={(e) => {
                  // 输入法上屏用的 Enter 不是提交（见 ui/keys）。
                  if (isComposingKey(e)) return
                  if (e.key === 'Enter') answer(custom)
                }}
                placeholder="或输入你的回答…"
                className="flex-1 text-[13.5px] bg-surface2 text-ink border border-line2 rounded-[10px] px-3 py-2 outline-none focus:border-primary"
              />
              <button
                onClick={() => answer(custom)}
                disabled={!custom.trim() || busy}
                className="text-[13px] font-semibold text-white bg-primary px-3.5 rounded-[10px] cursor-pointer hover:brightness-105 disabled:opacity-40 disabled:cursor-default"
              >
                发送
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// PlanChoiceCard appears after a plan-mode turn: the user picks how to carry out
// the plan (interactive or judge permission mode) or keeps refining it.
export function PlanChoiceCard({ busy, onExecute, onDismiss }: { busy: boolean; onExecute: (mode: string) => void; onDismiss: () => void }) {
  return (
    <div className="anim-rise">
      <div className="min-w-0 flex-1 bg-surface border border-primary rounded-[14px] shadow-xs p-4">
        <div className="flex items-center gap-2 mb-1 text-primaryink">
          <Icon name="compass" size={16} />
          <span className="text-[12.5px] font-semibold">方案已就绪，如何继续？</span>
        </div>
        <div className="text-[12.5px] text-muted mb-3">选择执行模式后将退出计划模式并开始执行；也可先不执行、继续补充想法。</div>
        <div className="flex flex-wrap gap-2">
          <button
            onClick={() => onExecute('interactive')}
            disabled={busy}
            className="text-[13px] font-semibold text-white bg-primary px-3.5 py-2 rounded-[10px] cursor-pointer hover:brightness-105 disabled:opacity-40 disabled:cursor-default"
          >
            交互模式执行
          </button>
          <button
            onClick={() => onExecute('judge')}
            disabled={busy}
            className="text-[13px] font-semibold text-primaryink bg-primarysoft border border-primary px-3.5 py-2 rounded-[10px] cursor-pointer hover:brightness-105 disabled:opacity-40 disabled:cursor-default"
          >
            智能模式执行
          </button>
          <button
            onClick={onDismiss}
            disabled={busy}
            className="text-[13px] text-muted bg-surface2 border border-line2 px-3.5 py-2 rounded-[10px] cursor-pointer hover:text-ink disabled:opacity-40 disabled:cursor-default"
          >
            先不执行 / 继续补充
          </button>
        </div>
      </div>
    </div>
  )
}
