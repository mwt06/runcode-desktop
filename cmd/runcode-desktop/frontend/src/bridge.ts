// bridge wraps the Wails-bound backend methods and event stream in typed helpers,
// keeping window.go/window.runtime access in one place.

export interface SessionInfo {
  sessionId: string
  model: string
  cwd: string
  permissionMode: string
  planMode?: boolean
  reasoningScenario?: string
  thinkingEffort?: string
  maxContextTokens?: number
  previewBaseURL?: string
  inputPricePerMTok: number
  outputPricePerMTok: number
  pricingSource: string
}

export interface StartSessionRequest {
  cwd: string
  provider?: string
  model?: string
  baseURL?: string
  apiKey?: string
  authToken?: string
  permissionMode?: string
  reasoningScenario?: string
  thinkingEffort?: string
  maxTokens?: number
  maxContextTokens?: number
  maxHistoryMessages?: number
  resume?: string
  continue?: boolean
  /** MRU list of previously opened workspaces, maintained backend-side and offered
   *  in the start form. Ignored when sent from the frontend. */
  recentWorkspaces?: string[]
}

export interface ApprovalSummary {
  ToolName: string
  Operation: string
  Risk: string
  ResourceTypes: string[] | null
  ResourceScope: string
  ResourceCount: number
  MutationKind: string
  CommandCategory: string
  CommandSummary: string
  NetworkHost: string
  MCPServer: string
  MCPTool: string
  PolicyRule: string
}

export interface PermissionRequest {
  id: string
  summary: ApprovalSummary
  targets: string[] | null
  command: string
  // Set when the model harm gate flagged this action as potentially harmful.
  harmReason?: string
  // Set when this is an MCP sampling approval (a server asking to use the model);
  // names the requesting server.
  samplingServer?: string
}

export interface ToolEvent {
  type: 'started' | 'progress' | 'output' | 'completed' | 'failed' | 'agent_delta' | 'agent_usage'
  toolName?: string
  toolUseID?: string
  // Set when this event comes from a sub-agent's child session: it nests under the
  // Task card whose tool-use id this matches. agentName names the sub-agent.
  parentToolUseID?: string
  agentName?: string
  // Raw tool-call arguments (object on live events, JSON string on resumed ones).
  input?: unknown
  // Structured side-channel payload some tools attach for the UI. TodoWrite puts a
  // PlanSnapshot here so the progress board can render the full task list; other
  // tools may leave it unset.
  data?: unknown
  message?: string
  files?: { path: string; kind?: string }[]
  filesTotal?: number
  output?: { stream?: string; text: string }[]
  outputTotal?: number
  outputTruncated?: boolean
  // An inline image the tool returned (e.g. Read of an image); data is base64.
  image?: { mediaType?: string; data?: string; url?: string }
  // Token totals + run time, set on an agent_usage event (a sub-agent reporting its
  // own spend and wall-clock duration).
  inputTokens?: number
  outputTokens?: number
  durationMs?: number
}

// EditRecord is the per-edit metadata a Write/Edit tool event carries on `data`
// (live), and that ListEdits returns (resume). Anchored to a tool step by toolUseId.
export interface EditRecord {
  snapshotId: string
  toolUseId: string
  relPath: string
  added: number
  removed: number
  created: boolean
  reverted?: boolean
}

// EditDiff is the red/green review of one edit (turn baseline vs the turn's latest
// content for that file).
export interface EditDiff {
  relPath: string
  created: boolean
  lines: { stream?: string; text: string }[]
}

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

export interface TurnEnd {
  text: string
  stopReason: string
  iterations: number
  toolResultCount: number
  inputTokens: number
  outputTokens: number
  // The final request's input-token count — the current context-window occupancy,
  // shown against maxContextTokens as a usage bar.
  contextTokens?: number
  // True when the turn stopped because the user denied a tool and asked to halt,
  // rather than the model finishing on its own.
  stopped?: boolean
  // The turn's wall-clock time in milliseconds, shown next to the per-reply usage.
  durationMs?: number
}

export interface SessionSummary {
  id: string
  title: string
  when: string
  turns: number
}

