import { useState } from 'react'

// usePersistentBool 是记在 localStorage 里的开关状态：侧栏折叠、写完自动预览等
// 都是"用户调过一次就该一直保持"的偏好。原先三处各写一遍 getItem/setItem，
// 现在读写规则(缺省值、'1'/'0' 编码)只有这一份。
export function usePersistentBool(key: string, fallback: boolean): [boolean, () => void] {
  const [value, setValue] = useState<boolean>(() => {
    const raw = localStorage.getItem(key)
    return raw == null ? fallback : raw === '1'
  })
  const toggle = () =>
    setValue((v) => {
      localStorage.setItem(key, v ? '0' : '1')
      return !v
    })
  return [value, toggle]
}

// usePersistentNumber 同上，但值由调用方在提交时显式写入(拖拽调宽这类连续变化
// 不该每一帧都落盘，所以 commit 与 setValue 分开)。
export function usePersistentNumber(
  key: string,
  parse: (stored: number) => number,
): [number, (v: number) => void, () => void] {
  const [value, setValue] = useState<number>(() => parse(Number(localStorage.getItem(key))))
  const commit = () => localStorage.setItem(key, String(value))
  return [value, setValue, commit]
}
