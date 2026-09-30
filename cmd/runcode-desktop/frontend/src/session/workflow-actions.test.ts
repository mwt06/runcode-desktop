import { describe, expect, it, vi } from 'vitest'
import type { RecordingInfo, SkillList, SkillMarketPage } from '@/core/bridge'
import type { Scenario } from '@/core/scenarios'
import { createActionScope } from './action-scope'
import { ensureScenarioSkill, pickScenarioAction, type ScenarioActions } from './scenario-actions'
import { createMinutesTrigger, sendRecordingMinutes, type MinutesActions } from './recording-minutes'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => { resolve = r })
  return { promise, resolve }
}
const skillList = (skills: Array<{ name: string; disabledUser?: boolean; disabledProject?: boolean }>) => ({ skills }) as SkillList
const scenario: Scenario = { id: 's', name: '场景', blurb: '', prompt: '请处理【任务】', skill: 'cn-docx' }
function scenarioDeps(): ScenarioActions {
  return {
    list: vi.fn(async () => skillList([])),
    market: vi.fn(async () => ({ skills: [{ id: 1, name: 'cn-docx' }] }) as SkillMarketPage),
    install: vi.fn(async () => undefined), installing: vi.fn(), notify: vi.fn(), errorText: String,
  }
}
const mark = { id: 'rec', title: '例会', audioMs: 1000 }
function minutesDeps(): MinutesActions {
  return {
    read: vi.fn(async () => '会议转写正文'), skills: vi.fn(async () => skillList([])),
    send: vi.fn(async () => undefined), notify: vi.fn(), errorText: String,
  }
}

describe('action ownership', () => {
  it('没有会话不接收异步结果，切走再切回不复活旧请求', () => {
    const scope = createActionScope()
    expect(scope.capture()()).toBe(false)
    scope.focus('a')
    const old = scope.capture()
    expect(old()).toBe(true)
    scope.focus('b'); scope.focus('a')
    expect(old()).toBe(false)
    const next = scope.capture()
    scope.invalidate()
    expect(next()).toBe(false)
  })
})

it('卸载时失效，StrictMode 同步重放生命周期不吞掉已认领任务', () => {
  const scope = createActionScope()
  scope.focus('a')
  const current = scope.capture()
  scope.deactivate()
  expect(current()).toBe(false)
  scope.activate()
  expect(current()).toBe(true)
})

describe('scenario actions', () => {
  it('本地可用时不查询市场，也不安装', async () => {
    const deps = scenarioDeps()
    deps.list = vi.fn(async () => skillList([{ name: 'cn-docx' }]))
    expect(await ensureScenarioSkill('cn-docx', deps)).toBe(true)
    expect(deps.market).not.toHaveBeenCalled()
    expect(deps.install).not.toHaveBeenCalled()
  })
  it('停用的技能不被重新安装或点名', async () => {
    const deps = scenarioDeps()
    deps.list = vi.fn(async () => skillList([{ name: 'cn-docx', disabledProject: true }]))
    expect(await ensureScenarioSkill('cn-docx', deps)).toBe(false)
    expect(deps.market).not.toHaveBeenCalled()
  })
  it('市场安装成功，始终收起等待状态', async () => {
    const deps = scenarioDeps()
    expect(await ensureScenarioSkill('cn-docx', deps)).toBe(true)
    expect(deps.install).toHaveBeenCalledWith(1)
    expect(deps.installing).toHaveBeenLastCalledWith(null)
  })
  it('安装失败仍然填入提示词，但不点名技能', async () => {
    const deps = scenarioDeps()
    deps.install = vi.fn(async () => { throw new Error('offline') })
    const apply = vi.fn()
    await pickScenarioAction(scenario, deps, { current: () => true, read: () => '', apply })
    expect(apply.mock.calls[0][0].value).toBe(scenario.prompt)
    expect(deps.installing).toHaveBeenLastCalledWith(null)
    expect(deps.notify).toHaveBeenCalledWith('Error: offline')
  })
  it('等待过程中输入的新草稿被保留', async () => {
    const deps = scenarioDeps()
    const wait = deferred<SkillList>()
    deps.list = () => wait.promise
    let input = '原文'
    const apply = vi.fn()
    const work = pickScenarioAction(scenario, deps, { current: () => true, read: () => input, apply })
    input = '刚刚输入的新内容'
    wait.resolve(skillList([{ name: 'cn-docx' }]))
    await work
    expect(apply.mock.calls[0][0].value).toContain(input)
  })
  it('等待技能期间切换会话不写入另一条草稿', async () => {
    const deps = scenarioDeps()
    const wait = deferred<SkillList>()
    deps.list = () => wait.promise
    const scope = createActionScope(); scope.focus('a')
    const apply = vi.fn()
    const work = pickScenarioAction(scenario, deps, { current: scope.capture(), read: () => 'b', apply })
    scope.focus('b')
    wait.resolve(skillList([{ name: 'cn-docx' }]))
    await work
    expect(apply).not.toHaveBeenCalled()
  })
})

