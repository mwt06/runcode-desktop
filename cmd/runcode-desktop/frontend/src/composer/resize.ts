export const DEFAULT_COMPOSER_HEIGHT = 96
export const MIN_COMPOSER_HEIGHT = 64
const MIN_CHAT_HEIGHT = 160

export interface ComposerHeightBounds { min: number; max: number }

export function parseComposerHeight(stored: number): number {
  return Number.isFinite(stored) && stored > 0
    ? Math.max(MIN_COMPOSER_HEIGHT, Math.round(stored))
    : DEFAULT_COMPOSER_HEIGHT
}

// available 是输入框与聊天滚动区能共用的高度，不含附件、工具条和计划说明。
export function composerHeightBounds(viewport: number, available: number): ComposerHeightBounds {
  return {
    min: MIN_COMPOSER_HEIGHT,
    max: Math.max(MIN_COMPOSER_HEIGHT, Math.floor(Math.min(viewport / 2, available - MIN_CHAT_HEIGHT))),
  }
}

export function clampComposerHeight(wanted: number, bounds: ComposerHeightBounds): number {
  return Math.min(bounds.max, Math.max(bounds.min, parseComposerHeight(wanted)))
}

export function draggedComposerHeight(startHeight: number, startY: number, y: number, bounds: ComposerHeightBounds): number {
  // 底部不动：向上拖才是增高。先夹下限，负值不能被当成“缺省偏好”恢复默认。
  return Math.min(bounds.max, Math.max(bounds.min, Math.round(startHeight + startY - y)))
}
