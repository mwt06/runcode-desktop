// Toggle 是 iOS 风格的拨动开关：蓝色=开，灰色=关。点击不冒泡，方便放在整行可点
// 的卡片里而不触发行本身的跳转。
export function Toggle({ on, onChange, disabled }: { on: boolean; onChange: (next: boolean) => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      disabled={disabled}
      onClick={(e) => { e.stopPropagation(); onChange(!on) }}
      className={`relative inline-flex h-[24px] w-[42px] flex-none items-center rounded-full transition-colors ${on ? 'bg-primary' : 'bg-line2'} ${disabled ? 'opacity-40 cursor-default' : 'cursor-pointer'}`}
    >
      <span className={`inline-block h-[18px] w-[18px] transform rounded-full bg-white shadow transition-transform ${on ? 'translate-x-[21px]' : 'translate-x-[3px]'}`} />
    </button>
  )
}
