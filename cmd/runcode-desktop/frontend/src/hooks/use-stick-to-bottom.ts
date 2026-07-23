import { useEffect, useRef } from 'react'

// useStickToBottom keeps a scroll container pinned to its newest content while it
// streams, unless the user has scrolled up to read — then it leaves them be. `dep`
// changes (growing text/output) trigger the re-pin.
//
// Shared by every streaming surface: the conversation flow, the assistant answer
// window, the thinking panel, a tool's output pane and the sub-agent detail view.
export function useStickToBottom<T extends HTMLElement = HTMLDivElement>(dep: unknown) {
  const ref = useRef<T>(null)
  const stick = useRef(true)
  useEffect(() => {
    const el = ref.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [dep])
  const onScroll = () => {
    const el = ref.current
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48
  }
  return { ref, onScroll }
}
