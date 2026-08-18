// 起始页的「恢复上次选择」逻辑。
//
// 上次用的模型/工作区/租户都已经存在 desktop.json 里（后端 saveConfig 在开会话时
// 写入），这里负责把它们还原成表单的初值、并判断能不能直接跳过表单进会话。
//
// 曾经这两件事只认平台(通行证)模型：模型初值写的是
//   initial.provider === 'passport' && initial.model ? `passport:${initial.model}` : ''
// 而自定义连接存下来的是 provider='anthropic'(或 openai/…) + customModelName='xxx'，
// 于是每次启动都落进 else 分支变成空选择——**存了却读不回来**，用户每次都要重选一遍。
// 判据因此改成「先看 customModelName，再看 provider==='passport'」，与 buildRequest
// 写回时的两个分支严格对称。
import type { CustomModel, StartSessionRequest } from '@/core/bridge'

// modelChoice 的取值：'passport:<模型id>' | 'custom:<连接档名>' | ''（未选）。
export function initialModelChoice(initial: Partial<StartSessionRequest>): string {
  const custom = (initial.customModelName ?? '').trim()
  if (custom) return `custom:${custom}`
  const model = (initial.model ?? '').trim()
  if (initial.provider === 'passport' && model) return `passport:${model}`
  return ''
}

// 自定义连接能否自动进入。与通行证那条路径（canAutoStartPassport）并列：
// 自定义连接直连自己的 Base URL，**不依赖登录态，也不经租户**，所以只需确认
// 工作区还在、且那份连接档没有被删掉或改名。
//
// 注意登录门优先于它：未登录且没开「免登录」时起始页显示的是登录页，调用方在
// 那种状态下不评估自动进入（见 pages/start/index.tsx 的守卫）。
export function canAutoStartCustom(
  initial: Partial<StartSessionRequest>,
  customModels: CustomModel[],
): boolean {
  if (!(initial.cwd ?? '').trim()) return false
  const name = (initial.customModelName ?? '').trim()
  if (!name) return false
  return customModels.some((model) => model.name === name)
}
