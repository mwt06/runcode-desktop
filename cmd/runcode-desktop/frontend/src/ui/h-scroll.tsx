// HScroll 是「一行放不下就横向滚」的轨道：藏掉滚动条、滚轮翻译成横向、两端按
// 「那一侧还有没有内容」给出淡出与翻页箭头。
//
// ---- 为什么需要「两端的提示」 ------------------------------------------------
//
// 之前这类条子只做了「藏滚动条 + 溢出就能滚」，理由是"能不能滚由内容有没有溢出
// 表达"。实际用下来不成立：切断点落在两个 chip 之间时，右边什么都不剩，看起来
// 就是"这一排只有这几项"——用户根本不知道后面还有，更不会想到去滚。所以溢出必须
// 被**画出来**，而不是靠用户推断。
//
// 淡出用 mask 而不是"从透明到背景色"的渐变：渐变要求调用方的背景色和这里写死的
// 那个颜色永远一致，换个容器背景就会露出一条色带；mask 是把内容本身擦掉，跟底下
// 是什么颜色无关。
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { Icon } from './icons'

// FADE_PX 是两端淡出的宽度，也决定了翻页箭头能藏在多宽的淡出区里。
const FADE_PX = 30

// PAGE_RATIO 是点一下箭头滚多远（可视宽度的几成）。不滚满一屏是为了留下重叠部分，
// 让人能看出前后是同一排而不是换了一批。
const PAGE_RATIO = 0.8

/** edgeMask 按两端是否还有内容拼出 mask；都没有时返回 undefined（不加 mask）。 */
function edgeMask(left: boolean, right: boolean): string | undefined {
  if (!left && !right) return undefined
  const stops = [
    left ? `transparent 0, #000 ${FADE_PX}px` : '#000 0',
    right ? `#000 calc(100% - ${FADE_PX}px), transparent 100%` : '#000 100%',
  ]
  return `linear-gradient(to right, ${stops.join(', ')})`
}

export function HScroll({ className = '', rowClassName = '', style, children }: {
  /** 外层容器的类名（外边距一类）。翻页箭头贴的是这一层的左右边界。 */
  className?: string
  /** 内容行的类名，排布用（gap、items-*）。 */
  rowClassName?: string
  /** 外层容器的内联样式。给右侧留白（让开浮层）就用它，见 composer/scenario-bar。 */
  style?: CSSProperties
  children: ReactNode
}) {
  const trackRef = useRef<HTMLDivElement>(null)
  const rowRef = useRef<HTMLDivElement>(null)
  const [edge, setEdge] = useState({ left: false, right: false })

  // 滚轮在这一行上时，纵向滚轮翻译成横向滚动。
  //
  // 必须用原生监听且 passive:false：React 把 wheel 挂在根节点上、而且是被动的，
  // 在 onWheel 里 preventDefault 不生效（只会在控制台留一句警告）。
  useEffect(() => {
    const el = trackRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      // 装得下就不劫持。窗口够宽时这一行本来就不滚，再吞掉滚轮，鼠标扫过这一条
      // 时对话区会莫名其妙地滚不动。
      if (el.scrollWidth <= el.clientWidth) return
      // 触控板的横向手势交给浏览器原生处理，这里只翻译纵向的那一维。
      if (Math.abs(e.deltaX) > Math.abs(e.deltaY)) return
      e.preventDefault()
      // deltaMode=1 是按行计数（部分鼠标驱动），此时 deltaY 只有个位数，
      // 直接当像素用会几乎推不动。
      el.scrollLeft += e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [])

  // 两端还有没有内容：滚动位置、轨道宽度、内容宽度，三者任一变了都要重算。
  // 观察的是轨道与内容行这两个稳定的节点，而不是 children——children 每次渲染都是
  // 新对象，挂在依赖里等于每帧重装一次观察器。
  useEffect(() => {
    const track = trackRef.current
    const row = rowRef.current
    if (!track || !row) return
    const read = () => {
      // 亚像素：宽度经常差个零点几，用 1px 容差，否则右箭头会在"已经到底"时还亮着。
      const max = track.scrollWidth - track.clientWidth
      setEdge({ left: track.scrollLeft > 1, right: track.scrollLeft < max - 1 })
    }
    read()
    track.addEventListener('scroll', read, { passive: true })
    const ro = new ResizeObserver(read)
    ro.observe(track)
    ro.observe(row)
    return () => {
      track.removeEventListener('scroll', read)
      ro.disconnect()
    }
  }, [])

  const page = (dir: -1 | 1) => {
    const el = trackRef.current
    if (!el) return
    el.scrollBy({ left: dir * el.clientWidth * PAGE_RATIO, behavior: 'smooth' })
  }

  const mask = edgeMask(edge.left, edge.right)
  return (
    <div className={`relative min-w-0 ${className}`} style={style}>
      <div
        ref={trackRef}
        // min-w-0：外层若是 flex 容器（如预览标签条），轨道要能被压到比内容窄。
        className="no-scrollbar overflow-x-auto min-w-0"
        style={{ maskImage: mask, WebkitMaskImage: mask }}
      >
        {/* w-max：内容行按内容宽度排，不被轨道压成轨道宽——否则 ResizeObserver 看到的
            永远是轨道的宽度，内容增减时不会触发重算。 */}
        <div ref={rowRef} className={`flex flex-nowrap items-center w-max ${rowClassName}`}>
          {children}
        </div>
      </div>
      {edge.left && <EdgeButton dir={-1} onClick={() => page(-1)} />}
      {edge.right && <EdgeButton dir={1} onClick={() => page(1)} />}
    </div>
  )
}

// EdgeButton 是压在淡出区上的翻页箭头。它会盖住半个 chip——那个 chip 本来就被淡出
// 擦掉了一半，盖住的是已经读不清的部分；换成"箭头挤在轨道外面"则要么占掉一截可视
// 宽度，要么在装得下时留两块空白。
function EdgeButton({ dir, onClick }: { dir: -1 | 1; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={dir < 0 ? '向左' : '向右'}
      className={`absolute top-1/2 -translate-y-1/2 w-[22px] h-[22px] inline-flex items-center justify-center rounded-full border border-line2 bg-surface text-muted shadow-xs cursor-pointer transition hover:text-primaryink hover:border-primary ${
        dir < 0 ? 'left-0' : 'right-0'
      }`}
    >
      {/* 图标表里只有 chevron-down，转出左右两个方向——与 panel-left 一图两用同理，
          不为方向各存一份字形。 */}
      <Icon name="chevron-down" size={14} className={dir < 0 ? 'rotate-90' : '-rotate-90'} />
    </button>
  )
}
