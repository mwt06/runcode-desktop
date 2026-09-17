// 品牌配置(白标)。同一套代码内置多套品牌,构建时选一套——不改任何组件即可整体
// 换名字、换标记、换文案。默认是原品牌 runcode(XRUN),所以不设开关的构建与以前
// 完全一致;原品牌永远留在这里,不是被替换掉。
//
// 换品牌两种方式,任选其一:
//   1. 构建前设环境变量 VITE_BRAND=zhikai(供 CI/打包脚本,不改源码);
//   2. 把下面的 DEFAULT_BRAND 改成 'zhikai'(改一行,提交即生效)。
// 拼错或未知的值一律回落 DEFAULT_BRAND,绝不因为一个笔误就静默换掉品牌。
import zhikaiLogo from '@/assets/zhikai-logo.png'
import zhikaiMascot from '@/assets/zhikai-mascot.gif'
import zhikaiComposerMascot from '@/assets/zhikai-composer.gif'

// BrandLogo 决定品牌标记怎么画:'mark' 用内置的 X 双笔画 SVG(原品牌矢量标),
// 'image' 用一张位图(如智开的 logo.png,构建时打包进产物,运行期不联网)。
export type BrandLogo = { kind: 'mark' } | { kind: 'image'; src: string; alt: string }

// GreetingMark 是空对话问候语上方的插画(如智开的吉祥物动图)。它与 logo 是两回事:
// logo 是小尺寸品牌标记,统一套在圆角方框里(标题栏/起始页/问候语共用);插画自带
// 形状与背景,按原图放大直出、不套框,否则会双重描边且细节糊成一团。不设的品牌
// 沿用带框的 logo。
export type GreetingMark = { src: string; alt: string; size: number }

// ComposerMark 是钉在输入框上方的品牌插画。与 greetingMark 的分工:那个只在空对话
// 时出现在问候语上方,对话一开始就没了;这个从头到尾都在输入区上边。不设的品牌
// (原品牌 XRUN)什么都不画,输入区与以前完全一致。
//
// height 是显示高度(px),宽度按原图比例自适应——换一张不同比例的图不用改代码。
export type ComposerMark = { src: string; alt: string; height: number }

// GreetingStyle 决定空对话时的欢迎语形态,由 chat-pane 按此渲染:
//   'explore' —— 面向编程:让 <助手> 在 <工作区> 中探索、修改或运行点什么。
//   'welcome' —— 面向办公:<登录用户名>老师您好,今天有什么可以帮您?
export type GreetingStyle = 'explore' | 'welcome'

// BrandFeatures 是按品牌开关的功能模块。关掉的功能连入口一起不画,而不是画出来
// 置灰或点了报错——置灰对用户是"这版有这功能但现在不能用",整块不画才是"这版
// 没有这功能"。代码与数据一律留在原处,开关是唯一的事实来源。
export type BrandFeatures = {
  /**
   * 录音纪要:输入框上方那个「录音纪要」分类,以及设置页的录音区块。
   *
   * 智开版 2026-09-17 起**临时**关掉,把这里改回 true 即可恢复(Go 侧的录音窗、
   * 采集、纪要链路都没动,只是前端没有入口把它叫出来)。
   */
  recorder: boolean
}

export type Brand = {
  key: string
  // name 是文字标:标题栏、起始页大标题、对话中对助手的称呼都用它。
  name: string
  // tagline 是起始页副标题,含"这个助手是干什么的"那半句(编程 / 办公)。
  tagline: string
  // loginHeadline 是通行证登录门的大标语。
  loginHeadline: string
  // greeting 是空对话时的欢迎语形态。
  greeting: GreetingStyle
  logo: BrandLogo
  // greetingMark 覆盖空对话问候语上方的标记;不设则用带框的 logo。
  greetingMark?: GreetingMark
  // composerMark 是输入框上方的插画;不设则输入区上方什么都不画。
  composerMark?: ComposerMark
  // features 必填:加品牌时必须对每个功能表个态,漏了就编译不过——默认继承一套
  // 的话,新品牌会悄悄带上别人没想清楚的功能。
  features: BrandFeatures
}

// 想换品牌:改这里,或设 VITE_BRAND。
const DEFAULT_BRAND = 'runcode'

const BRANDS: Record<string, Brand> = {
  runcode: {
    key: 'runcode',
    name: 'XRUN',
    tagline: '你的 AI 编程伙伴 · 打开一个工作区开始会话',
    loginHeadline: 'XRUN，您的 AI 编程助手',
    greeting: 'explore',
    logo: { kind: 'mark' },
    features: { recorder: true },
  },
  zhikai: {
    key: 'zhikai',
    name: '智开',
    tagline: '你的 AI 办公助手 · 打开一个工作区开始会话',
    loginHeadline: '智开AI，您的AI办公助手',
    greeting: 'welcome',
    logo: { kind: 'image', src: zhikaiLogo, alt: '智开' },
    greetingMark: { src: zhikaiMascot, alt: '智开', size: 96 },
    composerMark: { src: zhikaiComposerMascot, alt: '智开', height: 64 },
    // 录音纪要临时下线,见 BrandFeatures.recorder。
    features: { recorder: false },
  },
}

// selectBrand 把开关值解析成一套品牌:空/未知一律回落默认品牌。纯函数,可单测。
export function selectBrand(requested: string | undefined): Brand {
  const key = (requested ?? '').trim()
  return BRANDS[key] ?? BRANDS[DEFAULT_BRAND]
}

// BRAND 是本次构建生效的品牌;组件只读这一个。
export const BRAND: Brand = selectBrand(import.meta.env.VITE_BRAND)
