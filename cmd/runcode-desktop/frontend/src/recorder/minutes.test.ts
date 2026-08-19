import { describe, expect, it } from 'vitest'
import type { RecordingInfo } from '@/core/bridge'
import { buildMinutesPrompt, minutesFileName, pickMinutesSkill } from './minutes'

function info(p: Partial<RecordingInfo> = {}): RecordingInfo {
  return {
    id: 'rec_20260819_105756',
    title: '季度评审',
    room: 'rec_20260819_105756',
    state: 'stopped',
    startedAt: '2026-08-19T10:57:56+08:00',
    audioMs: 425_000,
    ...p,
  }
}

describe('pickMinutesSkill', () => {
  it('优先命中「会议纪要」而不是只含「纪要」的那个', () => {
    // 机构自己的模板技能通常叫「国开会议纪要」，而工作区里可能还有别的沾边技能。
    expect(pickMinutesSkill(['周报纪要', '国开会议纪要', '深度研究'])).toBe('国开会议纪要')
  })

  it('挑不到就返回空串，让调用方退回通用提示', () => {
    expect(pickMinutesSkill(['深度研究', 'ppt'])).toBe('')
  })
})

describe('minutesFileName', () => {
  it('日期在前，一个工作区里多场会议才排得整齐', () => {
    expect(minutesFileName(info())).toBe('会议纪要-20260819-季度评审.md')
  })

  it('洗掉标题里的路径分隔符与保留字符', () => {
    // 标题是用户填的，直接拼进文件名会写到别的目录去，或者在 Windows 上直接失败。
    expect(minutesFileName(info({ title: 'a/b:c*d?e"f<g>h|i' }))).toBe('会议纪要-20260819-a_b_c_d_e_f_g_h_i.md')
  })

  it('标题为空时有兜底', () => {
    expect(minutesFileName(info({ title: '' }))).toBe('会议纪要-20260819-录音纪要.md')
  })
})

describe('buildMinutesPrompt', () => {
  const transcript = '**[00:03] 我**：先说结论\n\n**[00:11] S1**：同意'

  it('有技能时明确要求走技能模板', () => {
    const p = buildMinutesPrompt({ info: info(), transcript, skill: '国开会议纪要', outPath: 'out.md' })
    expect(p).toContain('请使用「国开会议纪要」技能')
  })

  it('没有技能时退回通用措辞，不提技能', () => {
    const p = buildMinutesPrompt({ info: info(), transcript, outPath: 'out.md' })
    expect(p).toContain('整理成一份会议纪要')
    expect(p).not.toContain('技能')
  })

  it('带上标题、时间、时长与落盘路径', () => {
    const p = buildMinutesPrompt({ info: info(), transcript, outPath: '会议纪要-20260819-季度评审.md' })
    expect(p).toContain('季度评审')
    expect(p).toContain('2026-08-19 10:57')
    expect(p).toContain('7 分 5 秒')
    expect(p).toContain('`会议纪要-20260819-季度评审.md`')
  })

  it('说清 S1／S2 是聚类编号不是人名', () => {
    // 不说的话纪要里会冒出「S1 表示……」这种把编号当人名用的句子。
    const p = buildMinutesPrompt({ info: info(), transcript, outPath: 'out.md' })
    expect(p).toContain('不是真名')
  })

  it('禁止补写转写里没有的信息', () => {
    // 纪要被当会议记录用，编出来的待办和责任人是最坏的一类错误。
    const p = buildMinutesPrompt({ info: info(), transcript, outPath: 'out.md' })
    expect(p).toContain('不要补')
  })

  it('转写有缺口时要求点明，不要脑补衔接', () => {
    const p = buildMinutesPrompt({ info: info({ needsBackfill: true }), transcript, outPath: 'out.md' })
    expect(p).toContain('断开过')
    expect(p).toContain('不要脑补')
  })

  it('没缺口时不提缺口，免得模型无端加免责声明', () => {
    const p = buildMinutesPrompt({ info: info(), transcript, outPath: 'out.md' })
    expect(p).not.toContain('断开过')
  })

  it('转写原文整段带进去', () => {
    const p = buildMinutesPrompt({ info: info(), transcript, outPath: 'out.md' })
    expect(p).toContain('先说结论')
    expect(p).toContain('同意')
  })
})
