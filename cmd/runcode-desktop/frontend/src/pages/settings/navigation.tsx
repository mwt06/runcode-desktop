// 设置页目录的展示件：宽布局的左侧目录、窄布局的吸顶选择框，以及正文里的分类标题与
// 小节锚点包装。全部是纯展示 + 回调，不持有业务或登录状态；滚动与当前项归
// use-settings-navigation。
import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from 'react'
import { BTN } from '@/ui/tokens'
import { LABEL_CLS, SelectField } from '@/ui/fields'
import {
  sectionAnchorId,
  visibleSectionIds,
  type SettingsCategory,
  type SettingsSection,
  type SettingsSectionId,
} from './navigation-model'
import './settings.css'

export function SettingsNavigation({ categories, activeId, onNavigate, narrow, compactNavRef }: {
  categories: readonly SettingsCategory[]
  activeId: SettingsSectionId | null
  onNavigate: (id: SettingsSectionId) => void
  narrow: boolean
  compactNavRef: RefObject<HTMLDivElement>
}) {
  // 窄目录的待选项。放在这里而不是窄目录内部：宽窄来回切换时不丢。
  const [pending, setPending] = useState<SettingsSectionId | null>(null)
  if (narrow) {
    return (
      <CompactNavigation
        categories={categories}
        activeId={activeId}
        pending={pending}
        onPending={setPending}
        onNavigate={onNavigate}
        barRef={compactNavRef}
      />
    )
  }
  return <WideNavigation categories={categories} activeId={activeId} onNavigate={onNavigate} />
}

function WideNavigation({ categories, activeId, onNavigate }: {
  categories: readonly SettingsCategory[]
  activeId: SettingsSectionId | null
  onNavigate: (id: SettingsSectionId) => void
}) {
  const navRef = useRef<HTMLElement>(null)
  // 目录比视口高时它自己滚动；当前项变了只把目录自己滚到能看见当前项，不用
  // scrollIntoView——那会顺着祖先一路滚，把正文也带走。
  useEffect(() => {
    const nav = navRef.current
    if (!nav || nav.scrollHeight <= nav.clientHeight) return
    const item = nav.querySelector<HTMLElement>('[aria-current="location"]')
    if (!item) return
    const box = nav.getBoundingClientRect()
    const at = item.getBoundingClientRect()
    if (at.top < box.top) nav.scrollTop -= box.top - at.top
    else if (at.bottom > box.bottom) nav.scrollTop += at.bottom - box.bottom
  }, [activeId])

  return (
    <nav ref={navRef} aria-label="设置目录" className="settings-nav">
      <ul className="m-0 p-0 list-none flex flex-col gap-2">
        {categories.map((c) => (
          <li key={c.id}>
            <button
              type="button"
              className="settings-nav-item w-full text-left rounded-lg px-2.5 py-1.5 text-[13px] font-semibold text-ink hover:bg-surface2"
              onClick={() => onNavigate(c.sections[0].id)}
            >
              {c.title}
            </button>
            <ul className="m-0 p-0 list-none flex flex-col gap-0.5 mt-0.5">
              {c.sections.map((s) => {
                const active = s.id === activeId
                return (
                  <li key={s.id}>
                    <button
                      type="button"
                      aria-current={active ? 'location' : undefined}
                      className={`settings-nav-item w-full text-left rounded-lg border-l-2 pl-5 pr-2.5 py-1.5 text-[13px] ${
                        active
                          ? 'border-primary bg-primarysoft text-primaryink font-semibold'
                          : 'border-transparent text-ink2 hover:bg-surface2'
                      }`}
                      onClick={() => onNavigate(s.id)}
                    >
                      {s.title}
                    </button>
                  </li>
                )
              })}
            </ul>
          </li>
        ))}
      </ul>
    </nav>
  )
}

// 窄目录：选择只改待选项，按「前往」才定位。这样方向键能连续挑选，焦点不会每换
// 一项就被拽走。没有待选时选择框跟随当前项；待选项被隐藏了就回落到当前项。
function CompactNavigation({ categories, activeId, pending, onPending, onNavigate, barRef }: {
  categories: readonly SettingsCategory[]
  activeId: SettingsSectionId | null
  pending: SettingsSectionId | null
  onPending: (id: SettingsSectionId | null) => void
  onNavigate: (id: SettingsSectionId) => void
  barRef: RefObject<HTMLDivElement>
}) {
  const visible = visibleSectionIds(categories)
  const choice = pending && visible.includes(pending) ? pending : activeId
  const currentTitle = categories.flatMap((c) => c.sections).find((s) => s.id === activeId)?.title ?? '—'
  function go() {
    onPending(null)
    if (choice) onNavigate(choice)
  }
  return (
    <nav ref={barRef} aria-label="设置目录" className="settings-compact-nav">
      <div className="flex items-end gap-2">
        <label className={`${LABEL_CLS} flex-1 min-w-0`}>
          设置目录
          <SelectField value={choice ?? ''} onChange={(v) => onPending(v as SettingsSectionId)}>
            {categories.map((c) => (
              <optgroup key={c.id} label={c.title}>
                {c.sections.map((s) => <option key={s.id} value={s.id}>{s.title}</option>)}
              </optgroup>
            ))}
          </SelectField>
        </label>
        <button type="button" className={`${BTN} settings-nav-item px-5 py-2.5 flex-none`} onClick={go}>前往</button>
      </div>
      <div className="mt-1.5 text-[12px] text-muted truncate">当前：{currentTitle}</div>
    </nav>
  )
}

// 正文里的分类：一个清晰的大类标题 + 它的小节。
export function SettingsGroup({ category, children }: { category: SettingsCategory; children: ReactNode }) {
  const headingId = `settings-group-${category.id}`
  return (
    <div role="group" aria-labelledby={headingId} className="settings-group">
      <h3 id={headingId} className="m-0 text-[15px] font-bold text-ink tracking-tight">{category.title}</h3>
      {children}
    </div>
  )
}

// 小节锚点包装：目录跳转的落点与焦点目标（tabIndex=-1，名称与目录一致）。原小节组件
// 原样放在里面，不需要知道导航存在。hidden 只藏包装、不卸载里面的组件——上下文审核
// 的能力探测就靠它在隐藏时继续挂着。
export function SettingsAnchor({ section, register, hidden, children }: {
  section: SettingsSection
  register: (id: SettingsSectionId, node: HTMLElement | null) => void
  hidden?: boolean
  children: ReactNode
}) {
  const { id, title } = section
  const ref = useCallback((node: HTMLElement | null) => register(id, node), [register, id])
  return (
    <section ref={ref} id={sectionAnchorId(id)} tabIndex={-1} aria-label={title} hidden={hidden} className="settings-section">
      {children}
    </section>
  )
}
