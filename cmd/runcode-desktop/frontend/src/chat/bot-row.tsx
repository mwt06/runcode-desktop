import { type ReactNode } from 'react'

// BotRow wraps an assistant-side entry (message, tool card, notice). The assistant
// output is left-aligned and flows naturally, while user messages are right-aligned
// flat blocks.
export function BotRow({ children }: { children: ReactNode }) {
  return <div className="anim-rise min-w-0">{children}</div>
}
