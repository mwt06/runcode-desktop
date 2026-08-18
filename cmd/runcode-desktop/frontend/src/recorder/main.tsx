// 录音窗的入口。Vite 的第二个入口（recorder.html），与主窗共用同一套
// styles.css 与 ui/ 的 token，所以两个窗口的观感天然一致——不会出现
// 「像两个应用」的割裂感。
import React from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource/ibm-plex-sans/400.css'
import '@fontsource/ibm-plex-sans/500.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import { RecorderApp } from './app'
import '../styles.css'

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <RecorderApp />
  </React.StrictMode>,
)
