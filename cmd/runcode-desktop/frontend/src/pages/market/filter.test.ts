import { describe, expect, it } from 'vitest'
import { type MarketSkill } from '@/core/bridge'
import { MARKET_PAGE_SIZE, avatarClass, avatarText, clampPage, filterMarket, matchMarket, pageCount, pageSlice } from './filter'

const mk = (p: Partial<MarketSkill>): MarketSkill => ({
  id: 1, name: 'cn-docx', displayName: '中文公文', description: '生成 Word 公文',
  category: '办公协同', version: '1.0.0', allowedTools: [], hasBundle: true,
  installedUser: false, installedProject: false,
  ...p,
})

describe('matchMarket', () => {
  it('空搜索词全命中', () => {
    expect(matchMarket(mk({}), '   ')).toBe(true)
  })

  it('中文展示名与英文 name 都能搜到同一条', () => {
    // 这个市场里展示名是中文、name 是 kebab-case，用户记得住的可能是任何一个。
    expect(matchMarket(mk({}), '公文')).toBe(true)
    expect(matchMarket(mk({}), 'docx')).toBe(true)
  })

  it('描述与分类也算', () => {
    expect(matchMarket(mk({}), 'word')).toBe(true) // 大小写不敏感
    expect(matchMarket(mk({}), '办公')).toBe(true)
  })

  it('都不沾边就不命中', () => {
    expect(matchMarket(mk({}), '路由器')).toBe(false)
  })
})

describe('filterMarket', () => {
  const list = [
    mk({ id: 1, name: 'a', displayName: '甲', category: '办公协同' }),
    mk({ id: 2, name: 'b', displayName: '乙', category: '开发工具' }),
    mk({ id: 3, name: 'c', displayName: '丙', category: '办公协同', installedUser: true }),
  ]

  it('空分类 = 全部', () => {
    expect(filterMarket(list, '', '').map((s) => s.id)).toEqual([1, 2, 3])
  })

  it('按分类筛', () => {
    expect(filterMarket(list, '开发工具', '').map((s) => s.id)).toEqual([2])
  })

  it('分类与搜索词同时生效', () => {
    expect(filterMarket(list, '办公协同', '丙').map((s) => s.id)).toEqual([3])
  })

  it('不重排：已安装的留在原位', () => {
    // 装完把卡片挪走会让刚点过的那张在眼皮底下跳位，下一次点击就落到别人身上。
    expect(filterMarket(list, '', '').map((s) => s.id)).toEqual([1, 2, 3])
  })
})

describe('avatar', () => {
  it('同名总是同一组配色', () => {
    expect(avatarClass('cn-docx')).toBe(avatarClass('cn-docx'))
  })

  it('取首字；英文大写', () => {
    expect(avatarText('中文公文')).toBe('中')
    expect(avatarText('docx tool')).toBe('D')
    expect(avatarText('  ')).toBe('?')
  })

  it('emoji 开头不会被劈成半个', () => {
    expect(avatarText('🚀 发布助手')).toBe('🚀')
  })
})

describe('分页', () => {
  const many = Array.from({ length: 45 }, (_, i) => mk({ id: i + 1, name: 's' + i }))

  it('每页 20 个', () => {
    expect(MARKET_PAGE_SIZE).toBe(20)
    expect(pageCount(45)).toBe(3)
    expect(pageSlice(many, 1).map((s) => s.id)).toEqual(Array.from({ length: 20 }, (_, i) => i + 1))
    expect(pageSlice(many, 3).map((s) => s.id)).toEqual([41, 42, 43, 44, 45])
  })

  it('空清单也算一页——要有地方画空态', () => {
    expect(pageCount(0)).toBe(1)
    expect(pageSlice([], 1)).toEqual([])
  })

  it('筛完页数缩水时把页码夹回最后一页，而不是给一片空白', () => {
    // 停在第 3 页时输入搜索词，结果只剩 5 条：不夹一下就会看到空白——有结果，
    // 只是不在这一页，而用户不会想到"往回翻"。
    expect(clampPage(3, 5)).toBe(1)
    expect(pageSlice(many.slice(0, 25), 3).map((s) => s.id)).toEqual([21, 22, 23, 24, 25])
  })

  it('非法页码落到第一页', () => {
    expect(clampPage(0, 45)).toBe(1)
    expect(clampPage(-7, 45)).toBe(1)
    expect(clampPage(NaN, 45)).toBe(1)
    expect(clampPage(999, 45)).toBe(3)
  })
})
