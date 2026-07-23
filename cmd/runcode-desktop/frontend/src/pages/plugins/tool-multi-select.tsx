// ToolMultiSelect 是子代理「工具」字段的多选下拉:从工具目录勾选,存回逗号分隔
// 串(与子代理 .md frontmatter 的 wire 格式一致);留空 = 继承全部工具。点选不
// 关闭面板,方便连续勾选;点外部收起。
import { useState } from 'react'
import { Icon } from '@/ui/icons'
import { FIELD_CLS } from '@/ui/fields'
import { Popover } from '@/ui/popover'
import { BUILTIN_TOOLS } from '@/core/tool-catalog'
import { type ToolInfo } from '@/core/bridge'

export function ToolMultiSelect({ value, options, onChange, disabled }: {
  value: string
  options: ToolInfo[]
  onChange: (next: string) => void
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const picked = value.split(',').map((s) => s.trim()).filter(Boolean)
  const toggle = (n: string) => {
    const next = picked.includes(n) ? picked.filter((p) => p !== n) : [...picked, n]
    onChange(next.join(', '))
  }
  return (
    <div className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        className={`${FIELD_CLS} w-full flex items-center text-left ${disabled ? '' : 'cursor-pointer hover:border-primary/60'} transition-colors`}
      >
        <span className={`flex-1 truncate ${picked.length ? 'font-mono' : 'text-faint'}`}>{picked.length ? picked.join(', ') : '继承全部工具'}</span>
        <Icon name="chevron-down" size={16} className="text-faint flex-none ml-2" />
      </button>
      <Popover open={open} onClose={() => setOpen(false)} placement="down-full" variant="panel" className="max-h-[300px]">
        <div className="overflow-y-auto py-1">
          <button
            type="button"
            onClick={() => onChange('')}
            className={`w-full text-left px-3.5 py-2 text-[12.5px] hover:bg-surface2 ${picked.length ? 'text-muted' : 'text-primary'}`}
          >
            继承全部工具{picked.length === 0 && ' ✓'}
          </button>
          {options.map((t) => {
            const zh = BUILTIN_TOOLS[t.name]
            const on = picked.includes(t.name)
            return (
              <button key={t.name} type="button" onClick={() => toggle(t.name)} className="w-full text-left px-3.5 py-2 flex items-center gap-2 hover:bg-surface2">
                <span className={`w-4 h-4 rounded border flex-none inline-flex items-center justify-center text-[10px] leading-none ${on ? 'bg-primary border-primary text-white' : 'border-line2 bg-surface'}`}>{on ? '✓' : ''}</span>
                <span className="text-[13px] text-ink flex-none">{zh?.label ?? t.name}</span>
                {zh && <span className="font-mono text-[11.5px] text-faint truncate">{t.name}</span>}
              </button>
            )
          })}
          {options.length === 0 && (
            <div className="px-3.5 py-6 text-center text-[12.5px] text-muted">工具目录为空(启动一个会话后可选);留空即继承全部工具</div>
          )}
        </div>
      </Popover>
    </div>
  )
}
