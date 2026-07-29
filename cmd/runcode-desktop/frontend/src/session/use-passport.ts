// usePassportStatus 暴露当前登录用户的通行证状态(未登录为 null)。开会话时取一次,并
// 订阅 passport:changed,登录/登出/换租户后自动跟新——欢迎语称呼(经 passportDisplayName)
// 与侧栏用户区(头像/用户名/退出登录)共用这一份订阅,避免各自重复拉取。
import { useEffect, useState } from 'react'
import { Events, onEvent, passportStatus, type PassportStatus } from '@/core/bridge'

export function usePassportStatus(): PassportStatus | null {
  const [status, setStatus] = useState<PassportStatus | null>(null)
  useEffect(() => {
    let alive = true
    passportStatus()
      .then((s) => { if (alive) setStatus(s) })
      .catch(() => { /* 取不到就当未登录,展示层自行降级 */ })
    // 事件负载本身就是最新的 PassportStatus,直接采纳,无需再查一次。
    const off = onEvent(Events.PassportChanged, (s) => setStatus(s))
    return () => { alive = false; off() }
  }, [])
  return status
}
