export const PICKER_DISMISS_DELAY = 200

// 跨过入口与面板之间的空隙不应误关；连续移出也不能无限推迟关闭。
export function createPickerDismiss(onClose: () => void) {
  let timer: ReturnType<typeof setTimeout> | undefined
  const cancel = () => {
    if (timer !== undefined) clearTimeout(timer)
    timer = undefined
  }
  const close = () => { cancel(); onClose() }
  const schedule = () => {
    if (timer === undefined) timer = setTimeout(close, PICKER_DISMISS_DELAY)
  }
  return { cancel, close, schedule }
}
