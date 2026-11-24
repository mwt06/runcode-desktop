// 静态预览失败码 → 用户可读说明。不回显源码或堆栈；生产界面只展示短语，
// 开发构建可附带失败码便于对照。

export type PreviewFailureCode = string

const LABELS: Record<string, string> = {
  empty: '内容为空',
  'too-large': '内容过大',
  'too-complex': '图过于复杂',
  'unsupported-type': '暂不支持此图型',
  'platform-unsupported': '当前环境不支持该预览',
  directive: '含有不允许的配置指令',
  frontmatter: '含有不允许的配置头',
  interaction: '含有不允许的交互绑定',
  syntax: '语法无法解析',
  'load-failed': '渲染器加载失败',
  busy: '预览队列繁忙',
  timeout: '渲染超时',
  cancelled: '已取消',
  'unsafe-output': '输出未通过安全检查',
  'parse-error': '无法解析',
  'not-svg': '不是有效的 SVG',
  'unsafe-dtd': '含有不安全的文档声明',
  'needs-script': '页面依赖未能加载的脚本，暂时无法显示',
  'no-language': '未标注语言',
  language: '语言不在预览范围',
  unclosed: '代码块未闭合',
  source: '已指定仅显示源码',
  'html-fragment': 'HTML 片段需完整文档或 preview 标记',
  'not-html': '不是有效的 HTML',
}

export function previewFailureLabel(code: PreviewFailureCode | null | undefined): string {
  if (!code) return '预览不可用'
  return LABELS[code] ?? '预览不可用'
}

export function previewFailureHint(code: PreviewFailureCode | null | undefined): string {
  const label = previewFailureLabel(code)
  if (import.meta.env.DEV && code) return `${label}（${code}）`
  return label
}

export function logPreviewFailure(parts: {
  kind: string
  reason: PreviewFailureCode
  messageId?: string
}): void {
  // 仅开发构建写控制台：生产包不落诊断；内容只含类型与失败码，不含用户源码。
  if (!import.meta.env.DEV) return
  console.debug('[inline-preview]', parts.kind, parts.reason, parts.messageId ?? '')
}