export interface ResumedTool {
  toolName?: string
  toolUseId?: string
  path?: string
  isError?: boolean
  output?: string
  // Attached client-side after resume (from ListEdits, by toolUseId) so an edited
  // file's card + undo/review re-render; not sent by the backend resume payload.
  data?: EditRecord
}

export interface ResumedBlock {
  kind: 'user' | 'assistant' | 'tool'
  text?: string
  tool?: ResumedTool
}

export interface ToolInfo {
  name: string
  description: string
  source: string
  server?: string
  concurrencySafe: boolean
}

export interface MCPServerInfo {
  name: string
  transport: string
  command?: string
  args?: string[]
  env?: Record<string, string>
  dir?: string
  url?: string
  headers?: Record<string, string>
  enabled: boolean
  connected: boolean
  toolCount: number
}

export interface MCPServerInput {
  originalName: string
  name: string
  transport: string
  command: string
  args: string[]
  env: Record<string, string>
  dir: string
  url: string
  headers: Record<string, string>
  enabled: boolean
}

export interface ProjectContextInfo {
  path: string
  name: string
  content: string
  exists: boolean
}

export interface MemoryInfo {
  user: string[]
  project: string[]
}

export interface SkillInfo {
  name: string
  description: string
  body: string
  source: string
  path: string
  editable: boolean
}

export interface SkillProblem {
  dir: string
  reason: string
}

export interface SkillList {
  skills: SkillInfo[]
  problems: SkillProblem[]
}

export interface SkillSaveRequest {
  originalName?: string
  name: string
  description: string
  body: string
  scope: string
}
// (skill bridge helpers are declared with the other exports above)

export interface AgentInfo {
  name: string
  description: string
  tools: string
  model: string
  prompt: string
  source: string
  path: string
  editable: boolean
}

export interface AgentProblem {
  path: string
  reason: string
}

export interface AgentList {
  agents: AgentInfo[]
  problems: AgentProblem[]
}

export interface AgentSaveRequest {
  originalName?: string
  name: string
  description: string
  tools: string
  model: string
  prompt: string
  scope: string
}

export interface ResumedSession {
  info: SessionInfo
  blocks: ResumedBlock[]
  // Estimated context occupancy of the reopened history, to seed the usage bar
  // before the first turn reports an exact count.
  contextTokens?: number
}

export interface PassportStatus {
  loggedIn: boolean
  userId?: string
  userName?: string
  name?: string
  nickname?: string
  avatar?: string
  tenantId?: string
}

export interface PassportModel {
  id: string
  ownedBy: string
}

export interface CustomModel {
  name: string
  model: string
  baseURL: string
  apiKey?: string
}

const app = () => window.go.desktop.App

export const startSession = (req: StartSessionRequest) =>
  app().StartSession(req) as Promise<SessionInfo>
export const sendMessage = (text: string) => app().SendMessage(text)
export const sendMessageWithImages = (text: string, paths: string[]) => app().SendMessageWithImages(text, paths)
export const pickImageAttachment = () => app().PickImageAttachment() as Promise<string>
export const interrupt = () => app().Interrupt()
export const resolvePermission = (id: string, decision: string) =>
  app().ResolvePermission(id, decision)
