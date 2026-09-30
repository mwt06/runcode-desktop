import { useEffect, useRef } from 'react'
import { createActionScope } from './action-scope'

export function useActionScope(sessionId: string) {
  const ref = useRef(createActionScope())
  const scope = ref.current
  scope.focus(sessionId)
  // StrictMode 会立即重放 setup/cleanup；暂时停用而不是销毁代际，避免吞掉已认领的录音。
  useEffect(() => {
    scope.activate()
    return () => scope.deactivate()
  }, [scope])
  return scope
}
