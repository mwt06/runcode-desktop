// Shared page scaffolding: the manager-page shell, placeholder rows, and the
// inset row used for settings/status strips.
//
// Each of these was previously written out per page, which is why the three
// manager pages drifted apart in padding and heading size despite being the same
// screen. Structure lives here; pages supply content only.
import { type ReactNode, type Ref } from 'react'

// Inset strip: a settings row, a status line, a "you're logged in as…" bar. The
// box styling is exported separately from the flex row because some call sites
// need the surface without the space-between layout (plain notice text).
export const INSET_BOX = 'rounded-field border border-line2 bg-surface2 px-3 py-2.5'

export function InsetRow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={`flex items-center justify-between ${INSET_BOX}${className ? ' ' + className : ''}`}>{children}</div>
}

// Placeholder is the centered muted line standing in for absent content —
// "加载中…", "还没有技能". `pad` is the only knob: 'md' inside a card or section,
// 'lg' when it owns a whole tab body and needs to sit optically centered.
export function Placeholder({ children, pad = 'md', className }: {
  children: ReactNode
  pad?: 'md' | 'lg'
  className?: string
}) {
  return (
    <div className={`text-center text-muted text-[13px] ${pad === 'lg' ? 'py-16' : 'py-6'}${className ? ' ' + className : ''}`}>
      {children}
    </div>
  )
}

// PageShell is the manager-page frame: a scrolling column, a centered measure, a
// heading with optional hint copy, and an optional action parked on the heading's
// right. `width` is the reading measure — settings runs narrower because it is a
// form, while MCP/memory hold prose and tables. It goes through inline style, not
// a max-w-[…] class: Tailwind scans source text for literal class names, so a
// runtime-built one would never be emitted.
//
// Deliberately not applied to the plugins and permissions pages: those have a
// pinned header with its own scroll body and a wider documentation measure, so
// forcing them through here would mean adding flags for every difference.
//
// `scrollRef` hands out the scrolling column itself, for pages that position
// content inside it (settings' in-page directory). `contentClassName` is appended
// to the measure only; the scrolling column's classes are not configurable.
//
// 页面在主栏的剩余高度内滚动，min-h-0 允许长内容收缩，overscroll-contain 隔离触底滚动。
// 这不能替代 App 的窗口边界：无就近定位父容器的绝对定位元素仍可能越出页面滚动区。
export function PageShell({ title, hint, action, width = 720, scrollRef, contentClassName, children }: {
  title: ReactNode
  hint?: ReactNode
  action?: ReactNode
  width?: number
  scrollRef?: Ref<HTMLDivElement>
  contentClassName?: string
  children: ReactNode
}) {
  return (
    <div ref={scrollRef} className="flex-1 min-h-0 overflow-y-auto overscroll-contain px-[22px] py-7">
      <div className={`mx-auto flex flex-col gap-5${contentClassName ? ' ' + contentClassName : ''}`} style={{ maxWidth: width }}>
        <div className={action ? 'flex items-start justify-between gap-3' : undefined}>
          <div className={action ? 'min-w-0' : undefined}>
            <h2 className="m-0 text-[20px] font-bold tracking-tight">{title}</h2>
            {hint && <p className="mt-1 text-muted text-[13px]">{hint}</p>}
          </div>
          {action && <div className="flex-none">{action}</div>}
        </div>
        {children}
      </div>
    </div>
  )
}
