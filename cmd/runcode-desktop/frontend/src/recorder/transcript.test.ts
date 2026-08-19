import { describe, expect, it } from 'vitest'
import type { RecorderTranscript } from '@/core/bridge'
import { applyTranscript, emptyTranscript, lastLine, LIVE_SEG, timeline, type TranscriptState } from './transcript'

function ev(p: Partial<RecorderTranscript> & { type: string; track: string }): RecorderTranscript {
  return { seg: 0, rev: 0, ...p } as RecorderTranscript
}

function fold(...events: RecorderTranscript[]): TranscriptState {
  return events.reduce(applyTranscript, emptyTranscript)
}

describe('applyTranscript', () => {
  it('实时行按轨各一条，文本是全量替换而不是拼接', () => {
    // 块模式下 partial 的 text 已经是累积后的全文（engine/track.py 的
    // _partial_text += piece），前端再拼一次就会出现「你好你好吗」。
    const s = fold(
      ev({ type: 'partial', track: 'mic', seg: LIVE_SEG, text: '你好' }),
      ev({ type: 'partial', track: 'mic', seg: LIVE_SEG, text: '你好吗' }),
    )
    expect(Object.keys(s.live)).toEqual(['mic'])
    expect(s.live.mic.text).toBe('你好吗')
  })

  it('live_clear 只清自己那一轨', () => {
    const s = fold(
      ev({ type: 'partial', track: 'mic', seg: LIVE_SEG, text: '我这边' }),
      ev({ type: 'partial', track: 'sys', seg: LIVE_SEG, text: '对方那边' }),
      ev({ type: 'live_clear', track: 'mic', seg: LIVE_SEG }),
    )
    expect(s.live.mic).toBeUndefined()
    expect(s.live.sys.text).toBe('对方那边')
  })

  it('两条轨按房间时间轴交织，不是按到达顺序', () => {
    // 上行是两条独立的 WebSocket，回环轨的事件完全可能比更早说的麦克风轨先到。
    const s = fold(
      ev({ type: 'final', track: 'sys', seg: 0, rev: 1, text: '第二句', bt: 5000, et: 7000 }),
      ev({ type: 'final', track: 'mic', seg: 0, rev: 1, text: '第一句', bt: 1000, et: 3000 }),
      ev({ type: 'final', track: 'mic', seg: 1, rev: 1, text: '第三句', bt: 9000, et: 11000 }),
    )
    expect(s.finals.map((x) => x.text)).toEqual(['第一句', '第二句', '第三句'])
  })

  it('drop 撤掉旧段，紧随其后的新 final 补上', () => {
    // 这是修订：段落边界变了（两句合并成一句），旧段整批撤掉再发新的。
    let s = fold(
      ev({ type: 'final', track: 'mic', seg: 0, rev: 1, text: '这个方案', bt: 0, et: 1000 }),
      ev({ type: 'final', track: 'mic', seg: 1, rev: 1, text: '先上灰度', bt: 1000, et: 2000 }),
    )
    s = applyTranscript(s, ev({ type: 'drop', track: 'mic', segs: [0, 1] }))
    expect(s.finals).toHaveLength(0)
    s = applyTranscript(s, ev({ type: 'final', track: 'mic', seg: 2, rev: 1, text: '这个方案先上灰度', bt: 0, et: 2000 }))
    expect(s.finals.map((x) => x.text)).toEqual(['这个方案先上灰度'])
  })

  it('drop 不误伤另一条轨的同号段', () => {
    // seg 是每轨各自递增的，两条轨的 seg 0 是两个不同的句子。
    let s = fold(
      ev({ type: 'final', track: 'mic', seg: 0, rev: 1, text: '我说的', bt: 0 }),
      ev({ type: 'final', track: 'sys', seg: 0, rev: 1, text: '对方说的', bt: 100 }),
    )
    s = applyTranscript(s, ev({ type: 'drop', track: 'mic', segs: [0] }))
    expect(s.finals.map((x) => x.text)).toEqual(['对方说的'])
  })

  it('低 rev 不能覆盖高 rev', () => {
    // 精修结果（rev 2/3）晚到是常态，但断线重连后服务端会重放，低 rev 可能
    // 后到——照单全收就会把精修好的文本又换回粗结果。
    let s = fold(ev({ type: 'final', track: 'mic', seg: 0, rev: 3, text: '精修后的文本', bt: 0 }))
    s = applyTranscript(s, ev({ type: 'final', track: 'mic', seg: 0, rev: 1, text: '粗结果', bt: 0 }))
    expect(s.finals[0].text).toBe('精修后的文本')
  })

  it('respeaker 追认说话人，不动文本', () => {
    let s = fold(ev({ type: 'final', track: 'sys', seg: 0, rev: 1, text: '我同意', bt: 0, spk: 'spk1' }))
    expect(s.finals[0].speaker).toBe('spk1')
    s = applyTranscript(s, ev({ type: 'respeaker', track: 'sys', seg: 0, spk: 'spk1', name: '马文涛' }))
    expect(s.finals[0].speaker).toBe('马文涛')
    expect(s.finals[0].text).toBe('我同意')
  })

  it('respeaker 找不到对应段时原样返回', () => {
    const s = fold(ev({ type: 'final', track: 'mic', seg: 0, rev: 1, text: '在', bt: 0 }))
    expect(applyTranscript(s, ev({ type: 'respeaker', track: 'mic', seg: 99, name: '谁' }))).toBe(s)
  })

  it('说话人取名优先于盲聚类编号，都没有时退到轨道称呼', () => {
    const s = fold(
      ev({ type: 'final', track: 'mic', seg: 0, rev: 1, text: 'a', bt: 0 }),
      ev({ type: 'final', track: 'sys', seg: 0, rev: 1, text: 'b', bt: 1, spk: 'spk2' }),
      ev({ type: 'final', track: 'sys', seg: 1, rev: 1, text: 'c', bt: 2, spk: 'spk2', name: '李明' }),
    )
    expect(s.finals.map((x) => x.speaker)).toEqual(['我', 'spk2', '李明'])
  })

  it('live_status 按轨记录还要静音多久', () => {
    const s = fold(ev({ type: 'live_status', track: 'mic', silence: 2, need: 5 }))
    expect(s.silence.mic).toEqual({ silence: 2, need: 5 })
  })

  it('无关事件返回同一个对象，让渲染能靠引用相等跳过', () => {
    const s = fold(ev({ type: 'final', track: 'mic', seg: 0, rev: 1, text: 'x', bt: 0 }))
    expect(applyTranscript(s, ev({ type: 'ready', track: 'mic' }))).toBe(s)
  })
})

