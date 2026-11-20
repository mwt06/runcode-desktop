// 消息 HTML 的两条路径：纯静态复用原清理器；内联 JS 只在不透明来源的 iframe 执行。
// 调用方必须为 scripted 使用下面的 sandbox，绝不能同时授予 allow-same-origin。
import { renderStaticHtml, type HtmlFailure } from './static-html'
import { PREVIEW_LIMITS, utf8Bytes } from './static-detect'

export const HTML_SCRIPT_PREVIEW_SANDBOX = 'allow-scripts'
export const HTML_SCRIPT_PREVIEW_CSP = [
  "default-src 'none'", "script-src 'unsafe-inline'", "connect-src 'none'",
  "object-src 'none'", "frame-src 'none'", "base-uri 'none'", "form-action 'none'",
  "font-src 'none'", "media-src 'none'", "style-src 'unsafe-inline'", 'img-src data:',
].join('; ')

export type HtmlDocumentResult =
  | { ok: true; srcdoc: string; mode: 'static' | 'scripted'; channel: string | null; blocked: { scripts: number; externalStyles: number } }
  | { ok: false; reason: HtmlFailure }
export type HtmlPreviewMessage = { height: number } | { zoom: 1 | -1 } | { error: string }

const MESSAGE_TYPE = 'inline-html-preview'
const MAX_MESSAGE_HEIGHT = 1_000_000
const MAX_ERROR_LENGTH = 300
const JS_TYPES = new Set([
  'application/ecmascript', 'application/javascript', 'application/x-ecmascript', 'application/x-javascript',
  'text/ecmascript', 'text/javascript', 'text/javascript1.0', 'text/javascript1.1', 'text/javascript1.2',
  'text/javascript1.3', 'text/javascript1.4', 'text/javascript1.5', 'text/jscript', 'text/livescript',
  'text/x-ecmascript', 'text/x-javascript',
])
let channelSequence = 0

function isExecutableInlineScript(script: Element): boolean {
  if (script.hasAttribute('src') || !(script.textContent ?? '').trim()) return false
  const type = (script.getAttribute('type') ?? '').trim().toLowerCase().split(';', 1)[0].trim()
  return !type || type === 'module' || JS_TYPES.has(type)
}

function newChannel(): string {
  // 序号保证同进程每次准备都不同；随机部分避免旧页面猜测其它实例的频道。
  const random = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID() : Math.random().toString(36).slice(2)
  return `html-${(++channelSequence).toString(36)}-${random}`
}

function removeExternalContent(root: Document | DocumentFragment, blocked: { scripts: number; externalStyles: number }): void {
  for (const el of Array.from(root.querySelectorAll('script[src],link,meta,base,iframe,frame,frameset,object,embed,applet,portal'))) {
    if (el.localName === 'script') blocked.scripts++
    if (el.localName === 'link' && (el.getAttribute('rel') ?? '').toLowerCase().split(/\s+/).includes('stylesheet')) blocked.externalStyles++
    el.remove()
  }
  for (const el of Array.from(root.querySelectorAll('*'))) {
    for (const attr of ['action', 'formaction', 'target', 'formtarget', 'ping', 'download']) el.removeAttribute(attr)
    if (el.localName === 'a' || el.localName === 'area') {
      el.removeAttribute('href')
      el.removeAttribute('xlink:href')
    }
    // 模板仍交给生成脚本克隆，模板内部的外部依赖也提前计数并清除。
    if (el.localName === 'template') removeExternalContent((el as HTMLTemplateElement).content, blocked)
  }
}

