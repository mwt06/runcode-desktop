// useAskpass 是 sudo 密码框的队列。
//
// 请求来自后端：sudo 要密码时，后端核验过来者（父进程确实是 sudo、会话刚批准过这条
// 命令）才会发 askpass:request；无论结果如何最后都会发 askpass:done 撤回。所以这里
// 只做队列与回答，不做任何判断——什么时候该弹、弹给谁，全由后端决定。
//
// 密码只从输入框经 answerAskpass 交回后端，不进任何 state 之外的地方；弹框关掉时
// 那份 state 随组件一起消失。
import { useCallback, useEffect, useState } from 'react'
import { answerAskpass, cancelAskpass, onEvent, type AskpassRequest } from '@/core/bridge'

export interface AskpassController {
  /** current 是眼下该弹的那一个（队首），没有则为 null。 */
  current: AskpassRequest | null
  /** remaining 是它后面还排着几个。 */
  remaining: number
  answer: (id: string, password: string) => void
  cancel: (id: string) => void
}

export function useAskpass(): AskpassController {
  const [queue, setQueue] = useState<AskpassRequest[]>([])

  useEffect(() => {
    const offReq = onEvent('askpass:request', (r) =>
      setQueue((q) => [...q.filter((x) => x.id !== r.id), r]),
    )
    // 超时、取消、sudo 被杀——后端撤回时一律关掉，免得留下一个不知道为谁而开的密码框。
    const offDone = onEvent('askpass:done', (d) => setQueue((q) => q.filter((x) => x.id !== d.id)))
    return () => {
      offReq()
      offDone()
    }
  }, [])

  const drop = useCallback((id: string) => setQueue((q) => q.filter((x) => x.id !== id)), [])

  const answer = useCallback((id: string, password: string) => {
    drop(id)
    answerAskpass(id, password).catch(() => {})
  }, [drop])

  const cancel = useCallback((id: string) => {
    drop(id)
    cancelAskpass(id).catch(() => {})
  }, [drop])

  return { current: queue[0] ?? null, remaining: Math.max(0, queue.length - 1), answer, cancel }
}
