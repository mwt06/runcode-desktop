// 技能市场：平台上架的技能，装到本地（全局或本项目）。
//
// 它是左栏里的一个**独立页面**，不是插件页的一个页签——装东西和管东西是两件事：
// 市场要宽版心、密排、按分类扫；插件页是一行一个开关的管理列表。挤在一起时两边的
// 版心和顶部控件互相将就，谁都不舒服。装完的技能出现在「插件 → 技能」里管理。
//
// 布局照设计稿：顶上一排分类页签，下面是密排的卡片网格——图标 + 名称 + 两行描述 +
// 右侧一颗安装按钮。密排是有理由的：这个市场一次要摆几十上百条，卡片做大了一屏
// 看不到几个，而用户在这里做的事是**扫**，不是读。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Icon } from '@/ui/icons'
import { InlineError } from '@/ui/feedback'
import { ConfirmDialog } from '@/ui/confirm-dialog'
import { Popover } from '@/ui/popover'
import { Placeholder } from '@/ui/layout'
import {
  deleteSkill, errText, installMarketSkill, skillMarket,
  type MarketSkill, type SkillMarketPage,
} from '@/core/bridge'
import { MARKET_PAGE_SIZE, avatarClass, avatarText, clampPage, filterMarket, pageCount, pageSlice } from './filter'

/** SCOPES 是两个安装去处。文案与插件页的「导入」菜单一致——同一件事只该有一种说法。 */
const SCOPES = [
  { k: 'project', label: '本项目', hint: '仅当前工作区可用' },
  { k: 'user', label: '全局(用户级)', hint: '所有项目都可用' },
] as const

type Scope = (typeof SCOPES)[number]['k']

