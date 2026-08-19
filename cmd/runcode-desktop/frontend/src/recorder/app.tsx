// 录音窗：设计稿里那个浮在所有应用之上的录音工具。
//
// 两个形态是同一个窗口改尺寸，不是两个窗口——切换时 WebView 不重建，所以计时、
// 字幕、电平都不会因为切一下形态就丢掉。
//
//   wide  工作区：标题、语言与保留音频两个开关、实时字幕、右侧状态栏、底部控制
//   mini  右下角浮窗：最新一句 + 电平 + 计时 + 暂停/结束
//
// 浮窗形态存在的全部理由是：用户切到会议软件之后还得看得见、点得到结束。
import { useEffect, useRef, useState } from 'react'
import { Icon } from '@/ui/icons'
import { DRAG, NO_DRAG } from '@/ui/tokens'
import { InlineError } from '@/ui/feedback'
import { errText, recorderSettings, saveRecorderSettings, type RecorderSettings } from '@/core/bridge'
import { useRecorder, type Recorder } from '@/session/use-recorder'
import { lastLine, timeline, TRACK_LABEL, type Segment } from './transcript'
import { LevelBar } from './level-bar'
import { hide, setMode, type RecorderMode } from './window-api'

// LANGS 是「自动识别 ∨」里的选项。空串 = 让服务端自己判。
const LANGS: { value: string; label: string }[] = [
  { value: '', label: '自动识别' },
  { value: 'zh', label: '中文' },
  { value: 'en', label: '英文' },
  { value: 'yue', label: '粤语' },
  { value: 'ja', label: '日语' },
  { value: 'ko', label: '韩语' },
]

