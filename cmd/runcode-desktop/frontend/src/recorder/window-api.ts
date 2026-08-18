// 录音窗自己的窗口控制。
//
// 这几个方法绑在外壳的 RecorderWindow 服务上（cmd/runcode-desktop/main.go），
// 不在 desktop.App 里——窗口控制天生是 Wails 的东西，而 internal/desktop 对
// Wails 零依赖是整个分层的地基。所以它们也不走 protocol/commands.ts 那条
// protogen 生成的路，单独放这里。
import { Call } from '@wailsio/runtime'

// Wails v3 按 "<包路径>.<类型>.<方法>" 定位绑定方法。RecorderWindow 在
// package main 里，reflect 的 PkgPath() 对 main 包就是 "main"。
const SVC = 'main.RecorderWindow'

// RecorderMode 是录音窗的两个形态：
//   wide 是设计稿里那个「新录音 N」工作区；
//   mini 是缩到右下角、浮在所有应用之上的小条——它存在的全部理由就是
//        用户切到会议软件之后还能看见并能点结束。
export type RecorderMode = 'wide' | 'mini'

// setMode 切换形态。两个形态是同一个窗口改尺寸和位置，不是两个窗口，
// 所以计时器与实时字幕不会因为切换而重建。
export function setMode(mode: RecorderMode): Promise<void> {
  return Call.ByName(`${SVC}.SetMode`, mode) as Promise<void>
}

// show 显示录音窗（大窗形态）。
export function show(): Promise<void> {
  return Call.ByName(`${SVC}.Show`) as Promise<void>
}

// hide 收起录音窗。录音本身不受影响——那是采集层的事。
export function hide(): Promise<void> {
  return Call.ByName(`${SVC}.Hide`) as Promise<void>
}
