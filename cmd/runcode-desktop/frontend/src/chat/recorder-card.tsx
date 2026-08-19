// 对话流里的录音卡片。
//
// 它不是一条会话消息，而是钉在对话末尾的实时组件——录音每秒都在变，走会话
// 消息那条路等于每秒往历史里塞一条。设计稿把它画在对话流里是因为用户是从这里
// 发起的录音，结果也该回到这里。
import { Icon } from '@/ui/icons'
import { Banner } from '@/ui/feedback'
import { revealInFolder, type RecordingInfo } from '@/core/bridge'
import { lastLine } from '@/recorder/transcript'
import { LevelBar } from '@/recorder/level-bar'
import { type Recorder } from '@/session/use-recorder'

function mmss(ms: number): string {
  const total = Math.floor(Math.max(0, ms) / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

// duration 是结束后那句人话时长，与卡片顶上的计时器分工不同：录着的时候要
// 精确到秒，录完之后「12 分 52 秒」比 12:52 更适合被读出来。
function duration(ms: number): string {
  const total = Math.round(Math.max(0, ms) / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return m > 0 ? `${m} 分 ${s} 秒` : `${s} 秒`
}

export function RecorderCard({ rec, onOpenWindow, onGenerateMinutes }: {
  rec: Recorder
  onOpenWindow: () => void
  // onGenerateMinutes 是「生成纪要」。录完会自动走一次，这个按钮用来重来一次
  // ——第一次可能撞上没有会话、或者模型答得不好。
  onGenerateMinutes?: (info: RecordingInfo) => void
}) {
  const info = rec.info
  if (!info || info.state === 'idle') return null

  const live = rec.recording || rec.paused
  const line = lastLine(rec.transcript)

  return (
    <div className="rounded-xl border border-line2 bg-surface overflow-hidden max-w-[560px]">
      <div className="flex items-center gap-2 px-4 pt-3 pb-2">
        <span className="text-primaryink flex-none"><Icon name="mic" size={15} /></span>
        <span className="text-[13px] font-medium flex-1 min-w-0 truncate">{info.title || '录音纪要'}</span>
        <span className="text-[12px] tabular-nums text-muted flex-none">{mmss(rec.elapsedMS)}</span>
      </div>

      {live ? (
        <>
          <div className="px-4">
            <LevelBar mic={rec.levels.mic} sys={rec.levels.sys} active={rec.recording} height={20} />
          </div>
          <div className="px-4 pt-1.5 pb-3 text-[13px] leading-[1.6] text-ink min-h-[38px]">
            {line || <span className="text-faint">{rec.paused ? '已暂停' : '正在听…'}</span>}
          </div>
          {rec.uplink === 'offline' && (
            <div className="px-4 pb-3">
              <Banner tone="warning">离线录制中：本地照常录音，但这段时间的文字会缺。</Banner>
            </div>
          )}
          <div className="flex items-center gap-2 px-4 py-2.5 border-t border-line bg-inset">
            <button
              className="text-[12px] text-muted hover:text-ink"
              onClick={onOpenWindow}
            >
              打开录音窗
            </button>
            <div className="flex-1" />
            <button
              className="text-[12px] text-muted hover:text-ink px-2 py-1 rounded hover:bg-surface2"
              onClick={() => void (rec.paused ? rec.resume() : rec.pause())}
            >
              {rec.paused ? '继续' : '暂停'}
            </button>
            <button
              className="px-2.5 py-1 rounded text-[12px] text-white bg-red hover:opacity-90"
              onClick={() => void rec.stop()}
            >
              结束录音转写
            </button>
          </div>
        </>
      ) : (
        <>
          <div className="px-4 pb-3 text-[13px] text-muted">
            录音已结束 · {duration(info.audioMs)}
          </div>
          {info.needsBackfill && (
            <div className="px-4 pb-3">
              <Banner tone="warning" title="转写有缺口">
                录音期间与转写服务断开过，有一段没有文字。本地音频还在，可以之后补一次。
              </Banner>
            </div>
          )}
          {info.dir && (
            <div className="flex items-center gap-3 px-4 py-2.5 border-t border-line bg-inset">
              <button
                className="inline-flex items-center gap-1.5 text-[12px] text-muted hover:text-ink"
                onClick={() => void revealInFolder(info.dir ?? '')}
              >
                <Icon name="folder" size={13} />
                在文件夹中显示
              </button>
              <div className="flex-1" />
              {info.transcript && onGenerateMinutes && (
                <button
                  className="px-2.5 py-1 rounded text-[12px] text-primaryink border border-primary hover:bg-primarysoft"
                  onClick={() => onGenerateMinutes(info)}
                  title="把这场录音的转写交给模型整理成会议纪要"
                >
                  生成纪要
                </button>
              )}
            </div>
          )}
        </>
      )}
    </div>
  )
}
