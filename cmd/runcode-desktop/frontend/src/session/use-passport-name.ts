// usePassportName 暴露当前登录用户的展示名(未登录为空串),给欢迎语等处用。开会话
// 时取一次,并订阅 passport:changed,登录/登出/换租户后自动跟新。取名口径见
// passportDisplayName。
import { useEffect, useState } from 'react'
import { Events, onEvent, passportStatus, type PassportStatus } from '@/core/bridge'
import { passportDisplayName } from '@/core/passport-account'

export function usePassportName(): string {
  const [status, setStatus] = useState<PassportStatus | null>(null)
  useEffect(() => {
    let alive = true
    passportStatus()
      .then((s) => { if (alive) setStatus(s) })
      .catch(() => { /* 取不到就当未登录,欢迎语降级为不带名字 */ })
    // 事件负载本身就是最新的 PassportStatus,直接采纳,无需再查一次。
    const off = onEvent(Events.PassportChanged, (s) => setStatus(s))
    return () => { alive = false; off() }
  }, [])
  return passportDisplayName(status)
}
