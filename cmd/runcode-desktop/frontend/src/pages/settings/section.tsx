import { type ReactNode } from 'react'

// 设置页统一的小节卡片外壳；提示在窄宽下可以换行，不能把卡片撑出横向滚动。
export function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="settings-section-card bg-surface border border-line2 rounded-card p-5 flex flex-col gap-[13px] shadow-xs">
      {hint ? (
        <div className="settings-section-heading">
          <div className="text-[15px] font-semibold text-ink">{title}</div>
          <span className="text-[12px] text-muted">{hint}</span>
        </div>
      ) : (
        <div className="text-[15px] font-semibold text-ink">{title}</div>
      )}
      {children}
    </section>
  )
}
