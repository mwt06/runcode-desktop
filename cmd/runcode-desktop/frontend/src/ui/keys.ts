// 输入法（IME）组字期间的按键必须原样交还给输入法，不能被当成快捷键。中日韩输入
// 法用 Enter 上屏候选词、用方向键翻候选页，这些键在组字过程中和"发送消息""选中候选
// 文件"毫无关系。

// ComposingKeyEvent 是判定所需的最小形状；React 的 KeyboardEvent 结构上满足它，
// 测试可以直接传字面量。
export type ComposingKeyEvent = {
  nativeEvent: { isComposing?: boolean }
  keyCode?: number
}

// isComposingKey 判断这次按键是否发生在输入法组字过程中。
//
// 两个判据缺一不可，因为浏览器引擎对"上屏那一下 Enter"的事件顺序并不一致：
//
//   - Chromium（Windows 的 WebView2）先派发 keydown 再派发 compositionend，此时
//     nativeEvent.isComposing 为 true。
//   - WebKit（macOS 的 WKWebView，即桌面版 mac 端）反过来，compositionend 先到，
//     keydown 时 isComposing 已经是 false——只看 isComposing 在 mac 上就漏了，
//     表现正是"用输入法打完中文按回车，消息直接发出去"。这种 keydown 的 keyCode
//     仍是 229（IME 处理中的历史约定），所以用它兜底。
//
// keyCode 虽已废弃，但至今是这个场景唯一可靠的信号，没有标准替代品。
export function isComposingKey(e: ComposingKeyEvent): boolean {
  return e.nativeEvent?.isComposing === true || e.keyCode === 229
}
