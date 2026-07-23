// 输入框上方浮出的候选面板：@技能 / /子代理 / #文件 三种。三者共用同一个定位与
// 滚动外壳(PickerPanel)，只是行内容不同。选中项由父级持有(键盘 ↑↓ 要改它)。
import { type ReactNode, type RefObject } from 'react'
import { Icon } from '@/ui/icons'
import { SourceBadge } from '@/ui/badges'
import { classifyPreview, fileColor, kindIcon } from '@/preview/classify'
import { type AgentInfo, type SkillInfo } from '@/core/bridge'

function PickerPanel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="absolute left-6 right-6 bottom-full mb-1.5 z-10 bg-surface border border-line2 rounded-[12px] shadow-card overflow-hidden max-h-[300px] overflow-y-auto">
      <div className="px-3.5 pt-2 pb-1 text-[11.5px] text-faint">{title}</div>
      {children}
    </div>
  )
}

// rowCls 是三种候选行共用的选中/悬停底色。
const rowCls = (active: boolean) => `cursor-pointer ${active ? 'bg-primarysoft' : 'hover:bg-surface2'}`

export function SkillPicker({ items, sel, selRef, onHover, onPick }: {
  items: SkillInfo[]
  sel: number
  selRef: RefObject<HTMLDivElement>
  onHover: (i: number) => void
  onPick: (name: string) => void
}) {
  return (
    <PickerPanel title={`使用技能 · ${items.length}`}>
      {items.map((sk, i) => (
        <div
          key={sk.source + '/' + sk.name}
          ref={sel === i ? selRef : undefined}
          onMouseDown={(e) => { e.preventDefault(); onPick(sk.name) }}
          onMouseEnter={() => onHover(i)}
          className={`flex items-start gap-2.5 px-3.5 py-2 ${rowCls(sel === i)}`}
        >
          <span className="text-primaryink flex-none mt-px"><Icon name="book" size={15} /></span>
          <div className="min-w-0">
            <div className="text-[13px] text-ink flex items-center gap-1.5">
              {sk.name}
              <SourceBadge source={sk.source} />
            </div>
            <div className="text-[11.5px] text-faint truncate">{sk.description}</div>
          </div>
        </div>
      ))}
    </PickerPanel>
  )
}

export function AgentPicker({ items, sel, selRef, onHover, onPick }: {
  items: AgentInfo[]
  sel: number
  selRef: RefObject<HTMLDivElement>
  onHover: (i: number) => void
  onPick: (name: string) => void
}) {
  return (
    <PickerPanel title={`委派子代理 · ${items.length}`}>
      {items.map((ag, i) => (
        <div
          key={ag.source + '/' + ag.name}
          ref={sel === i ? selRef : undefined}
          onMouseDown={(e) => { e.preventDefault(); onPick(ag.name) }}
          onMouseEnter={() => onHover(i)}
          className={`flex items-start gap-2.5 px-3.5 py-2 ${rowCls(sel === i)}`}
        >
          <span className="text-primaryink flex-none mt-px"><Icon name="bot" size={15} /></span>
          <div className="min-w-0">
            <div className="text-[13px] text-ink flex items-center gap-1.5">
              {ag.name}
              <SourceBadge source={ag.source} />
            </div>
            <div className="text-[11.5px] text-faint truncate">{ag.description}</div>
          </div>
        </div>
      ))}
    </PickerPanel>
  )
}

export function FilePicker({ items, sel, selRef, onHover, onPick }: {
  items: string[]
  sel: number
  selRef: RefObject<HTMLDivElement>
  onHover: (i: number) => void
  onPick: (path: string) => void
}) {
  return (
    <PickerPanel title={`引用文件 · ${items.length}${items.length >= 50 ? '+' : ''}`}>
      {items.length === 0 && (
        <div className="px-3.5 py-2.5 text-[12.5px] text-faint">该工作区没有可引用的文件</div>
      )}
      {items.map((p, i) => {
        const slash = p.lastIndexOf('/')
        const name = slash >= 0 ? p.slice(slash + 1) : p
        const dir = slash >= 0 ? p.slice(0, slash) : ''
        return (
          <div
            key={p}
            ref={sel === i ? selRef : undefined}
            onMouseDown={(e) => { e.preventDefault(); onPick(p) }}
            onMouseEnter={() => onHover(i)}
            className={`flex items-center gap-2.5 px-3.5 py-1.5 ${rowCls(sel === i)}`}
          >
            <span className="w-6 h-6 rounded-[6px] flex-none bg-inset inline-flex items-center justify-center" style={{ color: fileColor(p) }}><Icon name={kindIcon(classifyPreview(p).kind)} size={14} /></span>
            <span className="text-[13px] text-ink flex-none">{name}</span>
            {dir && <span className="text-[11.5px] text-faint font-mono truncate min-w-0">{dir}</span>}
          </div>
        )
      })}
    </PickerPanel>
  )
}
