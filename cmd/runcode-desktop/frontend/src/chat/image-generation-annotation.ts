/**
 * 行下预览「标注」角标等次要入口的开关（实现代码保留）。
 *
 * 本开关当前控制：
 * - 行下预览角标铅笔「标注」
 *
 * 查看器 mode/脏状态、image-edit-* 和 SaveImageGenerationEdit 均完整保留。
 * 恢复入口：改为 true，并在 App.tsx 接回 onRasterImageAnnotate。
 */
export const IMAGE_GENERATION_ANNOTATION_UI = false
