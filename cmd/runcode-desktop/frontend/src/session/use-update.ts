// useUpdate 是版本更新在前端的唯一状态源。
//
// 它是**后端那台状态机的镜子**，不是第二台状态机：Go 侧每次状态变化都把整份
// UpdateInfo 推过来（update:status），这里只负责收下、连同四个动作一起交出去。
// 前端不自己推导「现在算不算有新版」「下完了没有」——两份各自演进的状态机正是
// 「明明下好了，按钮还停在下载」这类缺陷的来源，而它们只在慢网络上才复现。
//
// 检查这件事前端也不主动发起：启动后的自动检查在 Go 侧（延后几秒，失败不打扰），
// 登录成功后还会补一次。这里只在挂载时读一次当前状态，好让设置页一打开就有东西可画。
import { useCallback, useEffect, useState } from 'react'
import {
  cancelUpdateDownload,
  checkUpdate,
  downloadUpdate,
  errText,
  installUpdate,
  onEvent,
  updateStatus,
  UpdateStages,
  type UpdateInfo,
  type UpdateStage,
} from '@/core/bridge'

export interface UpdateController {
  /** info 为 null 表示还没读到后端状态（挂载后的头一瞬）。 */
  info: UpdateInfo | null
  stage: UpdateStage
  /** hasUpdate 是「有一个新版本在等着」——侧栏那颗小红点看的就是它。 */
  hasUpdate: boolean
  /** busy 是「正在跑一趟检查或下载」，按钮据此置灰。 */
  busy: boolean
  /** error 只兜住命令通道本身的异常；业务失败的原因在 info.error 里。 */
  error: string
  check: () => void
  download: () => void
  cancel: () => void
  install: () => void
}

export function useUpdate(): UpdateController {
  const [info, setInfo] = useState<UpdateInfo | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    // 先订阅再读取。反过来的话，恰好落在「读完」与「订阅上」之间的那一次推送会
    // 丢掉——而那正是启动自动检查出结果的时刻，丢了就要等用户手动点一次才有反应。
    const off = onEvent('update:status', setInfo)
    let alive = true
    updateStatus()
      .then((first) => {
        // 订阅先到的话以订阅为准：这一次读回来的可能已经是旧的。
        if (alive) setInfo((cur) => cur ?? first)
      })
      .catch(() => {})
    return () => {
      alive = false
      off()
    }
  }, [])

  // run 把四个动作收成一种写法：清掉上一次的通道错误，失败了记下来。
  // 业务失败（网关 500、校验不过）后端已经写进 info.error 并推过来了，这里记的是
  // 「连命令都没打出去」那种——两者不会同时出现，界面取其一即可。
  const run = useCallback((fn: () => Promise<unknown>) => {
    setError('')
    void fn().catch((e: unknown) => setError(errText(e)))
  }, [])

  const check = useCallback(() => run(checkUpdate), [run])
  const download = useCallback(() => run(downloadUpdate), [run])
  const cancel = useCallback(() => run(cancelUpdateDownload), [run])
  const install = useCallback(() => run(installUpdate), [run])

  const stage = info?.stage ?? UpdateStages.Idle
  return {
    info,
    stage,
    // 下载中与待安装也算「有新版」：更新还悬着，小红点不该在用户点了下载之后就消失。
    hasUpdate:
      stage === UpdateStages.Available ||
      stage === UpdateStages.Downloading ||
      stage === UpdateStages.Verifying ||
      stage === UpdateStages.Ready,
    busy: stage === UpdateStages.Checking || stage === UpdateStages.Downloading || stage === UpdateStages.Verifying,
    error,
    check,
    download,
    cancel,
    install,
  }
}
