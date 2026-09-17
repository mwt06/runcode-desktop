import { describe, it, expect, vi, afterEach } from 'vitest'

import * as shim from './wails-v2-runtime'
import { splitName, Call, Events, Browser, Clipboard, Application, Window } from './wails-v2-runtime'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('splitName', () => {
  it('砍掉包导入路径的前缀，只留最后一段', () => {
    expect(splitName('github.com/wt68/runcode/internal/desktop.App.StartSession')).toEqual({
      pkg: 'desktop',
      struct: 'App',
      method: 'StartSession',
    })
  })

  it('main 包没有斜杠，原样保留', () => {
    expect(splitName('main.RecorderWindow.SetMode')).toEqual({
      pkg: 'main',
      struct: 'RecorderWindow',
      method: 'SetMode',
    })
  })

  it('段数不够时报出原名，而不是静默拆错', () => {
    expect(() => splitName('App.Method')).toThrow(/App\.Method/)
  })
})

describe('Call.ByName', () => {
  it('按三段路径找到 v2 绑定表里的方法并原样转发实参', async () => {
    const StartSession = vi.fn().mockResolvedValue({ id: 's1' })
    vi.stubGlobal('window', { go: { desktop: { App: { StartSession } } } })

    const out = await Call.ByName('github.com/wt68/runcode/internal/desktop.App.StartSession', 'a', 1)

    expect(StartSession).toHaveBeenCalledWith('a', 1)
    expect(out).toEqual({ id: 's1' })
  })

  it('绑定表里没有时抛出带方法名的错误，不是 undefined is not a function', () => {
    vi.stubGlobal('window', { go: { desktop: { App: {} } } })
    expect(() => Call.ByName('github.com/wt68/runcode/internal/desktop.App.Nope')).toThrow(/desktop\.App\.Nope/)
  })
})

describe('Events.On', () => {
  it('把 v2 摊平的实参包成 v3 的 WailsEvent（载荷在 .data 上）', () => {
    let handler: ((...d: any[]) => void) | undefined
    const off = vi.fn()
    vi.stubGlobal('window', {
      runtime: {
        EventsOn: (_name: string, cb: (...d: any[]) => void) => {
          handler = cb
          return off
        },
      },
    })

    const seen: any[] = []
    const unsubscribe = Events.On('turn:end', (ev) => seen.push(ev))

    handler?.({ sessionId: 's1', payload: { ok: true } })
    expect(seen).toEqual([{ name: 'turn:end', data: { sessionId: 's1', payload: { ok: true } } }])

    // 退订函数必须是 v2 那个，不能自己造一个空壳。
    expect(unsubscribe).toBe(off)
  })

  it('window.runtime 缺失时报出人话，而不是读 undefined 的属性', () => {
    vi.stubGlobal('window', {})
    expect(() => Events.On('warning', () => {})).toThrow(/window\.runtime/)
  })
})

describe('窗口 / 浏览器 / 剪贴板', () => {
  it('都转发到 window.runtime 的对应方法，并统一成 Promise', async () => {
    const rt = {
      BrowserOpenURL: vi.fn(),
      ClipboardSetText: vi.fn().mockResolvedValue(true),
      Quit: vi.fn(),
      WindowMinimise: vi.fn(),
      WindowToggleMaximise: vi.fn(),
    }
    vi.stubGlobal('window', { runtime: rt })

    await Browser.OpenURL('https://example.com')
    await expect(Clipboard.SetText('hi')).resolves.toBe(true)
    await Application.Quit()
    await Window.Minimise()
    await Window.ToggleMaximise()

    expect(rt.BrowserOpenURL).toHaveBeenCalledWith('https://example.com')
    expect(rt.ClipboardSetText).toHaveBeenCalledWith('hi')
    expect(rt.Quit).toHaveBeenCalled()
    expect(rt.WindowMinimise).toHaveBeenCalled()
    expect(rt.WindowToggleMaximise).toHaveBeenCalled()
  })
})

// 这条测试是整个方案的兜底闸门：垫片靠 vite 的模块名别名顶替 '@wailsio/runtime'，
// 而 tsc 永远解析到真包的类型，所以"垫片少导出了一个东西"类型检查是看不见的。
// 少了的后果只在麒麟机器上显形（一块白屏），所以在这里按源码实际用到的名字来卡。
describe('导出覆盖率', () => {
  // 用 vite 的 glob 而不是 node 的 fs：本仓前端没装 @types/node，而为一条测试拉一个
  // 类型包进来不划算。?raw 拿源码文本，路径相对本文件，上一级就是整个 src/。
  const SOURCES = import.meta.glob('../**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true }) as Record<string, string>

  it('盖住全仓对 @wailsio/runtime 的每一个具名导入', () => {
    const wanted = new Set<string>()
    for (const [file, text] of Object.entries(SOURCES)) {
      if (file.includes('wails-v2-runtime')) continue
      for (const m of text.matchAll(/import\s+(?:type\s+)?\{([^}]*)\}\s+from\s+['"]@wailsio\/runtime['"]/g)) {
        for (const name of m[1].split(',')) {
          const bare = name.trim().split(/\s+as\s+/)[0].trim()
          if (bare) wanted.add(bare)
        }
      }
    }

    // 扫不到任何导入本身就是回归信号：要么正则失效了，要么调用点被改成了别的写法。
    expect(wanted.size).toBeGreaterThan(0)
    expect([...wanted].filter((name) => !(name in shim)).sort()).toEqual([])
  })
})
