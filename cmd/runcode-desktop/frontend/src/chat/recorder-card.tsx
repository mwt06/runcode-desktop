// 对话流里的录音卡片，两个形态：
//
//   LiveRecorderCard    录着的时候。每秒都在变，所以它是个**临时**组件，钉在
//                       对话末尾，录完就消失。
//   RecordingCard       录完之后。它是一条真正的对话块（Block），发起纪要的那一刻
//                       钉在历史里——换条对话看不见，恢复历史时原样回来。
//
// 分成两个而不是一个组件按状态切换，是因为这两件事的生命周期根本不同：前者属于
// 「此刻」，后者属于「那条对话」。早先做成一个，结果就是录完之后那张卡片一直浮在
// 界面上，换到别的对话它还在。
import { Icon } from '@/ui/icons'
import { Banner } from '@/ui/feedback'
import { revealInFolder } from '@/core/bridge'
import { lastLine } from '@/recorder/transcript'
import { LevelBar } from '@/recorder/level-bar'
import { type RecordingMark } from '@/recorder/minutes'
import { type Recorder } from '@/session/use-recorder'

function mmss(ms: number): string {
  const total = Math.floor(Math.max(0, ms) / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

// duration 是录完之后那句人话时长，与录着时的计时器分工不同：录的时候要精确到秒，
// 录完之后「12 分 52 秒」比 12:52 更适合被读出来。
function duration(ms: number): string {
  const total = Math.round(Math.max(0, ms) / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return m > 0 ? `${m} 分 ${s} 秒` : `${s} 秒`
}

const SHELL = 'rounded-xl border border-line2 bg-surface overflow-hidden max-w-[560px]'

export function LiveRecorderCard({ rec, onOpenWindow }: { rec: Recorder; onOpenWindow: () => void }) {
  const info = rec.info
  // 只画「正在录」（含收尾那几秒）。真正录完那一刻它才消失，接手的是历史里的
  // RecordingCard。
  //
  // 收尾也要画：按下结束之后，服务端还要几秒把最后一块刷出来。少了这一段，卡片
  // 当场消失、纪要要等几秒才冒出来，中间那几秒界面上什么都没有。
  if (!info || !(rec.recording || rec.paused || rec.stopping)) return null
  const line = lastLine(rec.transcript)

  return (
    <div className={SHELL}>
      <div className="flex items-center gap-2 px-4 pt-3 pb-2">
        <span className="text-primaryink flex-none"><Icon name="mic" size={15} /></span>
        <span className="text-[13px] font-medium flex-1 min-w-0 truncate">{info.title || '录音纪要'}</span>
        <span className="text-[12px] tabular-nums text-muted flex-none">{mmss(rec.elapsedMS)}</span>
      </div>

      <div className="px-4">
        <LevelBar mic={rec.levels.mic} sys={rec.levels.sys} active={rec.recording} height={20} />
      </div>
      <div className="px-4 pt-1.5 pb-3 text-[13px] leading-[1.6] text-ink min-h-[38px]">
        {rec.stopping
          ? <span className="text-faint">正在收尾：录音已停，正在等服务端把最后一句刷出来并校正前面的文字。</span>
          : line || <span className="text-faint">{rec.paused ? '已暂停' : '正在听…'}</span>}
      </div>
      {rec.uplink === 'offline' && (
        <div className="px-4 pb-3">
          <Banner tone="warning">离线录制中：本地照常录音，但这段时间的文字会缺。</Banner>
        </div>
      )}
      <div className="flex items-center gap-2 px-4 py-2.5 border-t border-line bg-inset">
        <button className="text-[12px] text-muted hover:text-ink" onClick={onOpenWindow}>
          打开录音窗
        </button>
        <div className="flex-1" />
        <button
          className="text-[12px] text-muted hover:text-ink px-2 py-1 rounded hover:bg-surface2 disabled:opacity-40"
          onClick={() => void (rec.paused ? rec.resume() : rec.pause())}
          disabled={rec.stopping}
        >
          {rec.paused ? '继续' : '暂停'}
        </button>
        <button
          className="px-2.5 py-1 rounded text-[12px] text-white bg-red hover:opacity-90 disabled:opacity-40"
          onClick={() => void rec.stop()}
          disabled={rec.stopping}
        >
          {rec.stopping ? '收尾中…' : '结束录音转写'}
        </button>
      </div>
    </div>
  )
}

export function RecordingCard({ mark, onGenerateMinutes }: {
  mark: RecordingMark
  // onGenerateMinutes 重来一次。自动那次可能撞上没有会话，或者纪要本身答得不好。
  onGenerateMinutes?: (mark: RecordingMark) => void
}) {
  return (
    <div className={SHELL}>
      <div className="flex items-center gap-2 px-4 pt-3 pb-2">
        <span className="text-primaryink flex-none"><Icon name="mic" size={15} /></span>
        <span className="text-[13px] font-medium flex-1 min-w-0 truncate">{mark.title || '录音纪要'}</span>
        <span className="text-[12px] tabular-nums text-muted flex-none">{mmss(mark.audioMs)}</span>
      </div>

      <div className="px-4 pb-3 text-[13px] text-muted">录音已结束 · {duration(mark.audioMs)}</div>

      {mark.needsBackfill && (
        <div className="px-4 pb-3">
          <Banner tone="warning" title="转写有缺口">
            录音期间与转写服务断开过，有一段没有文字。本地音频还在，可以之后补一次。
          </Banner>
        </div>
      )}

      <div className="flex items-center gap-3 px-4 py-2.5 border-t border-line bg-inset">
        {mark.dir && (
          <button
            className="inline-flex items-center gap-1.5 text-[12px] text-muted hover:text-ink"
            onClick={() => void revealInFolder(mark.dir ?? '')}
          >
            <Icon name="folder" size={13} />
            在文件夹中显示
          </button>
        )}
        <div className="flex-1" />
        {mark.transcript && onGenerateMinutes && (
          <button
            className="px-2.5 py-1 rounded text-[12px] text-primaryink border border-primary hover:bg-primarysoft"
            onClick={() => onGenerateMinutes(mark)}
            title="把这场录音的转写重新交给模型整理"
          >
            重新生成纪要
          </button>
        )}
      </div>
    </div>
  )
}