export const setPermissionMode = (mode: string) => app().SetPermissionMode(mode)
export const setModel = (model: string) => app().SetModel(model)
export const setPlanMode = (on: boolean) => app().SetPlanMode(on) as Promise<SessionInfo>
export const setReasoningScenario = (s: string) => app().SetReasoningScenario(s) as Promise<SessionInfo>
export const setThinkingEffort = (e: string) => app().SetThinkingEffort(e) as Promise<SessionInfo>
export const resetHistory = () => app().Reset()
export const compact = () => app().Compact() as Promise<{ before: number; after: number }>
export const listSessions = () => app().ListSessions() as Promise<SessionSummary[] | null>
export const resumeSession = (id: string) => app().ResumeSession(id) as Promise<ResumedSession>
export const newSession = () => app().NewSession() as Promise<SessionInfo>
export const pickWorkspaceFolder = () => app().PickWorkspaceFolder() as Promise<string>
export const switchWorkspace = (dir: string) => app().SwitchWorkspace(dir) as Promise<SessionInfo>
export const loadConfig = () => app().LoadConfig() as Promise<StartSessionRequest>
export const listTools = () => app().ListTools() as Promise<ToolInfo[] | null>
export const listMCPServers = () => app().ListMCPServers() as Promise<MCPServerInfo[] | null>
export const saveMCPServer = (s: MCPServerInput) => app().SaveMCPServer(s) as Promise<void>
export const deleteMCPServer = (name: string) => app().DeleteMCPServer(name) as Promise<void>
export const setMCPServerEnabled = (name: string, enabled: boolean) => app().SetMCPServerEnabled(name, enabled) as Promise<void>
export const readProjectContext = () => app().ReadProjectContext() as Promise<ProjectContextInfo>
export const saveProjectContext = (content: string) => app().SaveProjectContext(content) as Promise<void>
export const readMemory = () => app().ReadMemory() as Promise<MemoryInfo>
export const listFiles = () => app().ListFiles() as Promise<string[] | null>
export const readArtifact = (relPath: string) => app().ReadArtifact(relPath) as Promise<string>
export const openExternal = (relPath: string) => app().OpenExternal(relPath) as Promise<void>
export const revealInFolder = (relPath: string) => app().RevealInFolder(relPath) as Promise<void>
export const resolveArtifactPath = (relPath: string) => app().ResolveArtifactPath(relPath) as Promise<string>
export const revertEdit = (snapshotId: string) => app().RevertEdit(snapshotId) as Promise<void>
export const reviewEdit = (snapshotId: string) => app().ReviewEdit(snapshotId) as Promise<EditDiff>
export const listEdits = () => app().ListEdits() as Promise<EditRecord[] | null>

// copyText writes to the clipboard via the Wails runtime, falling back to the
// browser clipboard API.
export async function copyText(text: string): Promise<void> {
  try {
    await window.runtime.ClipboardSetText(text)
  } catch {
    await navigator.clipboard.writeText(text)
  }
}

export const listSkills = () => app().ListSkills() as Promise<SkillList>
export const saveSkill = (req: SkillSaveRequest) => app().SaveSkill(req) as Promise<SkillList>
export const deleteSkill = (name: string, scope: string) => app().DeleteSkill(name, scope) as Promise<SkillList>
export const importSkill = (scope: string) => app().ImportSkill(scope) as Promise<SkillList>
export const listAgents = () => app().ListAgents() as Promise<AgentList>
export const saveAgent = (req: AgentSaveRequest) => app().SaveAgent(req) as Promise<AgentList>
export const deleteAgent = (name: string, scope: string) => app().DeleteAgent(name, scope) as Promise<AgentList>
export const importAgent = (scope: string) => app().ImportAgent(scope) as Promise<AgentList>
export const saveSettings = (req: StartSessionRequest) =>
  app().SaveSettings(req) as Promise<SessionInfo>

export const passportStatus = () => app().PassportStatus() as Promise<PassportStatus>
export const passportLogin = () => app().PassportLogin() as Promise<PassportStatus>
export const passportCancelLogin = () => app().PassportCancelLogin()
export const passportLogout = () => app().PassportLogout()
export const passportModels = () => app().PassportModels() as Promise<PassportModel[] | null>
export const listCustomModels = () => app().ListCustomModels() as Promise<CustomModel[] | null>
export const saveCustomModel = (m: CustomModel) => app().SaveCustomModel(m) as Promise<CustomModel[] | null>
export const deleteCustomModel = (name: string) => app().DeleteCustomModel(name) as Promise<CustomModel[] | null>

export function onEvent<T>(name: string, cb: (data: T) => void): () => void {
  return window.runtime.EventsOn(name, (data) => cb(data as T))
}

export const Events = {
  AssistantDelta: 'assistant:delta',
  AssistantThinking: 'assistant:thinking',
  ToolEvent: 'tool:event',
  PermissionRequest: 'permission:request',
  TurnEnd: 'turn:end',
  TurnError: 'turn:error',
  Warning: 'warning',
  SessionRenamed: 'session:renamed',
  HarmAutoAllow: 'harm:autoallow',
  PassportChanged: 'passport:changed',
} as const
