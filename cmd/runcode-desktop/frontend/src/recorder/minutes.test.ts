import { describe, expect, it } from 'vitest'

import { buildMinutesPrompt, minutesDisplayText, minutesFileName, parseRecordingMarker, pickMinutesSkill, speakerBriefing, speakerLabels, type RecordingMark } from './minutes'

function info(p: Partial<RecordingMark> = {}): RecordingMark {
  return {
    id: 'rec_20260819_105756',
    title: '季度评审',
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
    const p = buildMinutesPrompt({ mark: info(), transcript, skill: '国开会议纪要', outPath: 'out.md' })
    expect(p).toContain('请使用「国开会议纪要」技能')
  })

  it('没有技能时退回通用措辞，不提技能', () => {
    const p = buildMinutesPrompt({ mark: info(), transcript, outPath: 'out.md' })
    expect(p).toContain('整理成一份会议纪要')
    expect(p).not.toContain('技能')
  })

  it('带上标题、时间、时长与落盘路径', () => {
    const p = buildMinutesPrompt({ mark: info(), transcript, outPath: '会议纪要-20260819-季度评审.md' })
    expect(p).toContain('季度评审')
    expect(p).toContain('2026-08-19 10:57')
    expect(p).toContain('7 分 5 秒')
    expect(p).toContain('`会议纪要-20260819-季度评审.md`')
  })



  it('禁止补写转写里没有的信息', () => {
    // 纪要被当会议记录用，编出来的待办和责任人是最坏的一类错误。
    const p = buildMinutesPrompt({ mark: info(), transcript, outPath: 'out.md' })
    expect(p).toContain('不要补')
  })

  it('转写有缺口时要求点明，不要脑补衔接', () => {
    const p = buildMinutesPrompt({ mark: info({ needsBackfill: true }), transcript, outPath: 'out.md' })
    expect(p).toContain('断开过')
    expect(p).toContain('不要脑补')
  })

  it('没缺口时不提缺口，免得模型无端加免责声明', () => {
    const p = buildMinutesPrompt({ mark: info(), transcript, outPath: 'out.md' })
    expect(p).not.toContain('断开过')
  })

  it('转写原文整段带进去', () => {
    const p = buildMinutesPrompt({ mark: info(), transcript, outPath: 'out.md' })
    expect(p).toContain('先说结论')
    expect(p).toContain('同意')
  })
})

describe('speakerLabels', () => {
  it('按首次出现的顺序列出，不重复', () => {
    const t = [
      '**[00:03] S1**：先说结论',
      '**[00:11] 马文涛**：同意',
      '**[00:20] S1**：那就这样',
      '**[00:31] S5**：嗯',
    ].join('\n\n')
    expect(speakerLabels(t)).toEqual(['S1', '马文涛', 'S5'])
  })

  it('格式对不上时返回空，不瞎猜', () => {
    // 这个正则和 Go 侧 Transcript.Markdown 的输出格式绑死，那边改了这里要跟。
    expect(speakerLabels('S1: 这不是我们的格式')).toEqual([])
  })
})

describe('speakerBriefing', () => {
  const mixed = '**[00:03] S1**：喂，阿波哥\n\n**[00:11] 马文涛**：在\n\n**[00:20] S5**：嗯'

  it('列出本场真实出现的标签，而不是举例', () => {
    const b = speakerBriefing(mixed).join('\n')
    expect(b).toContain('S1、马文涛、S5')
  })

  it('把编号和显示名分开解释', () => {
    const b = speakerBriefing(mixed).join('\n')
    expect(b).toContain('S1、S5 是声纹聚类给出的编号')
    expect(b).toContain('马文涛 是配置里填的显示名')
  })

  it('说明编号的作用域：同场同号即同人，跨场不通用', () => {
    const b = speakerBriefing(mixed).join('\n')
    expect(b).toContain('本场内指同一个人')
    expect(b).toContain('不在不同场次之间通用')
  })

  it('切断「内容里提到的人名」与编号的关联', () => {
    // 实测模型会自作主张认定「S1 应该就是阿波哥」——而喊名字的和被喊的从来不是
    // 同一个人。把发言安到具体某人头上，是这类纪要最坏的一种错。
    const b = speakerBriefing(mixed).join('\n')
    expect(b).toContain('不能')
    expect(b).toContain('喊名字的和被喊的从来不是同一个人')
  })

  it('全是编号时不提显示名那一行', () => {
    const b = speakerBriefing('**[00:03] S1**：甲\n\n**[00:09] S2**：乙').join('\n')
    expect(b).not.toContain('显示名')
  })

  it('空转写不编造标签', () => {
    expect(speakerBriefing('').join('\n')).toContain('（无）')
  })
})

describe('录音标记', () => {
  it('提示词开头带标记，且能原样解回来', () => {
    // 这是「历史恢复」的全部依据：对话历史由引擎回放，回放回来的只有消息原文，
    // 卡片本身不在里面。解不出来，恢复后就只剩几千字的提示词。
    const m = info({ dir: '/rec/x', transcript: 'transcript.md', needsBackfill: true })
    const p = buildMinutesPrompt({ mark: m, transcript: '**[00:03] S1**：喂', outPath: 'out.md' })
    expect(parseRecordingMarker(p)).toEqual(m)
  })

  it('普通消息解不出标记', () => {
    expect(parseRecordingMarker('帮我看看这个报错')).toBeNull()
  })

  it('标记坏了当作没有，而不是让整条消息渲染不出来', () => {
    expect(parseRecordingMarker('<!-- runcode-recording {坏的 -->\n正文')).toBeNull()
    expect(parseRecordingMarker('<!-- runcode-recording {"title":"缺 id"} -->')).toBeNull()
  })
})

describe('minutesDisplayText', () => {
  it('实时与恢复共用同一句，两边必须一致', () => {
    expect(minutesDisplayText('季度评审')).toBe('录音纪要 · 已附上《季度评审》的转写全文')
  })

  it('没标题时有兜底', () => {
    expect(minutesDisplayText('')).toContain('《录音》')
  })
})
