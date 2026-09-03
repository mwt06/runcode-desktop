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
import { ConfirmDialog } from '@/ui/confirm-dialog'
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
  const [askClose, setAskClose] = useState(false)
  const rec = useRecorder()

  useEffect(() => {
    recorderSettings().then(setSettings).catch((e: unknown) => setErr(errText(e)))
  }, [])

  // switchTo 返回 promise：换形态之后还要接着做别的事的地方（比如"先展开再弹确认框"）
  // 得等窗口真的变完，不然确认框会先画在 320×148 的浮窗里。
  const switchTo = (next: RecorderMode) => setMode(next).then(() => setLocalMode(next))

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

  // closeWindow 收起录音窗，并把形态复位成大窗。
  //
  // 复位这一步不能省：Go 侧的 Show() 一律按大窗尺寸显示，本地 mode 若停在 mini，
  // 下次打开就是「大窗的尺寸里画着浮窗那三个控件」。
  const closeWindow = () => hide().then(() => setLocalMode('wide'))

  // 结束后把窗口收起来。录音结果由主窗那张卡片接手——两个窗口同时讲同一件事
  // 只会让人不知道该看哪个。
  //
  // **失败不能把用户困在一个关不掉的浮窗里。** StopRecording 在返回错误之前就已经
  // 把这一场收尾了（先 finish 再报错），所以错误说的是收尾过程中的问题（典型是
  // 关闭上行链路超时），不是"没停下来"。原先失败时 .then 整条链断掉：窗口不收、
  // 浮窗又不显示错误，屏幕上就留下一个按了结束也没反应、也关不掉的小条。
  // 现在失败时展开成大窗并把错误摆出来——用户看得见发生了什么，也点得到关闭。
  const stop = () => {
    setErr('')
    // 收窗本身在 rec.stop() 里做（主窗那张卡片上的「结束」走同一条路），这里只把
    // 形态复位：Go 的 Show() 一律按大窗尺寸显示，本地 mode 停在 mini 的话，下次
    // 打开就是「大窗的尺寸里画着浮窗那三个控件」。
    rec.stop().then(() => setLocalMode('wide')).catch((e: unknown) => {
      setErr(errText(e))
      void switchTo('wide') // SetMode 末尾会 Show()，窗口带着错误重新出现
    })
  }
  const toggle = () => act(() => (rec.paused ? rec.resume() : rec.pause()))

  const live = rec.recording || rec.paused

  // requestClose 是两个形态里那个关闭按钮。没在录音就直接收起；正在录音时先问一句
  // ——一个叫「关闭」的按钮如果让录音在看不见的地方继续跑（麦克风还开着），
  // 是这里最坏的结果。要「关掉窗口但继续录」的人应该用旁边的缩为浮窗。
  //
  // 浮窗形态下先展开再问：确认框宽 400、连标题带按钮两百多高，塞进 320×148 的
  // 浮窗里按钮会被切在窗外——那就成了一个问了却答不了的问题。
  const requestClose = () => {
    if (!live) return act(closeWindow)
    if (mode === 'mini') return act(() => switchTo('wide').then(() => setAskClose(true)))
    setAskClose(true)
  }

  const shared = { rec, onToggle: toggle, onStop: stop, onClose: requestClose, error: err || rec.error }

  return (
    <div className="h-screen w-screen bg-surface text-ink flex flex-col select-none overflow-hidden" style={DRAG}>
      {mode === 'mini' ? (
        <MiniPanel {...shared} onExpand={() => act(() => switchTo('wide'))} />
      ) : (
        <WidePanel
          {...shared}
          settings={settings}
          onPatch={patch}
          onShrink={() => act(() => switchTo('mini'))}
        />
      )}
      {askClose && (
        // 弹窗要显式 no-drag：DRAG 是 CSS 自定义属性，会从根节点继承下来，
        // 不挡住的话点按钮变成拖窗口。
        <div style={NO_DRAG}>
          <ConfirmDialog
            title="正在录音"
            message="关闭窗口会先结束这场录音。已经录到的内容会照常收尾，不会丢。想让它继续录又不占屏幕，用旁边的「缩为浮窗」。"
            confirmLabel="结束并关闭"
            onConfirm={() => { setAskClose(false); stop() }}
            onCancel={() => setAskClose(false)}
          />
        </div>
      )}
    </div>
  )
}

interface PanelProps {
  rec: Recorder
  onToggle: () => void
  onStop: () => void
  // onClose 是"把这个窗口收掉"。两个形态都必须有——浮窗少了它就可能变成一个
  // 关不掉的小条：录音已经自己结束时「结束」是禁用的，那时它是唯一的出口。
  onClose: () => void
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
        <div className="flex-none flex items-center gap-0.5" style={NO_DRAG}>
          <button
            className="text-muted hover:text-ink p-1 rounded hover:bg-surface2"
            onClick={p.onShrink}
            title="缩为浮窗（留在右下角，浮在其他应用之上）"
          >
            <Icon name="shrink" size={16} />
          </button>
          <button
            className="text-muted hover:text-ink p-1 rounded hover:bg-surface2"
            onClick={p.onClose}
            title="关闭录音窗"
          >
            <Icon name="win-close" size={16} />
          </button>
        </div>
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
        <div className="flex-1 min-w-0 text-[13px] leading-[1.45] line-clamp-2" title={p.error || undefined}>
          {/* 出错时错误顶掉字幕行。浮窗只有两行的地方，而"链路断了"「结束失败」
              这类事不说出来，用户只会看到一个不动的小条，不知道它已经不在录了。 */}
          {p.error
            ? <InlineError variant="text" className="line-clamp-2">{p.error}</InlineError>
            : line || <span className="text-faint">{rec.recording ? '正在听…' : rec.stopping ? '正在收尾…' : '未在录音'}</span>}
        </div>
        <div className="flex-none flex items-center gap-0.5" style={NO_DRAG}>
          <button className="text-faint hover:text-ink p-0.5" onClick={p.onExpand} title="展开">
            <Icon name="expand" size={13} />
          </button>
          {/* 关闭。浮窗一度没有它，于是"录音已经自己结束了"的时候（「结束」按钮
              是禁用的）这个小条就再也关不掉了。 */}
          <button className="text-faint hover:text-red p-0.5" onClick={p.onClose} title="关闭录音窗">
            <Icon name="win-close" size={13} />
          </button>
        </div>
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

      {/* 收尾这几秒必须说出来：采集已经停了，但服务端还在把最后一块刷出来，
          同时补发精修过的确认段——字幕会继续变。不说明的话，用户看到的是
          「按了结束，它又重新识别了一遍」。 */}
      {rec.stopping && (
        <div className="mt-4 text-[12px] text-muted leading-[1.6]">
          正在收尾：录音已停，正在等服务端把最后一句刷出来并校正前面的文字。
        </div>
      )}
    </aside>
  )
}

