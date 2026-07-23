import { Icon } from '@/ui/icons'

// 预览面板标题栏上的小圆角图标按钮（刷新 / 外部打开 / 复制路径 / 关闭）。
export function IconBtn({ name, title, onClick }: { name: string; title: string; onClick: () => void }) {
  return (
    <button title={title} onClick={onClick} className="flex-none w-7 h-7 flex items-center justify-center rounded-md text-muted hover:text-ink hover:bg-surface2">
      <Icon name={name} size={14} />
    </button>
  )
}
