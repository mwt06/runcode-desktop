import { useState } from 'react'
import { Icon, Logo } from '@/ui/icons'
import { BRAND } from '@/core/brand'
import { DRAG, NO_DRAG } from '@/ui/tokens'
import { closeMainWindow, errText } from '@/core/bridge'
import { Window } from '@wailsio/runtime'

// TitleBar is the full-width top row (the frameless-window drag region): the
// brand wordmark on the left, an empty drag middle, and the window controls at
// the right.
export function TitleBar({ onError }: { onError?: (message: string) => void }) {
  return (
    <div className="h-[38px] flex-none flex items-center pl-3.5 bg-surface border-b border-line2 select-none" style={DRAG}>
      <span className="flex items-center gap-2 font-semibold text-[14px] tracking-tight">
        <span className="w-[20px] h-[20px] inline-flex items-center justify-center"><Logo size={18} /></span>
        {BRAND.name}
      </span>
      <div className="flex-1" />
      <WindowControls onError={onError} />
    </div>
  )
}

// WindowControls is the minimize / maximize / close cluster, placed at the far
// right of whichever bar hosts it.
function WindowControls({ onError }: { onError?: (message: string) => void }) {
  const [closing, setClosing] = useState(false)
  async function close() {
    if (closing) return
    setClosing(true)
    try {
      await closeMainWindow()
    } catch (e) {
      onError?.(errText(e))
    } finally {
      // closeMainWindow 只等到原生确认框创建完成；取消或隐藏后都必须恢复按钮，
      // 否则窗口从托盘恢复时标题栏 X 会永久禁用。
      setClosing(false)
    }
  }
  return (
    <div className="flex items-center" style={NO_DRAG}>
      <button className="w-11 h-[34px] inline-flex items-center justify-center text-muted rounded-md hover:bg-surface2" title="最小化" onClick={() => void Window.Minimise()}>
        <Icon name="win-min" size={15} />
      </button>
      <button className="w-11 h-[34px] inline-flex items-center justify-center text-muted rounded-md hover:bg-surface2" title="最大化" onClick={() => void Window.ToggleMaximise()}>
        <Icon name="win-max" size={13} />
      </button>
      <button
        className="w-11 h-[34px] inline-flex items-center justify-center text-muted rounded-md hover:bg-red hover:text-white disabled:opacity-50"
        title="关闭"
        disabled={closing}
        onClick={() => void close()}
      >
        <Icon name="win-close" size={15} />
      </button>
    </div>
  )
}
