// 电平条：设计稿里录音窗底部那条会起伏的线。
//
// 它不是装饰。用户开会时最想确认的一件事是「到底收没收到声音」——静音的麦克风
// 会让一整场会白录，而这件事只有电平条能在第一秒就告诉他。所以静止状态也要画
// 出来（一排点），而不是干脆不渲染。
import { useEffect, useRef } from 'react'

// BARS 是条的根数。取值只影响观感：太少像柱状图，太多在浮窗那点宽度里糊成一团。
const BARS = 48
// FLOOR 是每根条的最小高度占比，对应静止时那一排点。
const FLOOR = 0.06
// DB_FLOOR 是映射的下沿（dBFS）。低于它当作静音。
const DB_FLOOR = -50

// scale 把线性幅度换成分贝再归一。
//
// 直接按幅度画是不行的：安静房间的底噪实测约 0.005，一米外正常说话的峰值也就
// 0.05~0.3——线性映射下说话时条只涨到高度的几个百分点，看着跟没在收音一样，而
// 这根条存在的全部意义就是回答「到底收没收到声音」。人耳本来也是对数的。
function scale(amp: number): number {
  if (amp <= 0) return 0
  const db = 20 * Math.log10(amp)
  return Math.max(0, Math.min(1, (db - DB_FLOOR) / -DB_FLOOR))
}

export function LevelBar({ mic, sys, active, height = 28 }: {
  mic: number
  sys: number
  active: boolean
  height?: number
}) {
  // history 存最近 BARS 次的电平，新的从右边进——所以波形是从右往左滚，
  // 与「刚说的话在最右边」的直觉一致。
  const history = useRef<number[]>(new Array(BARS).fill(0))
  const canvas = useRef<HTMLCanvasElement>(null)

  // 两条轨取较大的那个：界面上只有一条波形，而用户关心的是「有没有声音进来」，
  // 不关心是哪一路。哪一路的问题由上面的轨道标签回答。
  const level = Math.max(mic, sys)

  useEffect(() => {
    history.current = [...history.current.slice(1), active ? scale(level) : 0]
    const el = canvas.current
    if (!el) return
    const ctx = el.getContext('2d')
    if (!ctx) return

    const dpr = window.devicePixelRatio || 1
    const w = el.clientWidth
    const h = el.clientHeight
    if (el.width !== Math.round(w * dpr) || el.height !== Math.round(h * dpr)) {
      el.width = Math.round(w * dpr)
      el.height = Math.round(h * dpr)
    }
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)

    // 颜色从 CSS 变量取，跟着主题走——canvas 拿不到 Tailwind 的类。
    const css = getComputedStyle(el)
    ctx.fillStyle = css.getPropertyValue('color') || '#888'

    const gap = 2
    const barW = Math.max(1, (w - gap * (BARS - 1)) / BARS)
    for (let i = 0; i < BARS; i++) {
      const v = Math.max(FLOOR, Math.min(1, history.current[i]))
      const bh = Math.max(1.5, v * h)
      const x = i * (barW + gap)
      const y = (h - bh) / 2
      ctx.globalAlpha = active ? 0.35 + 0.65 * v : 0.3
      ctx.beginPath()
      ctx.roundRect(x, y, barW, bh, barW / 2)
      ctx.fill()
    }
  }, [level, active])

  return (
    <canvas
      ref={canvas}
      className="w-full text-muted"
      style={{ height }}
      // 纯装饰性图形，读屏软件跳过它——真正要播报的是旁边的计时与状态文字。
      aria-hidden
    />
  )
}
