import { useEffect, useMemo, useRef, type PointerEvent as ReactPointerEvent } from 'react'
import { isComposingKey } from '@/ui/keys'
import { createPickerDismiss } from './picker-dismiss'

export function usePickerDismiss(activeId: string, onClose: () => void) {
  const regionRef = useRef<HTMLDivElement>(null)
  const dismiss = useMemo(() => createPickerDismiss(onClose), [onClose])

  useEffect(() => {
    if (!activeId) return
    const outside = (e: PointerEvent) => {
      if (!(e.target instanceof Node) || !regionRef.current?.contains(e.target)) dismiss.close()
    }
    const escape = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !isComposingKey({ nativeEvent: e, keyCode: e.keyCode })) dismiss.close()
    }
    // 不加遮罩、不拦默认行为：点击输入框应当一边关菜单、一边正常落下光标。
    document.addEventListener('pointerdown', outside, true)
    document.addEventListener('keydown', escape)
    window.addEventListener('blur', dismiss.close)
    return () => {
      dismiss.cancel()
      document.removeEventListener('pointerdown', outside, true)
      document.removeEventListener('keydown', escape)
      window.removeEventListener('blur', dismiss.close)
    }
  }, [activeId, dismiss])

  // 点击边界与鼠标活动区可以分开：补全只在点击候选项时保持打开，
  // 鼠标则允许在输入框与候选面板之间来回；场景菜单把二者挂在同一区域。
  return {
    regionRef,
    keepOpen: dismiss.cancel,
    pointerProps: {
      onPointerEnter: dismiss.cancel,
      onPointerLeave: (e: ReactPointerEvent<HTMLElement>) => {
        if (activeId && e.pointerType !== 'touch') dismiss.schedule()
      },
    },
  }
}
