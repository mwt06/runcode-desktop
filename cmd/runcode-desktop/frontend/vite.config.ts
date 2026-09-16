import { fileURLToPath, URL } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// 麒麟 V10 的 WebKitGTK 是 2.38，而**正则的后行断言要 2.40**（Safari 16.4）。
// remark-gfm 的"裸邮箱自动链接"正好用了一个：
//
//   /(?<=^|\s|\p{P}|\p{S})([-.\w+]+)@([-\w]+(?:\.[-\w]+)+)/gu
//
// 它不是构建期错误——esbuild 发现目标环境不支持后行断言，会把正则字面量降级成
// `new RegExp("...")`，把语法错误推迟到运行时。于是产物能构建、能加载，一旦渲染到
// markdown 就抛 SyntaxError，被错误边界接成"界面渲染出错"。真机实测过。
//
// 这里把断言去掉。语义差别只有一处：原式要求邮箱前面是行首/空白/标点/符号，去掉后
// 前面紧挨着单词字符的也会被识别成邮箱（`见foo@x.com` 这种）。这是**极小**的多识别，
// 换回整个 markdown 渲染不崩，值得。
//
// 只在麒麟那条构建上做。其它平台的 WebKit 都支持后行断言，没有理由让它们跟着降级。
//
// 找不到预期的那段源码时**直接让构建失败**，而不是悄悄跳过：依赖升级后这段替换一旦
// 失配，代价是麒麟上又一次"界面渲染出错"，而那要装到真机上才看得见。
function stripRegexLookbehind(): Plugin {
  // String.raw：普通字符串里 '\s' 会被 JS 解成 's'，匹配不到源码里的正则字面量。
  const FROM = String.raw`/(?<=^|\s|\p{P}|\p{S})([-.\w+]+)@([-\w]+(?:\.[-\w]+)+)/gu`
  const TO = String.raw`/([-.\w+]+)@([-\w]+(?:\.[-\w]+)+)/gu`
  let hit = false
  return {
    name: 'kylin-strip-regex-lookbehind',
    enforce: 'pre',
    transform(code, id) {
      if (!id.includes('mdast-util-gfm-autolink-literal')) return null
      if (!code.includes(FROM)) return null
      hit = true
      return { code: code.replace(FROM, TO), map: null }
    },
    buildEnd() {
      if (!hit) {
        throw new Error(
          'kylin-strip-regex-lookbehind：没找到 mdast-util-gfm-autolink-literal 里那个后行断言正则。' +
          '依赖大概升级了——请确认新版本是否仍含后行断言（WebKitGTK 2.38 解析不了），' +
          '据此更新或删除这个插件，不要直接忽略。',
        )
      }
    },
  }
}

// Wails serves the built assets from dist/ (embedded into the Go binary) and runs
// the dev server during `wails dev`.
export default defineConfig({
  plugins: [react(), tailwindcss(), ...(process.env.VITE_WAILS === 'v2' ? [stripRegexLookbehind()] : [])],
  // '@' = src/. Every cross-directory import uses it, so moving a file between
  // folders never rewrites its importers (only same-folder siblings stay relative).
  // vitest reads this same config, so tests resolve it too; tsconfig `paths` mirrors it.
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      // 麒麟 V10 的外壳跑在 Wails v2 上（见 ../main_kylin.go），它不提供 v3 的运行时。
      // 把 '@wailsio/runtime' 这个**模块名**整个换掉，是让同一份前端在两个 Wails 大
      // 版本上原样复用的唯一接缝：五个调用点与 protogen 的模板都不必知道这件事。
      // 别名只在 VITE_WAILS=v2 时存在，所以三平台的 v3 产物完全不受影响——垫片连打
      // 都不会被打进去。这个变量由 scripts/build-desktop.sh 的 --kylin 分支设置。
      ...(process.env.VITE_WAILS === 'v2'
        ? { '@wailsio/runtime': fileURLToPath(new URL('./src/core/wails-v2-runtime.ts', import.meta.url)) }
        : {}),
    },
  },
  // 测试固定跑在东八区。
  //
  // 界面上的时间一律按**本地时区**渲染（录音起止、纪要里的会议时间都是），那是对的
  // 产品行为，不该为了测试去改。但断言必须有确定的结果：不固定时区的话，同一份
  // 用例在开发机（东八区）绿、在 CI（UTC）红——这正是打包链路修通后第一次跑就撞上的
  // 事故，356 个用例里只有它一个挂，而且只挂在别人的机器上。
  test: {
    env: { TZ: 'Asia/Shanghai' },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // 两个入口 = 两个窗口。主窗加载 /,录音窗加载 /recorder.html——具体哪个窗口
    // 加载哪个 URL 在 main.go 的 WebviewWindowOptions 里指定。
    // 两者共用同一份 src/,所以设计 token 与公共组件天然一致,不会出现
    // 「像两个应用」的割裂感。
    rollupOptions: {
      input: {
        main: fileURLToPath(new URL('./index.html', import.meta.url)),
        recorder: fileURLToPath(new URL('./recorder.html', import.meta.url)),
      },
    },
  },
})
