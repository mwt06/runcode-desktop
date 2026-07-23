// ThinkingPanel shows the model's streamed reasoning ("chain of thought") in a
// collapsible panel above the answer. It auto-expands while the model is actively
// thinking (before any answer text) and auto-collapses once the answer begins; a
// manual toggle takes over from that point.
import { useEffect, useRef, useState } from 'react'
import { Icon } from '@/ui/icons'
import { useStickToBottom } from '@/hooks/use-stick-to-bottom'

export function ThinkingPanel({ text, streaming }: { text: string; streaming: boolean }) {
  const [open, setOpen] = useState(streaming)
  const userToggled = useRef(false)
  useEffect(() => {
    if (!userToggled.current) setOpen(streaming)
  }, [streaming])
  const scroll = useStickToBottom(open && streaming ? text.length : null)
  // Compact, dimmed disclosure (Claude-style): a single quiet line that opens into
  // the reasoning as indented, muted text with a left rule — not a boxed panel.
  return (
    <div>
      <button
        onClick={() => {
          userToggled.current = true
          setOpen((v) => !v)
        }}
        className="inline-flex items-center gap-1.5 text-[12.5px] text-faint hover:text-muted transition cursor-pointer select-none"
      >
        <span className={`flex-none${streaming ? ' animate-pulse' : ''}`}>
          <Icon name="sparkles" size={13} />
        </span>
        <span>{streaming ? '正在思考…' : '思考过程'}</span>
        <Icon name="chevron-down" size={12} className={`flex-none transition-transform${open ? ' rotate-180' : ''}`} />
      </button>
      {open && (
        <div
          ref={streaming ? scroll.ref : undefined}
          onScroll={streaming ? scroll.onScroll : undefined}
          className={`mt-1.5 text-[13px] leading-[1.7] text-faint whitespace-pre-wrap break-words${streaming ? ' max-h-[34vh] overflow-y-auto' : ''}`}
        >
          {text}
          {streaming && <span className="caret">▍</span>}
        </div>
      )}
    </div>
  )
}
