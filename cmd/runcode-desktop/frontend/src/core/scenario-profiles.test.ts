import { describe, expect, it } from 'vitest'
import { selectBrand } from './brand'
import { scenariosForProfile } from './scenario-profiles'
import { SCENARIOS, applyScenario, visibleScenarios } from './scenarios'

const expectedOA = [
  ['待办事项查询', '查询OA系统里的最近待办事项', '帮我查询OA系统里我最近的待办事项'],
  ['办事流程查询', '查询OA系统里的某个办事的具体流程进展', '帮我查询OA系统里【办事名称】的办事具体流程进展，看看我当前需要做什么'],
  ['最新通知查询', '查询OA系统里的最近通知', '帮我查询OA系统里最近的通知，看看哪些值得我关注'],
  ['教职工名片信息查询', '查询OA系统里的教职工名片信息', '帮我查询OA系统里【人名】的个人名片信息'],
  ['干部任职查询', '查询OA系统里的干部任职信息', '帮我查询OA系统里最近的干部任职信息'],
  ['规章制度查询', '查询OA系统里的规章制度信息', '帮我查询OA系统里最近的规章制度，看看哪些值得我关注'],
  ['党群园地信息查询', '查询OA系统里的党群园地信息', '帮我查询OA系统里最近的党群园地信息'],
]

const guokai = () => scenariosForProfile(selectBrand('zhikai-guokai').scenarioProfile)

describe('国开版场景目录', () => {
  it('只保留指定三类且顺序固定，提供录音动作也不会多出第四类', () => {
    const categories = guokai()
    expect(categories.map((c) => c.id)).toEqual(['oa', 'format', 'slides'])
    expect(categories.map((c) => c.name)).toEqual(['OA信息查询', '格式校验', '幻灯片'])
    expect(visibleScenarios(categories, { recorder: { onPick() {} } })).toEqual(categories)
  })

  it('OA 七项按原文和顺序呈现，不关联市场技能', () => {
    const items = guokai()[0].items
    expect(items.map((s) => [s.name, s.blurb, s.prompt])).toEqual(expectedOA)
    expect(items.every((s) => s.skill === '')).toBe(true)
    expect(items.map((s) => s.id)).toEqual(['oa-1', 'oa-workflow', 'oa-3', 'oa-2', 'oa-appointments', 'oa-regulations', 'oa-party'])
  })

  it('格式校验和幻灯片复用公共条目及技能关联', () => {
    for (const id of ['format', 'slides']) {
      expect(guokai().find((c) => c.id === id)).toBe(SCENARIOS.find((c) => c.id === id))
    }
  })

  it('原品牌仍使用完整目录，切换目录不改写原 OA 内容', () => {
    const before = JSON.stringify(SCENARIOS)
    guokai()
    for (const key of ['runcode', 'zhikai', 'unknown']) {
      expect(scenariosForProfile(selectBrand(key).scenarioProfile)).toBe(SCENARIOS)
    }
    expect(JSON.stringify(SCENARIOS)).toBe(before)
    expect(SCENARIOS.find((c) => c.id === 'oa')?.name).toBe('OA查询')
    expect(SCENARIOS.find((c) => c.id === 'oa')?.items).toHaveLength(3)
  })

  it('分类和场景 ID 均不重复且为合法资源名', () => {
    const categories = guokai()
    const ids = categories.flatMap((c) => c.items.map((s) => s.id))
    expect(new Set(categories.map((c) => c.id)).size).toBe(categories.length)
    expect(new Set(ids).size).toBe(ids.length)
    expect(ids.every((id) => /^[A-Za-z0-9_-]{1,64}$/.test(id))).toBe(true)
  })

  it('流程和名片占位符被选中，已有草稿不被覆盖', () => {
    for (const [id, placeholder] of [['oa-workflow', '【办事名称】'], ['oa-2', '【人名】']]) {
      const task = guokai()[0].items.find((s) => s.id === id)!
      const result = applyScenario('保留这份草稿', task.prompt)
      expect(result.value).toBe(`保留这份草稿\n\n${task.prompt}`)
      expect(result.value.slice(result.start, result.end)).toBe(placeholder)
    }
  })
})
