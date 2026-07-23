import { useEffect, useRef, useState } from 'react'

export type Toast = ReturnType<typeof useToast>

// useToast 是输入框上方那条会自己消失的轻提示（如"暂无可压缩内容"）——它刻意不
// 进对话流，免得反复按同一个按钮在历史里堆一排。重复调用会重置计时，不会闪。
export function useToast(ms = 2600) {
  const [text, setText] = useState('')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current) }, [])
  const show = (next: string) => {
    setText(next)
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => setText(''), ms)
  }
  return { text, show }
}
