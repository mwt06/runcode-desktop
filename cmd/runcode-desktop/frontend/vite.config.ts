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
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
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