export function MarketPage() {
  const [page, setPage] = useState<SkillMarketPage | null>(null)
  const [cat, setCat] = useState('')
  const [query, setQuery] = useState('')
  const [pageNo, setPageNo] = useState(1)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  // busy 是正在装/卸的那一条的 id。按 id 记而不是一个布尔：装一个的时候别的卡片
  // 照样能点，只有自己那颗按钮转圈。
  const [busy, setBusy] = useState(0)
  // 展开着作用域菜单的那一条的 id。同样按 id 记：一次只开一个菜单。
  const [menu, setMenu] = useState(0)
  // 待确认卸载的那一条 + 从哪个作用域卸。卸载会把整个技能目录删掉，不能一点就没。
  const [confirmDel, setConfirmDel] = useState<{ skill: MarketSkill; scope: Scope } | null>(null)

  const load = useCallback(async (refresh: boolean) => {
    setLoading(true)
    setErr('')
    try {
      setPage(await skillMarket(refresh))
    } catch (e) {
      setErr(errText(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load(false) }, [load])

  const act = async (s: MarketSkill, run: () => Promise<unknown>) => {
    setBusy(s.id)
    setMenu(0)
    setErr('')
    try {
      await run()
      // 回读一次清单（走缓存，不打网络）：「已装」是后端按本地目录算的，装完/卸完
      // 必须重算，否则那颗按钮要等缓存过期才变。
      setPage(await skillMarket(false))
    } catch (e) {
      setErr(errText(e))
    } finally {
      setBusy(0)
    }
  }

  const all = useMemo(() => page?.skills ?? [], [page])
  const shown = useMemo(() => filterMarket(all, cat, query), [all, cat, query])
  const cats = page?.categories ?? []
  // 页码是独立状态，而筛选会让总页数缩水——夹一下，否则筛完停在越界页上就是一片
  // 空白（有结果，只是不在这一页）。
  const current = clampPage(pageNo, shown.length)
  const pages = pageCount(shown.length)
  const visible = pageSlice(shown, current)
  // 换分类/改搜索词都从第一页看起。写成 effect 而不是塞进每个 setState：搜索框、
  // 分类页签、刷新按钮都会改动筛选结果，漏掉任何一处都是同一个 bug。
  useEffect(() => { setPageNo(1) }, [cat, query])

  return (
    <div className="flex-1 flex flex-col min-h-0">
      <div className="flex-none px-10 pt-9 pb-5 border-b border-line">
        <div className="max-w-[1400px] mx-auto">
          <h2 className="m-0 text-[22px] font-bold tracking-tight">技能市场</h2>
          <p className="mt-1.5 text-muted text-[13px]">平台上架的技能 · 可装到「本项目」或「全局(用户级)」 · 装完在「插件 → 技能」里管理</p>
          <div className="flex items-center gap-3 mt-5">
            <div className="relative">
              <span className="absolute left-3 top-1/2 -translate-y-1/2 text-faint pointer-events-none"><Icon name="search" size={14} /></span>
              <input
                className="w-[240px] font-sans text-[13px] bg-surface2 text-ink border border-line2 rounded-btn pl-9 pr-3 py-2 outline-none focus:border-primary"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="搜索技能"
              />
            </div>
            <button
              type="button"
              onClick={() => void load(true)}
              disabled={loading}
              title="重新拉取市场清单"
              className="ml-auto text-[12px] text-muted hover:text-ink inline-flex items-center gap-1.5 px-2 py-1 rounded-field hover:bg-surface2 disabled:opacity-40"
            >
              <Icon name="refresh" size={13} />{loading ? '加载中…' : '刷新'}
            </button>
          </div>
          {err && <InlineError variant="banner" className="mt-4">{err}</InlineError>}
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-10 py-5">
        <div className="max-w-[1400px] mx-auto">
        <div className="flex items-center gap-2 flex-wrap">
          <Chip on={!cat} onClick={() => setCat('')}>全部</Chip>
          {cats.map((c) => <Chip key={c} on={cat === c} onClick={() => setCat(c)}>{c}</Chip>)}
        </div>

        {visible.length > 0 && (
          <div className="mt-4 grid gap-2.5 grid-cols-[repeat(auto-fill,minmax(330px,1fr))]">
            {visible.map((s) => (
              <Card
                key={s.id}
                skill={s}
                busy={busy === s.id}
                menu={menu === s.id}
                onMenu={(open) => setMenu(open ? s.id : 0)}
                onInstall={(scope) => void act(s, () => installMarketSkill(s.id, scope))}
                onUninstall={(scope) => { setMenu(0); setConfirmDel({ skill: s, scope }) }}
              />
            ))}
          </div>
        )}
        {visible.length === 0 && (
          <Placeholder pad="lg">
            {loading ? '正在拉取市场清单…'
              : err ? '市场暂时打不开'
              : all.length === 0 ? '市场里还没有上架的技能'
              : '没有匹配的技能'}
          </Placeholder>
        )}

        {shown.length > 0 && (
          <Pager page={current} pages={pages} total={shown.length} onGo={setPageNo} />
        )}
        </div>
      </div>
      {confirmDel && (
        <ConfirmDialog
          title="卸载技能"
          message={<>确定从<b className="text-ink font-semibold">{scopeLabel(confirmDel.scope)}</b>卸载「<b className="text-ink font-semibold">{confirmDel.skill.displayName || confirmDel.skill.name}</b>」？该目录下的整个技能文件夹会被删掉；市场里还在，随时可以再装回来。</>}
          confirmLabel="卸载"
          onConfirm={() => {
            const { skill, scope } = confirmDel
            setConfirmDel(null)
            void act(skill, () => deleteSkill(skill.name, scope))
          }}
          onCancel={() => setConfirmDel(null)}
        />
      )}
    </div>
  )
}

function scopeLabel(scope: Scope): string {
  return SCOPES.find((s) => s.k === scope)?.label ?? scope
}

function Chip({ on, onClick, children }: { on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`px-3 py-1 rounded-field text-[13px] transition ${on ? 'bg-surface2 text-ink font-medium' : 'text-muted hover:text-ink hover:bg-surface2'}`}
    >
      {children}
    </button>
  )
}

// Pager 是底部翻页条。只画「上一页/第 n 页/下一页」，不铺页码按钮：这个市场的页数
// 随上架量走，铺开的页码条要么撑爆一行要么得再做省略号，而这里没人是奔着"第 7 页"
// 去的——翻页是浏览的节奏，不是寻址。
function Pager({ page, pages, total, onGo }: { page: number; pages: number; total: number; onGo: (n: number) => void }) {
  const btn = 'px-2.5 py-1 rounded-field text-[12px] text-muted hover:text-ink hover:bg-surface2 transition disabled:opacity-35 disabled:hover:bg-transparent disabled:hover:text-muted'
  return (
    <div className="mt-5 flex items-center gap-2 text-[12px] text-faint">
      <span>共 {total} 个技能{pages > 1 ? ` · 每页 ${MARKET_PAGE_SIZE} 个` : ''}</span>
      {pages > 1 && (
        <span className="ml-auto inline-flex items-center gap-1">
          <button type="button" className={btn} disabled={page <= 1} onClick={() => onGo(page - 1)}>上一页</button>
          <span className="text-muted tabular-nums px-1">{page} / {pages}</span>
          <button type="button" className={btn} disabled={page >= pages} onClick={() => onGo(page + 1)}>下一页</button>
        </span>
      )}
    </div>
  )
}

// Card 是市场里的一条。整张卡不可点——这里没有详情页，唯一的动作就是右侧那颗按钮。
//
// 那颗按钮统一是**菜单**，不分"没装时是安装、装了变成卸载"两种控件：两个作用域可以
// 各自处于装/未装的任一状态（全局装了、本项目没装，反之亦然，或者都装了），所以
// 菜单里逐条列出每个作用域此刻能做的那件事。装/卸是同一颗按钮同一个菜单，位置不跳。
function Card({ skill, busy, menu, onMenu, onInstall, onUninstall }: {
  skill: MarketSkill
  busy: boolean
  menu: boolean
  onMenu: (open: boolean) => void
  onInstall: (scope: Scope) => void
  onUninstall: (scope: Scope) => void
}) {
  const label = skill.displayName || skill.name
  const at = (s: Scope) => (s === 'user' ? skill.installedUser : skill.installedProject)
  const installedAt = SCOPES.filter((s) => at(s.k))
  const anyInstalled = installedAt.length > 0
  return (
    <div className="bg-surface border border-line2 rounded-card px-3.5 py-3 flex items-start gap-3 hover:border-primary hover:shadow-xs transition">
      <span className={`w-8 h-8 rounded-btn flex-none flex items-center justify-center text-[14px] font-semibold ${avatarClass(skill.name)}`}>
        {avatarText(label)}
      </span>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-[13px] font-medium text-ink truncate">{label}</span>
          {!skill.hasBundle && (
            // 纯提示词型：装下来只有一个 SKILL.md，没有随附脚本与参考文件。
            <span className="flex-none text-[11px] text-faint border border-line2 rounded px-1 py-px">提示词</span>
          )}
        </div>
        <div className="mt-1 text-[12px] text-muted leading-[1.5] line-clamp-2" title={skill.description}>
          {skill.description || '（没有描述）'}
        </div>
      </div>
      <div className="flex-none relative flex items-center gap-1.5">
        {anyInstalled && (
          // 「已装」是状态、装卸是动作，各占各的位置——不做成"点『已装』就删掉"那种
          // 标签与后果对不上的按钮。装在哪儿也直接写出来，因为这决定了它在别的项目
          // 里还在不在。
          <span className="text-[12px] text-green" title={`已装在${installedAt.map((s) => s.label).join(' 和 ')}`}>
            已装{installedAt.length === 1 ? `·${installedAt[0].label === '全局(用户级)' ? '全局' : '本项目'}` : ''}
          </span>
        )}
        <button
          type="button"
          onClick={() => onMenu(!menu)}
          disabled={busy}
          title={anyInstalled ? `管理「${label}」的安装` : `安装「${label}」`}
          className={`w-7 h-7 rounded-btn inline-flex items-center justify-center transition disabled:opacity-40 ${anyInstalled ? 'text-faint/70 hover:text-ink hover:bg-surface2' : 'border border-line2 text-muted hover:text-primary hover:border-primary'}`}
        >
          {busy ? <span className="text-[12px] leading-none">…</span> : <Icon name={anyInstalled ? 'more' : 'plus'} size={anyInstalled ? 15 : 14} />}
        </button>
        <Popover open={menu} onClose={() => onMenu(false)} placement="down-right" className="w-[230px]">
          {SCOPES.map((s) => (
            <div
              key={s.k}
              onClick={() => (at(s.k) ? onUninstall(s.k) : onInstall(s.k))}
              className="px-3.5 py-2 cursor-pointer hover:bg-surface2"
            >
              <div className={`text-[13px] ${at(s.k) ? 'text-red' : 'text-ink'}`}>
                {at(s.k) ? `从${s.label}卸载` : `安装到${s.label}`}
              </div>
              <div className="text-[12px] text-faint mt-0.5">{at(s.k) ? '删掉这个目录下的整个技能文件夹' : s.hint}</div>
            </div>
          ))}
        </Popover>
      </div>
    </div>
  )
}
