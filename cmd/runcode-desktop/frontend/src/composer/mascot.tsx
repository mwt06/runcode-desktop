// 输入框上方的品牌插画（智开的吉祥物）。
//
// 只在品牌配了 composerMark 时出现（见 core/brand），原品牌 XRUN 没有这一项，
// 输入区与以前一模一样。
//
// ---- 为什么不让它一直循环 ----------------------------------------------------
//
// GIF 默认无限重播。一个 4.8 秒的动图钉在输入框上方不停地转，就是眼角余光里的
// 一台永动机——正在写字的时候尤其烦人，而且看第三遍之后它就不再传达任何东西了。
// 所以这里分两层处理：
//
//   1. 资产本身已经改成「播一次即停」（assets/zhikai-composer.gif 的 NETSCAPE
//      循环次数从 0 改成 1），浏览器放完停在最后一帧，不需要 JS 去按停。
//      那一帧是特意挑的：原图 119 帧，末尾那段是抬爪挥手来回摆，最后一帧恰好
//      摆回爪子放下的位置，停在那儿看着像动画卡住了。所以资产按字节截到原图
//      第 117 帧（挥手的顶点，爪子举在脸侧）——不重编码，画质体积都没动。
//   2. 之后只在两种时机重播，都不是定时器式的机械节拍：
//        · 鼠标移上去——用户主动想看，带冷却，免得扫过去就重启；
//        · 隔一段**随机**的长间隔自己活动一下。随机是关键：固定的「每 60 秒动
//          一次」照样是机械重复，人很快会开始预期它，那就又变成背景噪音了。
//
// 页面不可见时那一次直接跳过（只重新排期），否则切回来会看到一串补播。
import { useEffect, useRef, useState } from 'react'
import { BRAND } from '@/core/brand'

// 随机静止间隔的上下界。下界要明显长过一次播放（约 4.8 秒），否则观感上仍然是
// 连着放；上界给得够远，让它更像偶尔动一下而不是在等秒表。
const IDLE_MIN_MS = 45_000
const IDLE_MAX_MS = 120_000

// 悬停重播的冷却。鼠标扫过输入区上方是很常见的动作，没有冷却就会被反复重启。
const HOVER_COOLDOWN_MS = 8_000

/**
 * nextIdleDelayMS 取下一次自发重播的间隔，落在 [IDLE_MIN_MS, IDLE_MAX_MS]。
 * 注入 rand 是为了可测——默认用 Math.random。
 */
export function nextIdleDelayMS(rand: () => number = Math.random): number {
  const span = IDLE_MAX_MS - IDLE_MIN_MS
  return IDLE_MIN_MS + Math.floor(Math.min(Math.max(rand(), 0), 0.999999) * (span + 1))
}

export function ComposerMascot() {
  const mark = BRAND.composerMark
  // take 是「第几次播放」。改它会让 <img> 换 key 重新挂载，动画从第一帧重来。
  const [take, setTake] = useState(0)
  const lastPlayedAt = useRef(0)
  const timer = useRef(0)

  useEffect(() => {
    if (!mark) return
    const play = () => {
      lastPlayedAt.current = Date.now()
      setTake((n) => n + 1)
    }
    const schedule = () => {
      timer.current = window.setTimeout(() => {
        // 后台标签页不播：切回来时不该看到补播出来的一串动画。
        if (!document.hidden) play()
        schedule()
      }, nextIdleDelayMS())
    }
    schedule()
    return () => window.clearTimeout(timer.current)
  }, [mark])

  if (!mark) return null

  const onHover = () => {
    if (Date.now() - lastPlayedAt.current < HOVER_COOLDOWN_MS) return
    lastPlayedAt.current = Date.now()
    setTake((n) => n + 1)
  }

  return (
    // w-fit：悬停区只贴着插画本身，而不是输入框上方一整条——鼠标从对话区滑向
    // 输入框时会横穿这一行，那不该算「想看它动」。
    // 绝对定位在输入框正上方：bottom-full 让它的下沿正好压在框的上沿，right 靠右。
    // 必须是绝对定位——放进正常流里它会自己占一行 64px，把上面「录音纪要」那排
    // 快捷技能顶走（定位参照是 index.tsx 里包住 textarea 的那层 relative）。
    // img 用 block：inline 的话基线下方会留几像素，那样「贴着」是假的。
    <div className="absolute bottom-full right-0.5 w-fit" onMouseEnter={onHover}>
      <img
        // key 换掉即重新挂载，动画回到第一帧；查询串保证拿到的不是同一份已经
        // 播完的解码结果（首次不带，用打包好的那份缓存）。
        key={take}
        src={take === 0 ? mark.src : `${mark.src}?take=${take}`}
        alt={mark.alt}
        height={mark.height}
        draggable={false}
        className="block object-contain select-none"
        style={{ height: mark.height, width: 'auto' }}
      />
    </div>
  )
}
