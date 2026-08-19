// useRecorder 是录音纪要的单一状态源：状态、转写、电平，以及五个控制动作。
//
// 主窗与录音窗是两个独立的 WebView，各自跑一份这个 hook——它们不共享 JS 内存，
// 同步靠的是 Go 侧广播的那三条事件（v3 的 Event.Emit 会发给所有窗口）。所以两边
// 看到的是同一份事实，而不是主窗算好再转发给录音窗。
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  discardRecording,
  Events,
  onEvent,
  pauseRecording,
  recorderStatus,
  resumeRecording,
  startRecording,
  stopRecording,
  type RecordingInfo,
  type StartRecordingRequest,
} from '@/core/bridge'
import { applyTranscript, emptyTranscript, type TranscriptState } from '@/recorder/transcript'

// LEVEL_DECAY 是电平条的回落系数。事件停了（安静下来）之后条要自己掉下去，
// 停在最后一次的高度上会让人以为还在收音。
const LEVEL_DECAY = 0.75
// LEVEL_IDLE_MS 是多久没收到电平事件就当作静音。Go 侧全零时会停发（见
// startLevelPump），这里得自己把条清掉。
const LEVEL_IDLE_MS = 400

export interface RecorderLevels {
  mic: number
  sys: number
}

export interface Recorder {
  /** info 是当前（或最近一场）录音；state 为 idle 表示没在录。 */
  info: RecordingInfo | null
  /** recording / paused 是两个界面要分开呈现的状态。 */
  recording: boolean
  paused: boolean
  /** elapsedMS 是已录时长，不含暂停。 */
  elapsedMS: number
  transcript: TranscriptState
  levels: RecorderLevels
  /** uplink 为 offline 时界面要显示「离线录制中」——本地照录，转写会有缺口。 */
  uplink: string
  error: string
  start: (req?: StartRecordingRequest) => Promise<RecordingInfo>
  pause: () => Promise<void>
  resume: () => Promise<void>
  stop: () => Promise<RecordingInfo>
  discard: () => Promise<void>
}

export function useRecorder(): Recorder {
  const [info, setInfo] = useState<RecordingInfo | null>(null)
  const [transcript, setTranscript] = useState<TranscriptState>(emptyTranscript)
  const [levels, setLevels] = useState<RecorderLevels>({ mic: 0, sys: 0 })
  const [error, setError] = useState('')

  // baseMS/startedAt 让计时器在两次状态事件之间也能走：状态事件只在开始/暂停/
  // 恢复/结束时才有，中间那几分钟没有任何事件，光靠它秒数会卡住不动。
  const baseMS = useRef(0)
  const startedAt = useRef(0)
  const [elapsedMS, setElapsedMS] = useState(0)

  const recording = info?.state === 'recording'
  const paused = info?.state === 'paused'

  const adopt = useCallback((audioMS: number, state: string) => {
    baseMS.current = audioMS
    startedAt.current = state === 'recording' ? Date.now() : 0
    setElapsedMS(audioMS)
  }, [])

  // pullStatus 取一次完整状态。
  //
  // 状态事件只带 id/state/audioMs——标题、音轨、目录只有这条命令有，所以换了一场
  // 录音就得重新取，否则录音窗标题会一直停在占位的「新录音」而不是「新录音 3」。
  //
  // 失败要重试。状态事件只在开始/暂停/结束那几个瞬间才有，中间几十分钟一条都没
  // 有——第一次取不到就意味着界面全程不知道正在录音（入口还可点、卡片不出现），
  // 要等下一次状态变化才自愈。实测重载页面时确实会撞上运行时尚未就绪的窗口。
  const pullStatus = useCallback(async () => {
    for (let i = 0; i < 6; i++) {
      try {
        const s = await recorderStatus()
        setInfo(s)
        adopt(s.audioMs, s.state)
        return
      } catch {
        await new Promise((r) => setTimeout(r, 300 * (i + 1)))
      }
    }
  }, [adopt])

  // 冷启动对齐一次：录音窗是被叫出来的，挂载时录音可能已经在跑了。
  useEffect(() => { void pullStatus() }, [pullStatus])

  const knownID = useRef('')
  useEffect(() => {
    const offState = onEvent(Events.RecorderState, (st) => {
      setError(st.error ?? '')
      adopt(st.audioMs, st.state)
      setInfo((prev) => {
        // 事件只带状态与时长，其余字段（标题、音轨、目录）留着上一次的。
        const base: RecordingInfo = prev ?? {
          id: st.id, title: '', room: '', state: st.state,
          startedAt: '', audioMs: st.audioMs,
        }
        return { ...base, id: st.id || base.id, state: st.state, audioMs: st.audioMs, uplink: st.uplink }
      })
      // 换了一场：补一次完整状态，并把上一场的字幕清掉。
      if (st.id && st.id !== knownID.current) {
        knownID.current = st.id
        setTranscript(emptyTranscript)
        void pullStatus()
      }
    })
    const offText = onEvent(Events.RecorderTranscript, (ev) => {
      setTranscript((s) => applyTranscript(s, ev))
    })
    const offLevel = onEvent(Events.RecorderLevel, (lv) => {
      setLevels({ mic: lv.mic, sys: lv.sys })
    })
    return () => { offState(); offText(); offLevel() }
  }, [adopt, pullStatus])

  // 计时器：只在录制中跑。暂停时 startedAt 归零，秒数就停在 baseMS 上。
  useEffect(() => {
    if (!recording) return
    const t = setInterval(() => {
      setElapsedMS(baseMS.current + (startedAt.current ? Date.now() - startedAt.current : 0))
    }, 200)
    return () => clearInterval(t)
  }, [recording])

  // 电平回落。没有新事件时按 LEVEL_DECAY 往下衰减到 0。
  const lastLevelAt = useRef(0)
  useEffect(() => { lastLevelAt.current = Date.now() }, [levels])
  useEffect(() => {
    if (!recording) { setLevels({ mic: 0, sys: 0 }); return }
    const t = setInterval(() => {
      if (Date.now() - lastLevelAt.current < LEVEL_IDLE_MS) return
      setLevels((l) => (l.mic === 0 && l.sys === 0 ? l : { mic: l.mic * LEVEL_DECAY, sys: l.sys * LEVEL_DECAY }))
    }, 100)
    return () => clearInterval(t)
  }, [recording])

  const start = useCallback(async (req?: StartRecordingRequest) => {
    setError('')
    setTranscript(emptyTranscript)
    const s = await startRecording(req ?? { title: '', lang: '', micDeviceId: '', sysDeviceId: '' })
    knownID.current = s.id
    setInfo(s)
    adopt(s.audioMs, s.state)
    return s
  }, [adopt])

  const stop = useCallback(async () => {
    const s = await stopRecording()
    setInfo(s)
    adopt(s.audioMs, s.state)
    return s
  }, [adopt])

  const discard = useCallback(async () => {
    await discardRecording()
    setInfo(null)
    setTranscript(emptyTranscript)
  }, [])

  return {
    info, recording, paused, elapsedMS, transcript, levels,
    uplink: info?.uplink ?? '', error,
    start,
    pause: () => pauseRecording(),
    resume: () => resumeRecording(),
    stop,
    discard,
  }
}
