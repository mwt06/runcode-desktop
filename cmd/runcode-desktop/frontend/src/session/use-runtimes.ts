// useRuntimes 是「运行时环境」（Python / Node / Git）在前端的唯一状态源。
//
// 与 useUpdate 同一套路，理由也同：它是**后端那台状态机的镜子**，不是第二台。
// Go 侧每次变化推整份 RuntimeInfo（runtimes:status），这里只收下、连同四个动作
// 一起交出去；「装到哪一步了」「能不能点安装」一概不在前端推导——两份各自演进的
// 状态机正是「明明装好了按钮还在转」这类只在慢网络上复现的缺陷的来源。
//
// 检查也不由前端发起：启动后 Go 侧会先探一遍系统、再查平台清单（延后几秒，失败
// 不打扰）。这里只在挂载时读一次，好让设置页一打开就有东西可画。
import { useCallback, useEffect, useState } from 'react'
import {
  authorizeRuntime,
  cancelRuntimeInstall,
  checkRuntimes,
  errText,
  installRuntime,
  onEvent,
  removeRuntime,
  runtimeStatus,
  RuntimeStages,
  type RuntimeInfo,
  type RuntimePack,
} from '@/core/bridge'

export interface RuntimeController {
  /** info 为 null 表示还没读到后端状态（挂载后的头一瞬）。 */
  info: RuntimeInfo | null
  packs: RuntimePack[]
  /** checking 是「正在查平台清单」。 */
  checking: boolean
  /** error 只兜住命令通道本身的异常；业务失败的原因在 info.error / pack.error 里。 */
  error: string
  check: () => void
  install: (id: string) => void
  /** authorize 请麒麟安全中心放行一个已装好的运行时（会弹一次应用的密码框）。 */
  authorize: (id: string) => void
  cancel: (id: string) => void
  remove: (id: string) => void
}

/** packBusy 是「这一行正在动」——安装按钮据此变成进度条。 */
export function packBusy(p: RuntimePack): boolean {
  return (
    p.stage === RuntimeStages.Downloading ||
    p.stage === RuntimeStages.Verifying ||
    p.stage === RuntimeStages.Extracting ||
    p.stage === RuntimeStages.Authorizing
  )
}

export function useRuntimes(): RuntimeController {
  const [info, setInfo] = useState<RuntimeInfo | null>(null)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    // 先订阅再读取。反过来的话，恰好落在「读完」与「订阅上」之间的那一次推送会
    // 丢掉——而那正是启动自动检查出结果的时刻。
    const off = onEvent('runtimes:status', setInfo)
    let alive = true
    runtimeStatus()
      .then((first) => {
        if (alive) setInfo((cur) => cur ?? first)
      })
      .catch(() => {})
    return () => {
      alive = false
      off()
    }
  }, [])

  const check = useCallback(() => {
    setError('')
    setChecking(true)
    checkRuntimes()
      .then(setInfo)
      .catch((e) => setError(errText(e)))
      .finally(() => setChecking(false))
  }, [])

  // 三个动作都不 setInfo：结果由 runtimes:status 推回来，这里只兜住「命令本身
  // 没打出去」这一种异常。装到一半失败的原因在那一行自己的 error 里。
  const install = useCallback((id: string) => {
    setError('')
    installRuntime(id).catch((e) => setError(errText(e)))
  }, [])

  const authorize = useCallback((id: string) => {
    setError('')
    authorizeRuntime(id).catch((e) => setError(errText(e)))
  }, [])

  const cancel = useCallback((id: string) => {
    cancelRuntimeInstall(id).catch(() => {})
  }, [])

  const remove = useCallback((id: string) => {
    setError('')
    removeRuntime(id).catch((e) => setError(errText(e)))
  }, [])

  return { info, packs: info?.packs ?? [], checking, error, check, install, authorize, cancel, remove }
}
