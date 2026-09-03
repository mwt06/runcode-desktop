// 技能市场的筛选规则。纯函数，与 React 无关，单独测。
import { type MarketSkill } from '@/core/bridge'

/**
 * matchMarket 判断一条市场技能是否命中搜索词。
 *
 * 展示名、真实 name、描述、分类都算：这个市场里展示名是中文而 name 是 kebab-case
 * 的英文，用户记得住的可能是任何一个（"docx" 和 "文档" 该找到同一条）。
 */
export function matchMarket(s: MarketSkill, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return [s.displayName, s.name, s.description, s.category]
    .some((f) => (f ?? '').toLowerCase().includes(q))
}

/**
 * filterMarket 按分类 + 搜索词筛出要画的那些。
 *
 * **不重排**。装完一个技能之后把它挪到别处，会让刚点过的那张卡片在眼皮底下跳走——
 * 用户的下一次点击就落到了另一条上。顺序始终是服务端给的顺序，安装只改卡片上的
 * 那颗按钮。
 */
export function filterMarket(skills: MarketSkill[], category: string, query: string): MarketSkill[] {
  return skills.filter((s) => (!category || s.category === category) && matchMarket(s, query))
}

// AVATAR 是首字方块的四组配色（都取自设计 token）。市场接口没有图标字段，
// 而一排纯灰方块认不出谁是谁——按名字散列取色，同一个技能每次都是同一个颜色。
const AVATAR = [
  'bg-primarysoft text-primaryink',
  'bg-greenbg text-green',
  'bg-redbg text-red',
  'bg-surface2 text-amberink',
]

/** avatarClass 按名字挑一组配色，稳定且与内容无关地分散开。 */
export function avatarClass(name: string): string {
  let h = 0
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0
  return AVATAR[h % AVATAR.length]
}

/** avatarText 取首字：中文取第一个字，英文取首字母大写。 */
export function avatarText(name: string): string {
  const t = name.trim()
  if (!t) return '?'
  return [...t][0].toUpperCase()
}

/**
 * MARKET_PAGE_SIZE 是市场一页放几张卡片。
 *
 * 分页是**客户端**做的：后端一次把全量清单拉下来缓存住，搜索与分类因此覆盖整个
 * 市场。若改成按页向服务端要，搜索就只能搜当前这 20 条——用户搜不到东西时不会
 * 想到"翻到第 3 页再搜一次"，只会以为市场里没有。
 */
export const MARKET_PAGE_SIZE = 20

/** pageCount 是 total 条按每页 size 条分成几页；空清单也算 1 页（要画空态）。 */
export function pageCount(total: number, size = MARKET_PAGE_SIZE): number {
  return Math.max(1, Math.ceil(total / size))
}

/**
 * clampPage 把页码收进 [1, pageCount]。
 *
 * 筛选会让总页数缩水，而页码是独立的一份状态：停在第 5 页时输入搜索词，不夹一下
 * 就会看到一片空白——有结果，只是不在这一页。
 */
export function clampPage(page: number, total: number, size = MARKET_PAGE_SIZE): number {
  return Math.min(Math.max(1, Math.trunc(page) || 1), pageCount(total, size))
}

/** pageSlice 取第 page 页（1 起）。页码越界时按 clampPage 落到最近的合法页。 */
export function pageSlice<T>(items: T[], page: number, size = MARKET_PAGE_SIZE): T[] {
  const p = clampPage(page, items.length, size)
  return items.slice((p - 1) * size, p * size)
}
