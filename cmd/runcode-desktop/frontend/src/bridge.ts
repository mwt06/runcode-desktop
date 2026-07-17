// bridge is the frontend's single facade over the generated protocol layer
// (src/protocol/*, protogen output — regenerate with `go generate ./pkg/protocol`).
// It re-exports the wire types, the typed event helpers, and the typed command
// functions, and keeps only the frontend-specific pieces: UI-side data shapes
// (PlanSnapshot), narrowing helpers (isEditRecord), and the few command wrappers
// that add real client-side logic (readArtifactBytes, copyText).
//
// Consumers import from './bridge' only; nothing outside src/protocol touches
// window.go directly, and window.runtime access stays here + in the generated
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
  MCPServerInfo,
  MCPServerInput,
  MemoryInfo,
  OutputLine,
  PassportModel,
  PassportStatus,
  PermissionRequest,
  ProjectContextInfo,
  ResultImage,
  ResumedBlock,
  ResumedSession,
  ResumedTool,
  SessionInfo,
  SessionRenamed,
  SessionSummary,
  SkillInfo,
  SkillList,
  SkillProblem,
  SkillSaveRequest,
  StartSessionRequest,
  ToolEvent,
  ToolEventType,
  ToolInfo,
  TurnEnd,
  TurnError,
  Warning,
} from './protocol/types'
// The generated wire error type is named `Error`, which shadows the global Error;
// expose it under a collision-free name (the old handwritten bridge exported no
// error type, so nothing depends on the bare name).
export type { Error as ProtocolError } from './protocol/types'
export { Events, ToolEventTypes, Decisions, ErrCodes, ProtocolVersion } from './protocol/types'

// PassportTenant: the wire type carries only id/name — the Go backend
// (protocol.PassportTenant) has never sent parentId; the old handwritten
// `parentId: string` was drift. The tenant-tree UI reads parentId and treats its
// absence as "root", so it renders a flat list at runtime. Keep an optional
// parentId here so that code keeps compiling with unchanged behavior.
import type { PassportTenant as WirePassportTenant } from './protocol/types'
export type PassportTenant = WirePassportTenant & { parentId?: string }

// ---- events (generated; unwraps the Envelope exactly like the old helpers) ----
export { onEvent, onEnvelope } from './protocol/events'
export type { EventMap } from './protocol/events'

// ---- commands (generated; same lowerCamel names the old helpers used) ---------
export {
  activeTenant,
  compact,
  deleteAgent,
  deleteCustomModel,
  deleteMCPServer,
  deleteSkill,
  importAgent,
  importSkill,
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
  newSession,
  openExternal,
  passportCancelLogin,
  passportLogin,
  passportLogout,
  passportModels,
  passportStatus,
  passportTenants,
  pickImageAttachment,
  pickWorkspaceFolder,
  readArtifact,
  readMemory,
  readProjectContext,
  resolveArtifactPath,
  resolvePermission,
  // renamed: the old bridge exposed the Reset command as resetHistory
  reset as resetHistory,
  resumeSession,
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
  switchWorkspace,
  webProxy,
} from './protocol/commands'

import {
  readArtifactBytes as readArtifactBytesCmd,
  switchModel as switchModelCmd,
} from './protocol/commands'
import type { EditRecord, SessionInfo } from './protocol/types'

// switchModel spans platform (passport) and custom direct-connection models: a
// same-connection platform swap is in place, any connection change rebuilds the
// session while preserving conversation history. kind is 'custom' (name = the
// custom model's display name) or 'platform' (name = the model id). The wire
// command takes a plain string; this wrapper keeps the kind union checked.
export const switchModel = (kind: 'platform' | 'custom', name: string): Promise<SessionInfo> =>
  switchModelCmd(kind, name)

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

// copyText writes to the clipboard via the Wails runtime, falling back to the
// browser clipboard API.
export async function copyText(text: string): Promise<void> {
  try {
    await window.runtime.ClipboardSetText(text)
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
