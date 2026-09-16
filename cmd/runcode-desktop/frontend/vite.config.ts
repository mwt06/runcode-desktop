import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Wails serves the built assets from dist/ (embedded into the Go binary) and runs
// the dev server during `wails dev`.
export default defineConfig({
  plugins: [react(), tailwindcss()],
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
