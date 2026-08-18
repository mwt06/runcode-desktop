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
