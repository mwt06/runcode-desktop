// bridge is the frontend's single facade over the generated protocol layer
// (src/protocol/*, protogen output — regenerate with `go generate ./pkg/protocol`).
// It re-exports the wire types, the typed event helpers, and the typed command
// functions, and keeps only the frontend-specific pieces: UI-side data shapes
// (PlanSnapshot), narrowing helpers (isEditRecord), and the few command wrappers
// that add real client-side logic (readArtifactBytes, copyText).
//
// Consumers import from './bridge' only; nothing outside src/protocol touches
// the Wails runtime directly, and runtime access stays here + in the generated
// events module.

// ---- wire types (generated single source of truth) ----------------------------
export type {
  AgentInfo,
  AgentList,
  AgentProblem,
  AgentSaveRequest,
  ApprovalSummary,
  AssistantDelta,
  CompactResult,
  ContextAuditInfo,
  CustomModel,
  Decision,
  EditDiff,
  EditRecord,
  Envelope,
  ErrCode,
  EventName,
  FileReference,
  HarmAutoAllow,
  Info,
  MarketSkill,
  MCPServerInfo,
  MCPServerInput,
  McpMarketEntry,
  MemoryInfo,
  OutputLine,
  PassportModel,
  PassportStatus,
  PermissionRequest,
  PlanApproveRequest,
  PlanApproveResult,
  PlanDoc,
  PlanRun,
  PlanStage,
  PlanState,
  SkillInstallProgress,
  SkillMarketPage,
  PlanStep,
  ProjectContextInfo,
  RecorderDevice,
  RecorderDeviceList,
  RecorderLevel,
  RecorderSettings,
  RecorderState,
  RecorderTrack,
  RecorderTranscript,
  RecordingInfo,
  ResultImage,
  ResumedBlock,
  ResumedSession,
  ResumedTool,
  SaveCustomModelRequest,
  SessionInfo,
  SessionRenamed,
  OpenSessionInfo,
  SessionSummary,
  SkillInfo,
  SkillList,
  SkillProblem,
  SkillSaveRequest,
  StartRecordingRequest,
  StartSessionRequest,
  ToolEvent,
  ToolEventType,
  ToolInfo,
  TurnEnd,
  TurnError,
  UpdateInfo,
  UpdateStage,
  Warning,
} from './protocol/types'
// The generated wire error type is named `Error`, which shadows the global Error;
// expose it under a collision-free name (the old handwritten bridge exported no
// error type, so nothing depends on the bare name).
export type { Error as ProtocolError } from './protocol/types'
export { Events, ToolEventTypes, Decisions, ErrCodes, PlanStages, PlanStates, SkillInstallStages, UpdateStages, ProtocolVersion } from './protocol/types'

export type { PassportTenant } from './protocol/types'

// ---- events (generated; unwraps the Envelope exactly like the old helpers) ----
export { onEvent, onEnvelope } from './protocol/events'
export type { EventMap } from './protocol/events'

// ---- commands (generated; same lowerCamel names the old helpers used) ---------
export {
  activeTenant,
  compact,
  contextAuditStatus,
  deleteAgent,
  deleteCustomModel,
  deleteMCPServer,
  deleteSkill,
  importAgent,
  importSkill,
  injectMessage,
  injectMessageWithImages,
  interrupt,
  listAgents,
  listCustomModels,
  listEdits,
  listFiles,
  listMCPServers,
  listSessions,
  listSkills,
  listTools,
  loadConfig,
  mcpMarket,
  skillMarket,
  installMarketSkill,
  newSession,
  status as sessionStatus,
  openSession,
  openSessions,
  focusSession,
  closeSession,
  openExternal,
  passportCancelLogin,
  passportLogin,
  passportLogout,
  passportModels,
  passportStatus,
  passportValidate,
  passportTenants,
  pickImageAttachment,
  pickWorkspaceFolder,
  planApprove,
  planCancel,
  planStatus,
  planUpdate,
  readArtifact,
  readRecordingTranscript,
  recorderDevices,
  recorderSettings,
  recorderStatus,
  listRecordings,
  saveRecorderSettings,
  deleteRecording,
  startRecording,
  pauseRecording,
  resumeRecording,
  stopRecording,
  discardRecording,
  readMemory,
  readProjectContext,
  reloadMCPServers,
  renderOfficePDF,
  resolveArtifactPath,
  resolvePermission,
  // renamed: the old bridge exposed the Reset command as resetHistory
  reset as resetHistory,
  resumeSession,
  deleteSession,
  revealInFolder,
  reviewEdit,
  revertEdit,
  saveAgent,
  saveCustomModel,
  saveMCPServer,
  saveProjectContext,
  saveSettings,
  saveSkill,
  sendMessage,
  sendMessageWithImages,
  sessionModels,
  setActiveTenant,
  setAgentEnabled,
  setContextAudit,
  setMCPServerEnabled,
  setModel,
  setPermissionMode,
  setPlanMode,
  setReasoningScenario,
  setSkillEnabled,
  setThinkingEffort,
  setToolEnabled,
  setWebProxy,
  startSession,
  updateStatus,
  checkUpdate,
  downloadUpdate,
  cancelUpdateDownload,
  installUpdate,
  webProxy,
} from './protocol/commands'

