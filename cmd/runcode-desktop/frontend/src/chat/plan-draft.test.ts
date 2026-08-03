import { describe, it, expect } from 'vitest'
import { moveStep, removeStep, insertStepAfter, patchStep, cleanDoc, canApprove, dirty } from './plan-draft'
import type { PlanDoc, PlanStep } from '@/core/bridge'

const steps = (...titles: string[]): PlanStep[] => titles.map((t, i) => ({ id: `s${i + 1}`, title: t }))
const titles = (list: PlanStep[]) => list.map((s) => s.title)

describe('moveStep', () => {
  it('上移下移都改变顺序', () => {
    expect(titles(moveStep(steps('a', 'b', 'c'), 1, -1))).toEqual(['b', 'a', 'c'])
    expect(titles(moveStep(steps('a', 'b', 'c'), 1, 1))).toEqual(['a', 'c', 'b'])
  })

  it('越界原样返回——第一条上移、最后一条下移都不能把清单弄乱', () => {
    const list = steps('a', 'b')
    expect(moveStep(list, 0, -1)).toBe(list)
    expect(moveStep(list, 1, 1)).toBe(list)
    expect(moveStep(list, 5, -1)).toBe(list)
  })

  it('不修改原数组(React 要靠引用变化重渲染)', () => {
    const list = steps('a', 'b')
    moveStep(list, 0, 1)
    expect(titles(list)).toEqual(['a', 'b'])
  })
})

describe('removeStep / insertStepAfter', () => {
  it('删除指定步骤', () => {
    expect(titles(removeStep(steps('a', 'b', 'c'), 1))).toEqual(['a', 'c'])
  })

  it('可以删到空——空清单由 canApprove 拦住，而不是不让删', () => {
    expect(removeStep(steps('a'), 0)).toEqual([])
    expect(canApprove({ steps: [] })).toBe(false)
  })

  it('在指定位置之后插入空步骤,并给它一个唯一 id', () => {
    const out = insertStepAfter(steps('a', 'b'), 0)
    expect(titles(out)).toEqual(['a', '', 'b'])
    expect(out[1].id).toBeTruthy()
    expect(new Set(out.map((s) => s.id)).size).toBe(3)
  })

  it('空清单里也能加第一条', () => {
    expect(insertStepAfter([], -1)).toHaveLength(1)
  })
})

describe('patchStep', () => {
  it('只改目标步骤', () => {
    const out = patchStep(steps('a', 'b'), 1, { title: 'B', detail: '细节' })
    expect(out[0]).toEqual({ id: 's1', title: 'a' })
    expect(out[1]).toEqual({ id: 's2', title: 'B', detail: '细节' })
  })
})

describe('cleanDoc', () => {
  it('丢掉没写标题的步骤,并 trim 各字段', () => {
    const doc: PlanDoc = {
      steps: [
        { id: 's1', title: '  加任务表  ', detail: '  新建迁移  ', files: [' db/0007.sql ', ''] },
        { id: 's2', title: '   ' },
      ],
    }
    const out = cleanDoc(doc)
    expect(out.steps).toEqual([{ id: 's1', title: '加任务表', detail: '新建迁移', files: ['db/0007.sql'] }])
  })

  it('空 detail / 空 files 不下发(省得给模型看一堆空字段)', () => {
    const out = cleanDoc({ steps: [{ id: 's1', title: 'a', detail: '  ', files: [] }] })
    expect(out.steps![0].detail).toBeUndefined()
    expect(out.steps![0].files).toBeUndefined()
  })
})

describe('canApprove', () => {
  it('至少要有一条有标题的步骤', () => {
    expect(canApprove({ steps: [{ id: 's1', title: '干活' }] })).toBe(true)
    expect(canApprove({ steps: [{ id: 's1', title: '  ' }] })).toBe(false)
    expect(canApprove({ steps: [] })).toBe(false)
    expect(canApprove(null)).toBe(false)
  })
})

describe('dirty', () => {
  const saved: PlanDoc = { steps: [{ id: 's1', title: 'a' }, { id: 's2', title: 'b' }] }

  it('顺序变了算改过', () => {
    expect(dirty({ steps: [{ id: 's2', title: 'b' }, { id: 's1', title: 'a' }] }, saved)).toBe(true)
  })

  it('内容一样就不算改过(避免每次确认都白存一次)', () => {
    expect(dirty({ steps: [{ id: 's1', title: 'a' }, { id: 's2', title: 'b' }] }, saved)).toBe(false)
  })

  it('只有 id 不同不算改过——id 是前端记账,不下发给模型', () => {
    expect(dirty({ steps: [{ id: 'new1', title: 'a' }, { id: 'new2', title: 'b' }] }, saved)).toBe(false)
  })

  it('改写标题、加删步骤都算改过', () => {
    expect(dirty({ steps: [{ id: 's1', title: 'A' }, { id: 's2', title: 'b' }] }, saved)).toBe(true)
    expect(dirty({ steps: [{ id: 's1', title: 'a' }] }, saved)).toBe(true)
  })
})
