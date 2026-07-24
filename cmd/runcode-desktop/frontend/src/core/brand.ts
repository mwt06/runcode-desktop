// 品牌配置(白标)。同一套代码内置多套品牌,构建时选一套——不改任何组件即可整体
// 换名字、换标记、换文案。默认是原品牌 runcode(XRUN),所以不设开关的构建与以前
// 完全一致;原品牌永远留在这里,不是被替换掉。
//
// 换品牌两种方式,任选其一:
//   1. 构建前设环境变量 VITE_BRAND=zhikai(供 CI/打包脚本,不改源码);
//   2. 把下面的 DEFAULT_BRAND 改成 'zhikai'(改一行,提交即生效)。
// 拼错或未知的值一律回落 DEFAULT_BRAND,绝不因为一个笔误就静默换掉品牌。
import zhikaiLogo from '@/assets/zhikai-logo.png'

// BrandLogo 决定品牌标记怎么画:'mark' 用内置的 X 双笔画 SVG(原品牌矢量标),
// 'image' 用一张位图(如智开的 logo.png,构建时打包进产物,运行期不联网)。
export type BrandLogo = { kind: 'mark' } | { kind: 'image'; src: string; alt: string }

// GreetingStyle 决定空对话时的欢迎语形态,由 chat-pane 按此渲染:
//   'explore' —— 面向编程:让 <助手> 在 <工作区> 中探索、修改或运行点什么。
//   'welcome' —— 面向办公:<登录用户名>老师您好,今天有什么可以帮您?
export type GreetingStyle = 'explore' | 'welcome'

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
  },
  zhikai: {
    key: 'zhikai',
    name: '智开',
    tagline: '你的 AI 办公助手 · 打开一个工作区开始会话',
    loginHeadline: '智开AI，您的AI办公助手',
    greeting: 'welcome',
    logo: { kind: 'image', src: zhikaiLogo, alt: '智开' },
  },
}

// selectBrand 把开关值解析成一套品牌:空/未知一律回落默认品牌。纯函数,可单测。
export function selectBrand(requested: string | undefined): Brand {
  const key = (requested ?? '').trim()
  return BRANDS[key] ?? BRANDS[DEFAULT_BRAND]
}

// BRAND 是本次构建生效的品牌;组件只读这一个。
export const BRAND: Brand = selectBrand(import.meta.env.VITE_BRAND)
