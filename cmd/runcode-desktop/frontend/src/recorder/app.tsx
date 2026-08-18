// 录音窗的界面。
//
// 当前是 Wails v3 双窗迁移的验证版：它证明的是**窗口机制**成立——第二个窗口
// 能渲染、能无边框拖动、能浮在其他应用之上、能在大窗与右下角浮窗之间切换，
// 且切换时 WebView 不重建（看那个一直在走的计时器）。
//
// 真正的录音界面（实时字幕、实时总结栏、自动识别/翻译/保留音频三个下拉）
// 接在采集层之后再补，形状照设计稿。
import { useEffect, useState } from 'react'
import { DRAG, NO_DRAG } from '@/ui/tokens'
import { hide, setMode, type RecorderMode } from './window-api'

// mmss 把秒数格成 03:24，与设计稿上的计时一致。
function mmss(total: number): string {
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

export function RecorderApp() {
  const [mode, setLocalMode] = useState<RecorderMode>('wide')
  const [seconds, setSeconds] = useState(0)
  const [err, setErr] = useState('')

  // 这个计时器就是验证点：切换形态是同一个窗口改尺寸，WebView 不重建,
  // 所以秒数会一直往上走而不是归零。
  useEffect(() => {
    const t = setInterval(() => setSeconds((n) => n + 1), 1000)
    return () => clearInterval(t)
  }, [])

  // 验证脚手架：?spike=mini 时挂载后自动切一次浮窗。
  //
  // 用它而不是让人去点，是为了能在无人值守下验证「第二个窗口的 JS 能调到
  // Go 绑定」这条链路——尤其是 window-api.ts 里那个手写的 main.RecorderWindow
  // 全限定名对不对。接上真正的录音入口后连同 main.go 里的开关一起删。
  useEffect(() => {
    if (new URLSearchParams(location.search).get('spike') !== 'mini') return
    const t = setTimeout(() => {
      setMode('mini')
        .then(() => setLocalMode('mini'))
        .catch((e: unknown) => setErr(`绑定调用失败：${String(e)}`))
    }, 2000)
    return () => clearTimeout(t)
  }, [])

  const switchTo = (next: RecorderMode) => {
    setMode(next)
      .then(() => setLocalMode(next))
      .catch((e: unknown) => setErr(String(e)))
  }

  return (
    <div className="h-screen w-screen bg-surface text-ink flex flex-col select-none" style={DRAG}>
      {mode === 'mini' ? (
        <MiniPanel seconds={seconds} onExpand={() => switchTo('wide')} onStop={() => void hide()} />
      ) : (
        <WidePanel seconds={seconds} onShrink={() => switchTo('mini')} onStop={() => void hide()} />
      )}
      {err && (
        <div className="px-4 py-2 text-[11px] text-red border-t border-line" style={NO_DRAG}>
          {err}
        </div>
      )}
    </div>
  )
}

function WidePanel(p: { seconds: number; onShrink: () => void; onStop: () => void }) {
  return (
    <>
      <div className="px-6 pt-5 pb-3 border-b border-line">
        <div className="text-[20px] font-semibold">新录音 1</div>
        <div className="mt-1 text-[12px] text-muted">
          Wails v3 双窗验证 · 本窗独立于主窗，且置顶于其他应用
        </div>
      </div>

      <div className="flex-1 grid grid-cols-[1fr_260px] min-h-0">
        <div className="p-6 flex flex-col items-center justify-center gap-3">
          <div className="text-[40px] font-semibold tabular-nums">{mmss(p.seconds)}</div>
          <div className="text-[12px] text-muted text-center">
            切到浮窗再切回来，这个计时不会归零
            <br />
            ——两个形态是同一个窗口改尺寸，WebView 不重建
          </div>
        </div>
        <div className="border-l border-line p-4 bg-inset">
          <div className="text-[12px] text-muted">实时总结</div>
          <div className="mt-3 space-y-2">
            <div className="h-3 rounded bg-surface2" />
            <div className="h-3 rounded bg-surface2 w-4/5" />
            <div className="h-3 rounded bg-surface2 w-3/5" />
          </div>
          <div className="mt-3 text-[11px] text-faint">正在识别说话人和讨论内容…</div>
        </div>
      </div>

      <div className="px-6 py-4 border-t border-line flex items-center gap-3" style={NO_DRAG}>
        <button
          className="px-3 py-1.5 rounded-md text-[13px] bg-surface2 hover:bg-inset"
          onClick={p.onShrink}
        >
          缩小为浮窗
        </button>
        <div className="flex-1" />
        <button
          className="px-3 py-1.5 rounded-md text-[13px] text-white bg-red hover:opacity-90"
          onClick={p.onStop}
        >
          结束
        </button>
      </div>
    </>
  )
}

function MiniPanel(p: { seconds: number; onExpand: () => void; onStop: () => void }) {
  return (
    <div className="h-full flex flex-col px-4 py-3">
      <div className="flex items-start">
        <div className="flex-1 text-[13px] truncate">我，那我。</div>
        <button
          className="text-[11px] text-muted hover:text-ink px-1"
          style={NO_DRAG}
          onClick={p.onExpand}
          title="展开"
        >
          ⤢
        </button>
      </div>
      <div className="flex-1" />
      <div className="flex items-center gap-3" style={NO_DRAG}>
        <div className="text-[13px] tabular-nums text-muted">{mmss(p.seconds)}</div>
        <div className="flex-1" />
        <button
          className="px-2.5 py-1 rounded text-[12px] text-white bg-red hover:opacity-90"
          onClick={p.onStop}
        >
          结束
        </button>
      </div>
    </div>
  )
}