import { Browser, Clipboard } from '@wailsio/runtime'
import {
  savePastedFile as savePastedFileCmd,
  readArtifactBytes as readArtifactBytesCmd,
  switchModel as switchModelCmd,
} from './protocol/commands'
import type { EditRecord, SessionInfo } from './protocol/types'

// switchModel spans platform (passport) and custom direct-connection models: a
// same-connection platform swap is in place, any connection change rebuilds the
// session while preserving conversation history. kind is 'custom' (name = the
// custom model's display name) or 'platform' (name = the model id). The wire
// command takes a plain string; this wrapper keeps the kind union checked.
export const switchModel = (sessionID: string, kind: 'platform' | 'custom', name: string): Promise<SessionInfo> =>
  switchModelCmd(sessionID, kind, name)

// readArtifactBytes returns a workspace file's raw bytes for renderers that need the
// binary (Office docs). It goes through the bridge (base64) rather than the loopback
// server to sidestep cross-origin fetch restrictions; see the Go ReadArtifactBytes.
export const readArtifactBytes = async (relPath: string): Promise<ArrayBuffer> => {
  const b64 = await readArtifactBytesCmd(relPath)
  const bin = atob(b64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return bytes.buffer
}

// errText renders a command rejection for display. Hosts serialize command
// failures as protocol.Error JSON (docs/protocol.md §5): parse it and show the
// human message; anything unparsable (plain string, runtime error) is shown
// as-is — the tolerant-client rule, so a host that has not adopted structured
// errors for some path never breaks the UI.
export function errText(e: unknown): string {
  const s = e instanceof Error ? e.message : String(e)
  try {
    const o = JSON.parse(s) as { message?: unknown }
    if (o && typeof o === 'object' && typeof o.message === 'string' && o.message !== '') return o.message
  } catch {
    /* not a structured error */
  }
  return s
}

// errCode reads the machine-readable code off a command rejection (protocol.Error
// JSON, docs/protocol.md §5), or '' when the host did not send one. Use it where
// the UI must *behave* differently — e.g. a preview target that no longer exists
// closes quietly instead of showing a message — never for what to display; that
// is errText's job. Matching on the message text would break the moment the
// wording is edited or translated.
export function errCode(e: unknown): string {
  const s = e instanceof Error ? e.message : String(e)
  try {
    const o = JSON.parse(s) as { code?: unknown }
    if (o && typeof o === 'object' && typeof o.code === 'string') return o.code
  } catch {
    /* not a structured error */
  }
  return ''
}

// openInBrowser opens an absolute URL in the system browser via the Wails
// runtime (in-app anchors would navigate the webview itself).
export function openInBrowser(url: string): void {
  void Browser.OpenURL(url)
}

// copyText writes to the clipboard via the Wails runtime, falling back to the
// browser clipboard API.
export async function copyText(text: string): Promise<void> {
  try {
    await Clipboard.SetText(text)
  } catch {
    await navigator.clipboard.writeText(text)
  }
}

// ---- frontend-only data shapes and narrowing helpers --------------------------

// isEditRecord narrows a ToolEvent's opaque `data` to an EditRecord (Write/Edit),
// distinguishing it from TodoWrite's PlanSnapshot.
export function isEditRecord(data: unknown): data is EditRecord {
  return !!data && typeof data === 'object' && 'snapshotId' in (data as object) && 'relPath' in (data as object)
}

// PlanItem is one task in the model's TodoWrite list. status is one of
// 'pending' | 'in_progress' | 'completed'; activeForm is the present-continuous
// label shown while the item is in progress.
export interface PlanItem {
  content: string
  status: string
  activeForm?: string
}

// PlanSnapshot is the full task list the TodoWrite tool attaches to its progress
// event's `data` field, rendered by the right-rail progress board.
export interface PlanSnapshot {
  items: PlanItem[]
  done: number
  total: number
}

// savePastedFile 把粘贴进来的文件交给后端落盘，返回可当附件用的绝对路径。
//
// 为什么要走这一趟而不是直接拿路径：WebView 里粘贴到的只有字节。剪贴板里的截图
// 本来就不是文件，而从资源管理器复制来的文件，浏览器的安全模型也不把真实路径交给
// 页面——File 对象上没有它。所以字节经 base64 送到 Go 那边落盘（见 SavePastedFile），
// 之后这个路径和"选一张图"得到的路径完全等价。
export const savePastedFile = async (file: File, name: string): Promise<string> => {
  const bytes = new Uint8Array(await file.arrayBuffer())
  return savePastedFileCmd(name, base64(bytes))
}

// base64 分块编码。
//
// 不能写成 String.fromCharCode(...bytes)：展开运算符是按实参传的，一张几 MB 的截图
// 就是几百万个实参，直接爆栈（RangeError: too many arguments）。分块之后每次调用的
// 实参数是固定的，多大的文件都只是多循环几轮。
function base64(bytes: Uint8Array): string {
  const CHUNK = 0x8000
  let binary = ''
  for (let i = 0; i < bytes.length; i += CHUNK) {
    binary += String.fromCharCode(...bytes.subarray(i, i + CHUNK))
  }
  return btoa(binary)
}