describe('recording minutes', () => {
  it('速览不加载技能，转写只进真正的请求而非显示文案', async () => {
    const deps = minutesDeps()
    await sendRecordingMinutes(mark, 'digest', () => true, deps)
    expect(deps.skills).not.toHaveBeenCalled()
    const [text, , display] = vi.mocked(deps.send).mock.calls[0]
    expect(text).toContain('会议转写正文')
    expect(display).not.toContain('会议转写正文')
  })
  it('没有转写或会话时不发请求', async () => {
    const deps = minutesDeps()
    deps.read = vi.fn(async () => '  ')
    await sendRecordingMinutes(mark, 'doc', () => true, deps)
    await sendRecordingMinutes(mark, 'doc', () => false, deps)
    expect(deps.send).not.toHaveBeenCalled()
    expect(deps.read).toHaveBeenCalledTimes(1)
  })
  it('文档跳过被停用的技能', async () => {
    const deps = minutesDeps()
    deps.skills = vi.fn(async () => skillList([{ name: 'cn-docx', disabledUser: true }]))
    await sendRecordingMinutes(mark, 'doc', () => true, deps)
    expect(vi.mocked(deps.send).mock.calls[0][0]).not.toContain('cn-docx')
  })
  it('读取转写后会话已切换，不把内容发到新会话', async () => {
    const deps = minutesDeps()
    const wait = deferred<string>(); deps.read = () => wait.promise
    const scope = createActionScope(); scope.focus('a')
    const work = sendRecordingMinutes(mark, 'doc', scope.capture(), deps)
    scope.focus('b'); wait.resolve('原会话的内容')
    await work
    expect(deps.skills).not.toHaveBeenCalled()
    expect(deps.send).not.toHaveBeenCalled()
  })
  it('等待技能时关闭会话也不会发送', async () => {
    const deps = minutesDeps()
    const wait = deferred<SkillList>(); deps.skills = () => wait.promise
    const scope = createActionScope(); scope.focus('a')
    const work = sendRecordingMinutes(mark, 'doc', scope.capture(), deps)
    await Promise.resolve()
    scope.invalidate(); wait.resolve(skillList([]))
    await work
    expect(deps.send).not.toHaveBeenCalled()
  })
  it('自动触发按 id 去重，无会话不消耗机会', () => {
    const trigger = createMinutesTrigger()
    const rec = { id: 'r', state: 'stopped', transcript: 'transcript.md' } as RecordingInfo
    expect(trigger(rec, false)).toBe(false)
    expect(trigger(rec, true)).toBe(true)
    expect(trigger(rec, true)).toBe(false)
    expect(trigger({ ...rec, id: 'r2' }, true)).toBe(true)
    expect(trigger(rec, true)).toBe(false)
  })
})
