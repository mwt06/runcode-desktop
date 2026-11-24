import { useState } from 'react'
import { Icon } from '@/ui/icons'
import { Popover } from '@/ui/popover'

// 静态入口。只放已经能打开的项；未实现的栏目不出现在菜单里。
// automation 保持原位；mail 追加在末尾，不用通用插件框架。
export const MORE_MENU_ITEMS = [
  { id: 'automation', label: '自动化任务', icon: 'clock' },
  { id: 'mail', label: '邮箱与通信录', icon: 'at' },
] as const

export function MoreMenu({ collapsed, onOpenAutomation, onOpenMail }: {
  collapsed?: boolean
  onOpenAutomation: () => void
  onOpenMail: () => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="relative">
      <button
        type="button"
        aria-expanded={open}
        aria-haspopup="menu"
        title={collapsed ? '更多' : undefined}
        onClick={() => setOpen((value) => !value)}
        className={`w-full flex items-center py-[9px] rounded-field text-sm cursor-pointer select-none text-muted hover:bg-surface2 hover:text-ink ${
          collapsed ? 'justify-center' : 'gap-[11px] px-[11px]'
        }`}
      >
        <Icon name="more-grid" size={18} />
        {!collapsed && '更多'}
      </button>
      <Popover open={open} onClose={() => setOpen(false)} placement="down-left" variant="menu" className="min-w-[196px]">
        <div role="menu">
          {MORE_MENU_ITEMS.map((item) => (
            <button
              key={item.id}
              type="button"
              role="menuitem"
              className="w-full text-left px-3 py-2 flex items-center gap-2 text-[14px] text-ink hover:bg-surface2"
              onClick={() => {
                setOpen(false)
                if (item.id === 'automation') onOpenAutomation()
                else if (item.id === 'mail') onOpenMail()
              }}
            >
              <Icon name={item.icon} size={16} />
              {item.label}
            </button>
          ))}
        </div>
      </Popover>
    </div>
  )
}
