import { Icon, Logo } from '@/ui/icons'
import { DRAG, NO_DRAG } from '@/ui/tokens'

// TitleBar is the full-width top row (the frameless-window drag region): the XRUN
// wordmark on the left, an empty drag middle, and the window controls at the right.
export function TitleBar() {
  return (
    <div className="h-[38px] flex-none flex items-center pl-3.5 bg-surface border-b border-line2 select-none" style={DRAG}>
      <span className="flex items-center gap-2 font-semibold text-[13.5px] tracking-tight">
        <span className="w-[20px] h-[20px] inline-flex items-center justify-center"><Logo size={18} /></span>
        XRUN
      </span>
      <div className="flex-1" />
      <WindowControls />
    </div>
  )
}

// WindowControls is the minimize / maximize / close cluster, placed at the far
// right of whichever bar hosts it.
function WindowControls() {
  const rt = () => window.runtime
  return (
    <div className="flex items-center" style={NO_DRAG}>
      <button className="w-11 h-[34px] inline-flex items-center justify-center text-muted rounded-md hover:bg-surface2" title="最小化" onClick={() => rt().WindowMinimise()}>
        <Icon name="win-min" size={15} />
      </button>
      <button className="w-11 h-[34px] inline-flex items-center justify-center text-muted rounded-md hover:bg-surface2" title="最大化" onClick={() => rt().WindowToggleMaximise()}>
        <Icon name="win-max" size={13} />
      </button>
      <button className="w-11 h-[34px] inline-flex items-center justify-center text-muted rounded-md hover:bg-red hover:text-white" title="关闭" onClick={() => rt().Quit()}>
        <Icon name="win-close" size={15} />
      </button>
    </div>
  )
}
