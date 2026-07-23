import { type ReactNode } from 'react'

// 设置页统一的小节卡片外壳：标题(可带右侧灰色提示)+ 内容。
export function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="bg-surface border border-line2 rounded-[14px] p-5 flex flex-col gap-[13px] shadow-xs">
      {hint ? (
        <div className="flex items-center justify-between">
          <div className="text-[13px] font-semibold text-ink">{title}</div>
          <span className="text-[11.5px] text-faint">{hint}</span>
        </div>
      ) : (
        <div className="text-[13px] font-semibold text-ink">{title}</div>
      )}
      {children}
    </section>
  )
}
