// 输入框上方那排快捷技能。
//
// 它是「录音纪要」唯一的入口：录音不是一句话能说清的指令，让用户去输入框里打字
// 触发是不合适的——点一下就该开始录，这是设计稿把它放在这里的原因。
//
// 设计稿里还有帮我写作 / 深度研究 / 幻灯片 / 数据分析及可视化。那几项各自是独立
// 功能，还没有对应实现；这里只登记做得出来的那些。摆一排点了没反应的按钮，比
// 少几个按钮糟糕得多。
import { Icon } from '@/ui/icons'

export interface QuickSkill {
  id: string
  label: string
  icon: string
  title?: string
  disabled?: boolean
  onPick: () => void
}

export function QuickSkills({ items }: { items: QuickSkill[] }) {
  if (items.length === 0) return null
  return (
    <div className="flex flex-wrap items-center gap-2 mb-2.5">
      {items.map((s) => (
        <button
          key={s.id}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full border border-line2 bg-surface text-[13px] text-ink transition hover:border-primary hover:text-primaryink disabled:opacity-45 disabled:cursor-default disabled:hover:border-line2 disabled:hover:text-ink"
          onClick={s.onPick}
          disabled={s.disabled}
          title={s.title}
        >
          <Icon name={s.icon} size={14} />
          {s.label}
        </button>
      ))}
    </div>
  )
}