// mmss 把毫秒格成 03:24；超过一小时补出小时位。
function mmss(ms: number): string {
  const total = Math.floor(Math.max(0, ms) / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const mm = String(m).padStart(2, '0')
  const ss = String(s).padStart(2, '0')
  return h > 0 ? `${h}:${mm}:${ss}` : `${mm}:${ss}`
}

export function RecorderApp() {
  const [mode, setLocalMode] = useState<RecorderMode>('wide')
  const [settings, setSettings] = useState<RecorderSettings | null>(null)
  const [err, setErr] = useState('')
  const rec = useRecorder()

  useEffect(() => {
    recorderSettings().then(setSettings).catch((e: unknown) => setErr(errText(e)))
  }, [])

  const switchTo = (next: RecorderMode) => {
    setMode(next).then(() => setLocalMode(next)).catch((e: unknown) => setErr(errText(e)))
  }

  // 改设置立刻落盘：这两个开关（语言、保留音频）下次开录还要用，留在内存里
  // 等于每场会都要重设一遍。
  const patch = (p: Partial<RecorderSettings>) => {
    if (!settings) return
    const next = { ...settings, ...p }
    setSettings(next)
    saveRecorderSettings(next).catch((e: unknown) => setErr(errText(e)))
  }

  const act = (fn: () => Promise<unknown>) => {
    setErr('')
    fn().catch((e: unknown) => setErr(errText(e)))
  }

  // 结束后把窗口收起来。录音结果由主窗那张卡片接手——两个窗口同时讲同一件事
  // 只会让人不知道该看哪个。
  const stop = () => act(() => rec.stop().then(() => hide()))
  const toggle = () => act(() => (rec.paused ? rec.resume() : rec.pause()))

  const shared = { rec, onToggle: toggle, onStop: stop, error: err || rec.error }

  return (
    <div className="h-screen w-screen bg-surface text-ink flex flex-col select-none overflow-hidden" style={DRAG}>
      {mode === 'mini' ? (
        <MiniPanel {...shared} onExpand={() => switchTo('wide')} />
      ) : (
        <WidePanel {...shared} settings={settings} onPatch={patch} onShrink={() => switchTo('mini')} />
      )}
    </div>
  )
}

interface PanelProps {
  rec: Recorder
  onToggle: () => void
  onStop: () => void
  error: string
}

function WidePanel(p: PanelProps & {
  settings: RecorderSettings | null
  onPatch: (patch: Partial<RecorderSettings>) => void
  onShrink: () => void
}) {
  const { rec } = p
  const lines = timeline(rec.transcript)
  const live = rec.recording || rec.paused

  return (
    <>
      <header className="px-6 pt-5 pb-3 flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="text-[20px] font-semibold truncate">{rec.info?.title || '新录音'}</div>
          <div className="mt-2 flex items-center gap-2 flex-wrap" style={NO_DRAG}>
            <span className="inline-flex items-center gap-1.5 pl-2.5 pr-1.5 py-1 rounded-full border border-line2 text-[12px] text-muted">
              <Icon name="globe" size={13} />
              <select
                className="bg-transparent outline-none cursor-pointer text-muted"
                value={p.settings?.lang ?? ''}
                onChange={(e) => p.onPatch({ lang: e.target.value })}
                title="识别语言"
              >
                {LANGS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
              </select>
            </span>
            <button
              className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border border-line2 text-[12px] text-muted hover:text-ink hover:border-primary transition"
              onClick={() => p.onPatch({ keepAudio: !p.settings?.keepAudio })}
              title="转写有缺口时，本地音频是唯一能补回来的依据"
            >
              <Icon name="file" size={13} />
              {p.settings?.keepAudio ? '保留音频' : '不保留音频'}
            </button>
          </div>
        </div>
        <button
          className="flex-none text-muted hover:text-ink p-1 rounded hover:bg-surface2"
          style={NO_DRAG}
          onClick={p.onShrink}
          title="缩为浮窗（留在右下角，浮在其他应用之上）"
        >
          <Icon name="shrink" size={16} />
        </button>
      </header>

      <div className="flex-1 grid grid-cols-[1fr_260px] min-h-0 border-t border-line">
        <TranscriptList lines={lines} recording={rec.recording} />
        <StatusColumn rec={rec} />
      </div>

      <footer className="px-6 py-3.5 border-t border-line" style={NO_DRAG}>
        {p.error && <InlineError variant="text" className="mb-2">{p.error}</InlineError>}
        <LevelBar mic={rec.levels.mic} sys={rec.levels.sys} active={rec.recording} />
        <div className="mt-2 flex items-center">
          <div className="w-20 text-[13px] tabular-nums text-muted">{mmss(rec.elapsedMS)}</div>
          <div className="flex-1 flex justify-center">
            <button
              className="w-10 h-10 rounded-full bg-surface2 hover:bg-inset flex items-center justify-center transition disabled:opacity-40"
              onClick={p.onToggle}
              disabled={!live}
              title={rec.paused ? '继续' : '暂停'}
            >
              <Icon name={rec.paused ? 'play' : 'pause'} size={16} />
            </button>
          </div>
          <div className="w-20 flex justify-end">
            <button
              className="px-3.5 py-1.5 rounded-md text-[13px] text-white bg-red hover:opacity-90 disabled:opacity-40"
              onClick={p.onStop}
              disabled={!live}
            >
              结束
            </button>
          </div>
        </div>
      </footer>
    </>
  )
}

function MiniPanel(p: PanelProps & { onExpand: () => void }) {
  const { rec } = p
  const line = lastLine(rec.transcript)
  const live = rec.recording || rec.paused

  return (
    <div className="h-full flex flex-col px-3.5 py-2.5">
      <div className="flex items-start gap-2">
        <div className="flex-1 min-w-0 text-[13px] leading-[1.45] line-clamp-2">
          {line || <span className="text-faint">{rec.recording ? '正在听…' : '未在录音'}</span>}
        </div>
        <button
          className="flex-none text-faint hover:text-ink p-0.5"
          style={NO_DRAG}
          onClick={p.onExpand}
          title="展开"
        >
          <Icon name="expand" size={13} />
        </button>
      </div>
      <div className="mt-1.5">
        <LevelBar mic={rec.levels.mic} sys={rec.levels.sys} active={rec.recording} height={16} />
      </div>
      <div className="mt-1.5 flex items-center gap-2" style={NO_DRAG}>
        <div className="text-[12px] tabular-nums text-muted">{mmss(rec.elapsedMS)}</div>
        <div className="flex-1 flex justify-center">
          <button
            className="w-7 h-7 rounded-full bg-surface2 hover:bg-inset flex items-center justify-center disabled:opacity-40"
            onClick={p.onToggle}
            disabled={!live}
            title={rec.paused ? '继续' : '暂停'}
          >
            <Icon name={rec.paused ? 'play' : 'pause'} size={12} />
          </button>
        </div>
        <button
          className="px-2.5 py-1 rounded text-[12px] text-white bg-red hover:opacity-90 disabled:opacity-40"
          onClick={p.onStop}
          disabled={!live}
        >
          结束
        </button>
      </div>
    </div>
  )
}

// TranscriptList 是实时字幕。它自己贴底滚动，但只在用户没往回翻的时候——
// 会议里回看刚才那句是常事，被强行拽回底部比不自动滚更让人恼火。
function TranscriptList({ lines, recording }: { lines: Segment[]; recording: boolean }) {
  const box = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)

  useEffect(() => {
    const el = box.current
    if (el && pinned.current) el.scrollTop = el.scrollHeight
  }, [lines])

  return (
    <div
      ref={box}
      className="overflow-y-auto px-6 py-4"
      onScroll={(e) => {
        const el = e.currentTarget
        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
      }}
    >
      {lines.length === 0 ? (
        <div className="h-full flex items-center justify-center text-[13px] text-faint text-center leading-[1.7]">
          {recording
            ? '正在听…说完一句会停一下，确认后的文字才落在这里'
            : '还没有开始录音'}
        </div>
      ) : (
        <div className="flex flex-col gap-3 max-w-[720px]">
          {lines.map((s) => (
            <div key={s.key} className="text-[14px] leading-[1.65]">
              <span
                className={`mr-2 text-[12px] ${s.track === 'mic' ? 'text-primaryink' : 'text-muted'}`}
                title={s.track === 'mic' ? '麦克风' : '系统声音（会议软件里对方的声音）'}
              >
                {s.speaker || TRACK_LABEL[s.track]}
              </span>
              <span className={s.live ? 'text-muted' : ''}>{s.text}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// StatusColumn 报告转写这一路的实时状态。
//
// 设计稿这个位置写的是「实时总结」。总结要走一次模型调用，那部分还没接上；在它
// 到位之前，把这块留给真正有用的信息：链路通没通、在等什么、已经认下来多少段。
// 这三件事恰好覆盖了用户录音过程中唯一会问的问题——「它到底在干活吗」。
function StatusColumn({ rec }: { rec: Recorder }) {
  const waits = Object.entries(rec.transcript.silence)
  const offline = rec.uplink === 'offline'

  return (
    <aside className="border-l border-line bg-inset p-4 overflow-y-auto">
      <div className="text-[12px] text-muted">实时状态</div>

      <div className="mt-3 flex items-center gap-2 text-[12px]">
        <span className={`w-1.5 h-1.5 rounded-full flex-none ${offline ? 'bg-red' : rec.uplink === 'connected' ? 'bg-primary' : 'bg-line2'}`} />
        <span className={offline ? 'text-red' : 'text-ink'}>
          {offline ? '离线录制中' : rec.uplink === 'connected' ? '转写已连接' : '正在连接转写服务'}
        </span>
      </div>
      {offline && (
        <div className="mt-1.5 text-[11px] text-muted leading-[1.6]">
          本地照常录音，但这段时间的文字会缺。结束后可以拿本地音频补一次。
        </div>
      )}

      <div className="mt-4 text-[12px] text-muted">已确认 {rec.transcript.finals.length} 段</div>

      {waits.map(([track, w]) => (
        <div key={track} className="mt-2 text-[11px] text-faint leading-[1.6]">
          {TRACK_LABEL[track] ?? track}：还需静音 {Math.max(0, w.need - w.silence)} 秒出确认文本
        </div>
      ))}

      {rec.paused && <div className="mt-4 text-[12px] text-amberink">已暂停，计时与录音都停住了</div>}
    </aside>
  )
}

