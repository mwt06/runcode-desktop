// 设置页目录的展示件：宽布局的左侧目录、窄布局的吸顶选择框，以及正文里的分类标题与
// 小节锚点包装。分类标题负责切换右侧分类内容，三级项始终展开；全部是纯展示 + 回调，
// 不持有业务或登录状态，滚动与当前项归 use-settings-navigation。
import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from 'react'
import { BTN } from '@/ui/tokens'
import { LABEL_CLS, SelectField } from '@/ui/fields'
import { Icon } from '@/ui/icons'
import {
  sectionAnchorId,
  visibleSectionIds,
  type SettingsCategory,
  type SettingsSection,
  type SettingsSectionId,
} from './navigation-model'
import { SettingsSearch } from './settings-search'
import './settings.css'

// 每个设置项使用稳定的语义图标；目录项由能力开关动态过滤，图标本身不参与业务判断。
const SECTION_ICONS: Record<SettingsSectionId, string> = {
  passport: 'bot',
  login: 'logout',
  mail: 'send',
  session: 'chat',
  'custom-models': 'plug',
  'model-context': 'settings',
  vision: 'eye',
  'image-generation': 'image-gen',
  recorder: 'mic',
  proxy: 'globe',
  runtimes: 'terminal',
  'context-audit': 'shield',
  updates: 'refresh',
  workspace: 'folder',
}

export function SettingsNavigation({
  categories,
  activeId,
  onNavigate,
  narrow,
  compactNavRef,
}: {
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
  return (
    <WideNavigation
      categories={categories}
      activeId={activeId}
      onNavigate={onNavigate}
    />
  )
}

function WideNavigation({
  categories,
  activeId,
  onNavigate,
}: {
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
    if (!item || item.closest('[hidden]')) return
    const box = nav.getBoundingClientRect()
    const at = item.getBoundingClientRect()
    if (at.top < box.top) nav.scrollTop -= box.top - at.top
    else if (at.bottom > box.bottom) nav.scrollTop += at.bottom - box.bottom
  }, [activeId])

  return (
    <nav ref={navRef} aria-label="设置目录" className="settings-nav">
      <SettingsSearch categories={categories} onSelect={onNavigate} />
      <ul className="settings-nav-groups">
        {categories.map((c) => {
          const activeCategory = c.sections.some((section) => section.id === activeId)
          const listId = `settings-category-${c.id}-sections`
          return (
          <li key={c.id} className="settings-nav-group">
            <button
              type="button"
              className={`settings-nav-category${activeCategory ? ' settings-nav-category-active' : ''}`}
              onClick={() => {
                // 分类标题不是折叠开关：每次点击都打开完整分类，并定位到首项。
                const first = c.sections[0]
                if (first) onNavigate(first.id)
              }}
            >
              <span>{c.title}</span>
            </button>
            <ul id={listId} className="settings-nav-subitems">
              {c.sections.map((s) => {
                const active = s.id === activeId
                return (
                  <li key={s.id}>
                    <button
                      type="button"
                      aria-current={active ? 'location' : undefined}
                      className={`settings-nav-item${active ? ' settings-nav-item-active' : ''}`}
                      onClick={() => onNavigate(s.id)}
                    >
                      <Icon name={SECTION_ICONS[s.id]} size={15} className="settings-nav-item-icon" />
                      <span>{s.title}</span>
                    </button>
                  </li>
                )
              })}
            </ul>
          </li>
          )
        })}
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
      <SettingsSearch categories={categories} onSelect={onNavigate} />
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

// 正文分组始终挂载，只隐藏非当前组，保住账号协调器、能力探测和表单草稿。
export function SettingsGroup({ category, hidden, children }: {
  category: SettingsCategory
  hidden?: boolean
  children: ReactNode
}) {
  return (
    <div role="group" aria-label={category.title} className="settings-group" hidden={hidden}>
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
