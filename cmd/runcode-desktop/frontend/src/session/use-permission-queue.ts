import { useState } from 'react'
import { resolvePermission, type PermissionRequest } from '@/core/bridge'

export type PermissionQueue = ReturnType<typeof usePermissionQueue>

// 并发工具（如并行的 WebFetch）可能各自弹一次授权。后端 Approver 已按 id 排队，
// 界面这边也排：只展示队首、解决后才轮到下一个，后来的请求不会顶掉屏幕上那个。
export function usePermissionQueue() {
  const [queue, setQueue] = useState<PermissionRequest[]>([])
  const pending = queue[0] ?? null

  // Enqueue (don't replace); dedup by id in case an event is delivered twice.
  const enqueue = (req: PermissionRequest) =>
    setQueue((q) => (q.some((p) => p.id === req.id) ? q : [...q, req]))

  // 回合已结束：仍在排队的请求都已被后端拒掉（上下文取消 / DenyAll），丢弃陈旧弹窗。
  const clear = () => setQueue([])

  const decide = async (decision: string) => {
    const cur = queue[0]
    if (!cur) return
    // Advance the queue: drop this request so the next one surfaces, then resolve it.
    setQueue((q) => q.filter((p) => p.id !== cur.id))
    try {
      await resolvePermission(cur.id, decision)
    } catch {
      /* 已解决或取消 */
    }
  }

  // Deny every queued request at once — handy when a burst of concurrent tools each
  // raised a prompt and the user wants to reject them all.
  const denyRest = async () => {
    const rest = queue
    setQueue([])
    for (const p of rest) {
      try {
        await resolvePermission(p.id, 'deny')
      } catch {
        /* 已解决或取消 */
      }
    }
  }

  return { pending, remaining: queue.length - 1, enqueue, clear, decide, denyRest }
}
