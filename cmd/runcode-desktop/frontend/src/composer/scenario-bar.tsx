// 输入框上方那排内置场景：一级是分类，点开是这个分类下的常用任务。
//
// 它取代了原来那排「快捷技能」——那时那里只有「录音纪要」一个按钮，而录音纪要本来
// 就是场景表里的一个分类。两者并排会让同一个东西在同一行里出现两次。
//
// 分两级而不是把 45 条铺开：铺开要占掉输入框上方一大片，而用户在这里是**先想清楚
// 要干哪类事**，再挑具体那条。二级面板复用 mention-picker 的 PickerPanel——浮在输入
// 区正上方、占满输入区宽度，所以不管点的是最左还是最右那个分类，面板都在原地展开，
// 不会被窗口边缘挤出去。
import { Icon } from '@/ui/icons'
import { HScroll } from '@/ui/h-scroll'
import { BRAND } from '@/core/brand'
import { PickerPanel } from './mention-picker'
import { BUILTIN_CATEGORY, type Scenario, type ScenarioCategory } from '@/core/scenarios'

// MASCOT_CLEARANCE 是右侧给吉祥物让开的宽度（智开的插画 216x144，按 height:64 渲染
// 出来 96px，再留点余量）。
//
// 插画是绝对定位在输入框正上方的（bottom-full right-0.5，见 mascot.tsx），正好压在
// 这一行右端。不让开的话最右边那个分类会被盖住——而且是**点不到**，不只是看不见：
// 插画那层要接 mouseenter 触发重播，不能设 pointer-events:none。
//
// 只有配了 composerMark 的品牌才有插画（XRUN 没有），所以这段留白也跟着品牌走，
// 不能写死——否则原品牌右边会平白空出一块。
const MASCOT_CLEARANCE = 104

/** BuiltinAction 是一个「点了直接执行」的分类（目前只有录音纪要）。 */
export interface BuiltinAction {
  onPick: () => void
  disabled?: boolean
  title?: string
}

export function ScenarioBar({ categories, openId, onToggle, builtins }: {
  categories: ScenarioCategory[]
  /** 当前展开的分类 id；'' 表示都没展开。 */
  openId: string
  onToggle: (id: string) => void
  /** 内置功能分类的动作，按 BUILTIN_CATEGORY 的值取。 */
  builtins: Record<string, BuiltinAction>
}) {
  if (categories.length === 0) return null
  return (
    // 一行不折行，超出的横向滚动看（HScroll：藏掉滚动条，两端画出淡出与翻页箭头）。
    // 折行的问题是这一行会随窗口宽度在 1~3 行之间跳，输入框跟着上下弹。
    <HScroll
      className="mb-2.5"
      rowClassName="gap-2"
      // 用 margin 而不是 padding 让开插画：padding 只在滚到底时才把内容顶开，滚动
      // 途中 chip 照样会从插画底下经过、和它叠在一起。margin 是把**可视轨道本身**
      // 缩短，chip 到那个边界就被裁掉，任何滚动位置都不会跑到插画上（右侧的翻页
      // 箭头也贴在这个缩短后的边界上，同样不会压到插画）。
      style={{ marginRight: BRAND.composerMark ? MASCOT_CLEARANCE : undefined }}
    >
      {categories.map((c) => {
        const builtin = builtins[BUILTIN_CATEGORY[c.id] ?? '']
        const on = openId === c.id
        return (
          <button
            key={c.id}
            type="button"
            // 内置功能分类点了直接执行，不展开二级：录音是点一下就该开始的动作，
            // 中间插一层「请选择」纯属多余。
            onClick={() => (builtin ? builtin.onPick() : onToggle(c.id))}
            disabled={builtin?.disabled}
            title={builtin?.title ?? `${c.name}（${c.items.length} 个场景）`}
            className={`flex-none inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full border text-[13px] whitespace-nowrap transition disabled:opacity-45 disabled:cursor-default ${
              on
                ? 'border-primary bg-primarysoft text-primaryink'
                : 'border-line2 bg-surface text-ink hover:border-primary hover:text-primaryink disabled:hover:border-line2 disabled:hover:text-ink'
            }`}
          >
            <Icon name={c.icon} size={14} />
            {c.name}
          </button>
        )
      })}
    </HScroll>
  )
}

export function ScenarioPanel({ category, onPick }: {
  category: ScenarioCategory
  onPick: (s: Scenario) => void
}) {
  return (
    <PickerPanel title={`${category.name} · ${category.items.length}`}>
      {category.items.map((s) => (
        <div
          key={s.id}
          // onMouseDown + preventDefault：输入框此刻可能是聚焦的，用 onClick 会先失焦
          // 再触发，选中占位符的那一步就落空了。三个 mention 选择器同理。
          onMouseDown={(e) => { e.preventDefault(); onPick(s) }}
          className="flex items-start gap-2.5 px-3.5 py-2 cursor-pointer hover:bg-surface2"
        >
          <span className="text-primaryink flex-none mt-px"><Icon name="sparkles" size={15} /></span>
          <div className="min-w-0">
            <div className="text-[13px] text-ink">{s.name}</div>
            {/* 描述只截两行：面板高度有上限，而这些描述长短悬殊（有的一句，有的
                三行），不截会让一个分类把整块撑满、后面几条要滚动才看得到。 */}
            <div className="text-[12px] text-faint leading-[1.5] line-clamp-2">{s.blurb}</div>
          </div>
        </div>
      ))}
    </PickerPanel>
  )
}
