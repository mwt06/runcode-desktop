import { useState } from 'react'
import { resolvePermission, type PermissionRequest } from '@/core/bridge'

export type PermissionQueue = ReturnType<typeof usePermissionQueue>

// 授权请求队列，**每会话一条**。
//
// 并发工具（如并行的 WebFetch）可能各自弹一次授权。后端 Approver 已按 id 排队，
// 界面这边也排：只展示队首、解决后才轮到下一个，后来的请求不会顶掉屏幕上那个。
//
// 为什么按会话分：并行之后，B 会话弹出的请求若混进同一条队列，用户在 A 会话里
// 点的「允许」就会解到 B 头上。更要紧的是"看不见"——后台会话卡在等授权时界面
// 毫无表示，用户会以为它在跑。waiting 就是给会话列表打角标用的（P2）。
export function usePermissionQueue(focusedID: string) {
  const [queues, setQueues] = useState<Record<string, PermissionRequest[]>>({})

  const queue = queues[focusedID] ?? []
  const pending = queue[0] ?? null

  const patch = (sessionID: string, fn: (q: PermissionRequest[]) => PermissionRequest[]) =>
    setQueues((all) => {
      if (!sessionID) return all
      const next = fn(all[sessionID] ?? [])
      if (next.length === 0) {
        if (!(sessionID in all)) return all
        const copy = { ...all }
        delete copy[sessionID]
        return copy
      }
      return { ...all, [sessionID]: next }
    })

  // Enqueue (don't replace); dedup by id in case an event is delivered twice.
  const enqueue = (sessionID: string, req: PermissionRequest) =>
    patch(sessionID, (q) => (q.some((p) => p.id === req.id) ? q : [...q, req]))

  // 回合已结束：这条会话仍在排队的请求都已被后端拒掉（上下文取消 / DenyAll），
  // 丢弃陈旧弹窗。**只清这一条**——别的会话可能正等着用户回答。
  const clear = (sessionID: string) => patch(sessionID, () => [])

  const decide = async (decision: string) => {
    const cur = queue[0]
    if (!cur) return
    // Advance the queue: drop this request so the next one surfaces, then resolve it.
    patch(focusedID, (q) => q.filter((p) => p.id !== cur.id))
    try {
      await resolvePermission(focusedID, cur.id, decision)
    } catch {
      /* 已解决或取消 */
    }
  }

  // Deny every queued request at once — handy when a burst of concurrent tools each
  // raised a prompt and the user wants to reject them all. 只针对当前会话。
  const denyRest = async () => {
    const rest = queue
    patch(focusedID, () => [])
    for (const p of rest) {
      try {
        await resolvePermission(focusedID, p.id, 'deny')
      } catch {
        /* 已解决或取消 */
      }
    }
  }

  // waiting 是"每条会话还有几个在等"，给会话列表的角标用。
  const waiting: Record<string, number> = {}
  for (const [id, q] of Object.entries(queues)) {
    if (q.length > 0) waiting[id] = q.length
  }

  return { pending, remaining: queue.length - 1, waiting, enqueue, clear, decide, denyRest }
}
