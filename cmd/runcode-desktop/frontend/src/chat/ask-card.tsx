// AskUser 工具的提问卡：对话流里就地交互，答完即定格。
// （计划模式的「怎么继续」已由阶段化审批板取代，见 chat/plan-board。）
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
