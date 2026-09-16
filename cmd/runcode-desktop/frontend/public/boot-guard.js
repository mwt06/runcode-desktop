// 启动自检：把"白屏"变成看得见的东西。
//
// 白屏是这个项目里最难查的一类故障——进程活着、窗口开着、界面什么都没有，而正式
// 构建里没有控制台可看。麒麟 V10 尤其：Wails v2 把 developer extras 锁在 devtools
// 构建标记后面，连 WEBKIT_INSPECTOR_SERVER 这个环境变量也随之失效，于是排查唯一的
// 办法是重新出一版诊断包——一轮下来十几分钟，而且要用户会按快捷键。
//
// 这段脚本收下启动阶段的异常，做两件事：
//
//   1. **写进宿主日志**。Wails v2 的 window.runtime.LogError 会打到应用自己的
//      stdout，也就是用户跑 ./zhikai 的那个终端里——不需要任何 GUI 操作。
//   2. **两秒后 #root 还是空的，就地画一块最小错误面板**。纯内联样式，不依赖任何
//      已加载的 CSS 或框架，所以哪怕样式表整个没生效、或者 JS 主包压根没解析成功，
//      它照样显示得出来。
//
// 它是普通 <script>（不是 module），所以在 <head> 里同步执行，排在主包之前，能接住
// 主包自身的解析/求值错误。健康启动时这段什么也不做。
//
// 两个 HTML 入口共用这一份（public/ 下的文件由 vite 原样拷进产物）。
;(function () {
  var errs = []

  function note(what) {
    errs.push(what)
    try { console.error('[boot]', what) } catch (e) { /* 控制台不可用时忽略 */ }
    // Wails v2 才有；v3 下没有这个全局，静默跳过。
    try {
      if (window.runtime && typeof window.runtime.LogError === 'function') {
        window.runtime.LogError('[boot] ' + what)
      }
    } catch (e) { /* 宿主没就绪时忽略 */ }
  }

  window.addEventListener('error', function (e) {
    // 资源加载失败（script/link 404、MIME 不对）没有 message，信息在 e.target 上。
    var el = e.target
    if (el && el !== window && (el.src || el.href)) {
      note('资源加载失败: ' + (el.src || el.href))
      return
    }
    note(
      e.message +
      (e.filename ? '  @ ' + e.filename + ':' + e.lineno + ':' + e.colno : '') +
      (e.error && e.error.stack ? '\n' + e.error.stack : '')
    )
  }, true) // 捕获阶段：资源错误不冒泡，不用 true 收不到

  window.addEventListener('unhandledrejection', function (e) {
    var r = e.reason
    note('未处理的 Promise 拒绝: ' + ((r && (r.stack || r.message)) || String(r)))
  })

  function mounted() {
    var root = document.getElementById('root')
    return !!root && root.childElementCount > 0
  }

  // 两级超时，而不是一个。麒麟这类机器上首屏渲染本来就慢，两秒就盖一块错误面板会
  // 在健康启动时闪出来——那比白屏还糟。所以两秒只往宿主日志里记一笔（不可见，够
  // 开发者在终端里看到），八秒还空才真的画面板。
  setTimeout(function () {
    if (mounted()) return
    note('启动偏慢：2 秒后界面仍未挂载')
  }, 2000)

  setTimeout(function () {
    if (mounted()) return
    note('启动失败：8 秒后界面仍未挂载')

    var box = document.createElement('div')
    box.setAttribute('style', [
      'position:fixed', 'inset:0', 'z-index:2147483647',
      'background:#fff', 'color:#1a1a1a', 'padding:24px',
      'font:13px/1.6 system-ui,-apple-system,"Noto Sans CJK SC","Microsoft YaHei",sans-serif',
      'overflow:auto',
    ].join(';'))

    var h = document.createElement('div')
    h.setAttribute('style', 'font-size:15px;font-weight:600;margin-bottom:8px')
    h.textContent = '界面未能启动'
    box.appendChild(h)

    var p = document.createElement('div')
    p.setAttribute('style', 'color:#666;margin-bottom:14px')
    p.textContent = '下面是启动阶段捕获到的错误，请连同这段内容一起反馈。'
    box.appendChild(p)

    var pre = document.createElement('pre')
    pre.setAttribute('style', [
      'white-space:pre-wrap', 'word-break:break-all', 'margin:0',
      'padding:12px', 'background:#f5f5f5', 'border:1px solid #e0e0e0',
      'border-radius:6px', 'font:12px/1.5 ui-monospace,Menlo,Consolas,monospace',
    ].join(';'))
    pre.textContent = errs.length ? errs.join('\n\n') : '（没有捕获到异常——界面可能卡在了某个一直不返回的调用上）'
    box.appendChild(pre)

    document.body.appendChild(box)
  }, 8000)
})()
