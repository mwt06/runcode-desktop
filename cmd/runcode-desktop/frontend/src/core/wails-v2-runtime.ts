// Wails v2 运行时垫片——只给麒麟 V10 那条链路用。
//
// 麒麟 V10 的外壳跑在 Wails v2 上（理由见 ../../../main_kylin.go 顶部：V10 的
// WebKitGTK 只有 4.0 这个 ABI，而 v3 没有 4.0 的代码路径）。可整份前端是按 **v3**
// 的运行时写的——protocol/commands.ts 用 Call.ByName、events.ts 用 Events.On，都来自
// '@wailsio/runtime'，底层 fetch /wails/runtime 并依赖 window._wails。v2 这三样一样
// 都不提供：它注入的是 window.go（绑定方法表）与 window.runtime（事件、窗口、剪贴板、
// 浏览器）。不接这一层的表现是窗口能开、界面一片空白。
//
// 接缝只有一处，在**构建期**：vite.config.ts 在 VITE_WAILS=v2 时把 '@wailsio/runtime'
// 这个**模块名**别名到本文件。于是
//
//   · 五个调用点（protocol/commands.ts、protocol/events.ts、bridge.ts、
//     recorder/window-api.ts、shell/title-bar.tsx）与 protogen 的模板一行都不用改；
//   · 三平台的 v3 产物跟今天完全一样——别名不存在时本文件根本不会被打进包；
//   · 不做 window._wails 的**运行时**探测。那种写法的失败模式是时序相关的偶发
//     （运行时脚本比模块先到还是后到），是这条链路上最难查的一类问题；构建期二选一
//     则是确定的。
//
// window.go / window.runtime 由 v2 的 assetserver 往 index.html 的 <head> 里插
// /wails/runtime.js 与 /wails/ipc.js 得到（v2 的 pkg/assetserver/assetserver.go），
// 我们不需要在 HTML 里写任何东西。
//
// 下面导出的六个名字必须盖住全仓对 '@wailsio/runtime' 的具名导入，这条由
// wails-v2-runtime.test.ts 扫源码断言——以后谁新用了一个 v3 API，红的是 CI，
// 而不是麒麟机器上的一块白屏。

// ---- v2 注入的两个全局 --------------------------------------------------------

type GoBindings = Record<string, Record<string, Record<string, (...args: any[]) => Promise<any>>>>

interface V2Runtime {
  EventsOn(name: string, cb: (...data: any[]) => void): () => void
  BrowserOpenURL(url: string): void
  ClipboardSetText(text: string): Promise<boolean>
  Quit(): void
  WindowMinimise(): void
  WindowToggleMaximise(): void
}

declare global {
  interface Window {
    go?: GoBindings
    runtime?: V2Runtime
  }
}

function runtime(): V2Runtime {
  const rt = window.runtime
  if (!rt) {
    throw new Error('Wails v2 垫片：window.runtime 不存在（assetserver 没注入 /wails/runtime.js？）')
  }
  return rt
}

// ---- 方法名翻译 ---------------------------------------------------------------

// splitName 把 v3 的全限定名拆成 v2 绑定表的三段。
//
// v3 按 "<包导入路径>.<类型>.<方法>" 定位（Call.ByName 的入参就是这个），例如
//   github.com/wt68/runcode/internal/desktop.App.StartSession
//   main.RecorderWindow.SetMode
// v2 的表是 window.go[包名][类型][方法]，包名取 reflect 的 Type.String() 前缀，
// 也就是**导入路径的最后一段**（v2 的 internal/binding）。两边的类型名与方法名一致，
// 差的只是包这一段，所以整个翻译就是"砍掉路径前缀"。
export function splitName(fqn: string): { pkg: string; struct: string; method: string } {
  const parts = fqn.split('.')
  if (parts.length < 3) {
    throw new Error(`Wails v2 垫片：不认识的方法名 ${fqn}（要 "<包>.<类型>.<方法>"）`)
  }
  const method = parts.pop() as string
  const struct = parts.pop() as string
  const path = parts.join('.')
  return { pkg: path.slice(path.lastIndexOf('/') + 1), struct, method }
}

// ---- 与 v3 同名同形的六个导出 --------------------------------------------------

export const Call = {
  ByName(fqn: string, ...args: any[]): Promise<any> {
    const { pkg, struct, method } = splitName(fqn)
    const fn = window.go?.[pkg]?.[struct]?.[method]
    if (!fn) {
      throw new Error(`Wails v2 垫片：绑定表里没有 ${pkg}.${struct}.${method}（原名 ${fqn}）`)
    }
    return fn(...args)
  },
}

export const Events = {
  // v3 的回调收到的是 WailsEvent 对象，载荷挂在 .data 上；v2 是把发射时的实参
  // 摊平传进来。Go 侧两份外壳都是单载荷发射（kylinSink.Emit → EventsEmit(ctx,
  // name, data)），所以 data[0] 就是 v3 的 ev.data。退订函数两边语义一致。
  On(name: string, cb: (ev: { name: string; data: any }) => void): () => void {
    return runtime().EventsOn(name, (...data: any[]) => cb({ name, data: data[0] }))
  },
}

export const Browser = {
  // v2 的是同步调用，v3 返回 Promise；统一成 Promise，调用点才不用分版本写。
  OpenURL(url: string): Promise<void> {
    runtime().BrowserOpenURL(url)
    return Promise.resolve()
  },
}

export const Clipboard = {
  SetText(text: string): Promise<boolean> {
    return runtime().ClipboardSetText(text)
  },
}

export const Application = {
  Quit(): Promise<void> {
    runtime().Quit()
    return Promise.resolve()
  },
}

// v3 的 Window 是"当前窗口"对象。v2 只有一个窗口，运行时方法直接对应。
export const Window = {
  Minimise(): Promise<void> {
    runtime().WindowMinimise()
    return Promise.resolve()
  },
  ToggleMaximise(): Promise<void> {
    runtime().WindowToggleMaximise()
    return Promise.resolve()
  },
}
