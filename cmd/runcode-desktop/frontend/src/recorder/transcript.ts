// 把网关下行的事件流折叠成一份可渲染的转写。
//
// 纯函数，没有 React 也没有 Wails——这里的规则全部来自网关协议，值得单独测：
// 段落会被撤掉重发（修订），说话人会被追认改名（重聚类），实时行会被确认文本
// 顶掉。任何一条搞错都表现为"字幕自己乱跳"，而那种问题在界面里极难查。
//
// 事件语义（engine/track.py）：
//   partial     实时行。块模式下整块只有一条，seg = LIVE_SEG(-1)，文本持续增长。
//   live_clear  清掉该轨的实时行。确认文本发出前先发它，否则旧 partial 会残留。
//   final       确认段，rev=1，seg 递增。
//   drop        撤掉一批 seg。修订时段落边界会变（合并后一句可能重新断开），
//               没法一一对应地改，只能整批撤掉再发新的。
//   respeaker   重聚类改了历史段落的说话人，按 seg 追认。
//   live_status 还需静音多久才出确认文本，给"识别中"一个交代。
import type { RecorderTranscript } from '@/core/bridge'

// LIVE_SEG 是实时行的段号（服务端 engine/const.py 的 LIVE_SEG）。
export const LIVE_SEG = -1

// TRACK_LABEL 把音轨换成人能读的称呼。这是双轨方案唯一暴露给用户的地方：
// 麦克风是「我」，系统回环是会议软件里对方的声音。
export const TRACK_LABEL: Record<string, string> = { mic: '我', sys: '对方' }

export interface Segment {
  /** key 是 `${track}:${seg}`，跨轨唯一。 */
  key: string
  track: string
  seg: number
  rev: number
  text: string
  /** bt/et 是房间时间轴上的毫秒，两条轨靠它交织成一条时间线。 */
  bt: number
  et: number
  /** speaker 优先用声纹库认出的姓名，退到盲聚类编号，再退到轨道称呼。 */
  speaker: string
  /** live 表示这是未确认的实时行，界面上要与确认文本区分开。 */
  live: boolean
}

export interface TranscriptState {
  /** finals 是已确认的段，按房间时间轴排好序。 */
  finals: Segment[]
  /** live 是每轨至多一条的实时行。 */
  live: Record<string, Segment>
  /** silence 是各轨的"还需静音多少秒"，来自 live_status。 */
  silence: Record<string, { silence: number; need: number }>
}

export const emptyTranscript: TranscriptState = { finals: [], live: {}, silence: {} }

function trackOf(ev: RecorderTranscript): string {
  return ev.track ?? ''
}

function speakerOf(ev: RecorderTranscript): string {
  return ev.name || ev.spk || TRACK_LABEL[trackOf(ev)] || trackOf(ev)
}

function segKey(track: string, seg: number): string {
  return `${track}:${seg}`
}

// byTimeline 按房间时间轴排序。bt 相同时（两人同时开口）用 track 再用 seg 定序，
// 保证同一批事件无论到达顺序如何，渲染出来都是同一个样子。
function byTimeline(a: Segment, b: Segment): number {
  if (a.bt !== b.bt) return a.bt - b.bt
  if (a.track !== b.track) return a.track < b.track ? -1 : 1
  return a.seg - b.seg
}

/**
 * applyTranscript 把一条事件折进当前状态，返回新状态（不改入参）。
 * 不认识的事件类型原样返回同一个对象，让 React 能靠引用相等跳过重渲染。
 */
export function applyTranscript(state: TranscriptState, ev: RecorderTranscript): TranscriptState {
  switch (ev.type) {
    case 'partial': {
      // 块模式下 seg 恒为 LIVE_SEG，文本是累积的全量而非增量，直接替换。
      const seg: Segment = {
        key: segKey(trackOf(ev), ev.seg),
        track: trackOf(ev),
        seg: ev.seg,
        rev: ev.rev,
        text: ev.text ?? '',
        bt: ev.bt ?? 0,
        et: ev.et ?? ev.bt ?? 0,
        speaker: speakerOf(ev),
        live: true,
      }
      return { ...state, live: { ...state.live, [trackOf(ev)]: seg } }
    }

    case 'live_clear': {
      if (!state.live[trackOf(ev)]) return state
      const live = { ...state.live }
      delete live[trackOf(ev)]
      return { ...state, live }
    }

    case 'final': {
      const seg: Segment = {
        key: segKey(trackOf(ev), ev.seg),
        track: trackOf(ev),
        seg: ev.seg,
        rev: ev.rev,
        text: ev.text ?? '',
        bt: ev.bt ?? 0,
        et: ev.et ?? ev.bt ?? 0,
        speaker: speakerOf(ev),
        live: false,
      }
      // 同 seg 重发时按 rev 取高的：精修（rev 2/3）可能比确认文本晚到，
      // 但也可能因为重连而乱序到达，低 rev 不能覆盖高 rev。
      const at = state.finals.findIndex((s) => s.key === seg.key)
      let finals: Segment[]
      if (at < 0) {
        finals = [...state.finals, seg]
      } else if (state.finals[at].rev > seg.rev) {
        return state
      } else {
        finals = state.finals.slice()
        finals[at] = seg
      }
      finals.sort(byTimeline)
      return { ...state, finals }
    }

    case 'drop': {
      const gone = new Set((ev.segs ?? []).map((s) => segKey(trackOf(ev), s)))
      if (gone.size === 0) return state
      const finals = state.finals.filter((s) => !gone.has(s.key))
      if (finals.length === state.finals.length) return state
      return { ...state, finals }
    }

    case 'respeaker': {
      const key = segKey(trackOf(ev), ev.seg)
      const at = state.finals.findIndex((s) => s.key === key)
      if (at < 0) return state
      const speaker = speakerOf(ev)
      if (state.finals[at].speaker === speaker) return state
      const finals = state.finals.slice()
      finals[at] = { ...finals[at], speaker }
      return { ...state, finals }
    }

    case 'live_status':
      return {
        ...state,
        silence: { ...state.silence, [trackOf(ev)]: { silence: ev.silence ?? 0, need: ev.need ?? 0 } },
      }

    default:
      // ready 之类的连接事件对转写没有影响。
      return state
  }
}

/**
 * timeline 把确认段与实时行拼成最终要渲染的那一列。
 * 实时行永远在最后——它是"正在说的话"，位置固定在底部才不会让视线跳来跳去。
 */
export function timeline(state: TranscriptState): Segment[] {
  const live = Object.values(state.live).filter((s) => s.text !== '')
  live.sort(byTimeline)
  return [...state.finals, ...live]
}

/** lastLine 是浮窗那一行：整场里最新的一句，优先取实时行。 */
export function lastLine(state: TranscriptState): string {
  const all = timeline(state)
  return all.length > 0 ? all[all.length - 1].text : ''
}