function bootstrapScript(channel: string): string {
  // 这里只插入客户端生成的频道，不拼接源码；脚本节点用 textContent 构造，避免原文关闭外壳。
  // 生成代码可以影响自身页面；此处没有把 sandbox 宣称为网络沙箱或阻止自身导航。
  return `(() => {
  const channel = ${JSON.stringify(channel)};
  const send = (payload) => parent.postMessage({type:'${MESSAGE_TYPE}',channel,...payload}, '*');
  const reportError = (message) => send({error:message.slice(0,${MAX_ERROR_LENGTH})});
  let pending = false;
  let lastHeight = 0;
  const measure = () => {
    pending = false;
    const body = document.body;
    if (!body) return;
    const measured = Math.ceil(Math.max(body.scrollHeight, body.getBoundingClientRect().height));
    if (!Number.isFinite(measured)) return;
    const height = Math.min(${MAX_MESSAGE_HEIGHT}, Math.max(1, measured));
    if (height !== lastHeight) { lastHeight = height; send({height}); }
  };
  const schedule = () => {
    if (pending) return;
    pending = true;
    if (typeof window.requestAnimationFrame === 'function') window.requestAnimationFrame(measure);
    else setTimeout(measure, 0);
  };
  const ready = () => {
    schedule();
    if (typeof ResizeObserver === 'function' && document.body) new ResizeObserver(schedule).observe(document.body);
    // 固定高度 body 的溢出变化不一定触发 ResizeObserver；只调度读取，不修改 DOM。
    if (typeof MutationObserver === 'function') new MutationObserver(schedule).observe(document.documentElement, {
      subtree:true, childList:true, characterData:true, attributes:true, attributeFilter:['style','class']
    });
  };
  document.addEventListener('DOMContentLoaded', ready, {once:true});
  window.addEventListener('load', schedule, true);
  if (document.readyState !== 'loading') ready();
  document.addEventListener('wheel', (event) => {
    if ((!event.ctrlKey && !event.metaKey) || event.deltaY === 0) return;
    event.preventDefault();
    send({zoom:event.deltaY < 0 ? 1 : -1});
  }, {passive:false,capture:true});
  document.addEventListener('submit', (event) => event.preventDefault(), true);
  window.addEventListener('error', () => reportError('页面脚本执行失败，部分内容可能未显示'), true);
  window.addEventListener('unhandledrejection', () => reportError('页面异步脚本执行失败，部分内容可能未显示'));
  document.addEventListener('securitypolicyviolation', (event) => {
    if (/^(?:script-src|style-src|connect-src|img-src)(?:-|$)/.test(event.effectiveDirective || event.violatedDirective || ''))
      reportError('页面依赖资源被阻止，部分内容可能未显示');
  });
})();`
}

export function prepareHtmlPreview(source: string): HtmlDocumentResult {
  if (utf8Bytes(source) > PREVIEW_LIMITS.markupBytes) return { ok: false, reason: 'too-large' }
  let doc: Document
  try {
    doc = new DOMParser().parseFromString(source, 'text/html')
  } catch {
    return { ok: false, reason: 'parse-error' }
  }
  if (!Array.from(doc.querySelectorAll('script')).some(isExecutableInlineScript)) {
    const result = renderStaticHtml(source)
    return result.ok ? { ...result, mode: 'static', channel: null } : result
  }

  // scripted 的 scripts 只计真正删掉的外部脚本；保留的内联脚本不能被标成“已阻止”。
  const blocked = { scripts: 0, externalStyles: 0 }
  removeExternalContent(doc, blocked)
  const channel = newChannel()
  const bootstrap = doc.createElement('script')
  bootstrap.textContent = bootstrapScript(channel)
  const charset = doc.createElement('meta')
  charset.setAttribute('charset', 'utf-8')
  const csp = doc.createElement('meta')
  csp.setAttribute('http-equiv', 'Content-Security-Policy')
  csp.setAttribute('content', HTML_SCRIPT_PREVIEW_CSP)
  // CSP 必须先于 bootstrap、生成样式和生成脚本，原文所有 meta 已被移除。
  doc.head.prepend(csp, charset, bootstrap)
  if (!doc.body) doc.documentElement.appendChild(doc.createElement('body'))
  return { ok: true, srcdoc: `<!doctype html>${doc.documentElement.outerHTML}`, mode: 'scripted', channel, blocked }
}

// 只解码有界数据，不处理消息中的任意指令。来源 window 与 origin 必须由父组件另行验证。
export function readHtmlPreviewMessage(data: unknown, channel: string): HtmlPreviewMessage | null {
  if (!channel || !data || typeof data !== 'object' || Array.isArray(data)) return null
  const value = data as Record<string, unknown>
  if (value.type !== MESSAGE_TYPE || value.channel !== channel) return null
  if (['height', 'zoom', 'error'].filter((key) => Object.prototype.hasOwnProperty.call(value, key)).length !== 1) return null
  if (typeof value.height === 'number' && Number.isFinite(value.height) && value.height > 0 && value.height <= MAX_MESSAGE_HEIGHT) return { height: value.height }
  if (value.zoom === 1 || value.zoom === -1) return { zoom: value.zoom }
  if (typeof value.error === 'string' && value.error.length <= MAX_ERROR_LENGTH && value.error.trim()) return { error: value.error.trim() }
  return null
}
