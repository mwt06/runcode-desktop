import React from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource/ibm-plex-sans/400.css'
import '@fontsource/ibm-plex-sans/500.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import '@fontsource/ibm-plex-mono/600.css'
import 'highlight.js/styles/github.css'
import App from './App'
import { ToolPreview, ThinkingPreview } from '@/dev/previews'
import './styles.css'

const preview = new URLSearchParams(location.search).get('preview')
const previewTools = preview === 'tools'
const previewThinking = preview === 'thinking'

// A render error in any component should show a message instead of a blank
// WebView. Event-handler errors aren't caught here, but render crashes are.
class ErrorBoundary extends React.Component<{ children: React.ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) {
    return { error }
  }
  render() {
    if (this.state.error) {
      return (
        <div style={{ padding: 28, fontFamily: 'monospace', color: '#c0563d' }}>
          <h3 style={{ margin: '0 0 10px' }}>界面渲染出错</h3>
          <pre style={{ whiteSpace: 'pre-wrap', color: '#6f6753' }}>{String(this.state.error?.stack || this.state.error?.message || this.state.error)}</pre>
          <button
            style={{ marginTop: 12, padding: '8px 16px', border: '1px solid #e4e7ef', borderRadius: 8, background: '#fff', cursor: 'pointer' }}
            onClick={() => this.setState({ error: null })}
          >
            重试
          </button>
        </div>
      )
    }
    return this.props.children
  }
}

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ErrorBoundary>
      {previewTools ? <ToolPreview /> : previewThinking ? <ThinkingPreview /> : <App />}
    </ErrorBoundary>
  </React.StrictMode>,
)
