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
  },
})