describe('timeline', () => {
  it('实时行永远排在确认文本之后', () => {
    // 实时行的 bt 可能比某些确认段还早（块从头开始算），但它是"正在说的话"，
    // 位置必须固定在底部，否则视线要在列表中间找它。
    const s = fold(
      ev({ type: 'final', track: 'mic', seg: 0, rev: 1, text: '已确认', bt: 9000 }),
      ev({ type: 'partial', track: 'sys', seg: LIVE_SEG, text: '正在说', bt: 0 }),
    )
    expect(timeline(s).map((x) => x.text)).toEqual(['已确认', '正在说'])
  })

  it('空的实时行不占位', () => {
    const s = fold(ev({ type: 'partial', track: 'mic', seg: LIVE_SEG, text: '' }))
    expect(timeline(s)).toHaveLength(0)
  })
})

describe('lastLine', () => {
  it('取整场最新的一句', () => {
    const s = fold(
      ev({ type: 'final', track: 'mic', seg: 0, rev: 1, text: '上一句', bt: 0 }),
      ev({ type: 'partial', track: 'mic', seg: LIVE_SEG, text: '这一句', bt: 1000 }),
    )
    expect(lastLine(s)).toBe('这一句')
  })

  it('什么都没有时是空串', () => {
    expect(lastLine(emptyTranscript)).toBe('')
  })
})
