// Minimal typings for the Wails v2 runtime globals the app uses, so the frontend
// builds without depending on Wails' generated bindings (which only appear after
// `wails dev`/`wails generate`). Bound Go methods of *desktop.App are exposed at
// window.go.desktop.App.* with their Go names; methods returning (T, error) become
// Promise<T> and error-only methods become Promise<void> (rejecting on error).
export {}

declare global {
  interface Window {
    go: {
      desktop: {
        App: {
          StartSession(req: unknown): Promise<unknown>
          SendMessage(text: string): Promise<void>
          SendMessageWithImages(text: string, paths: string[]): Promise<void>
          PickImageAttachment(): Promise<string>
          Interrupt(): Promise<void>
          ResolvePermission(id: string, decision: string): Promise<void>
          SetPermissionMode(mode: string): Promise<void>
          SetModel(model: string): Promise<void>
          SetPlanMode(on: boolean): Promise<unknown>
          SetReasoningScenario(scenario: string): Promise<unknown>
          SetThinkingEffort(effort: string): Promise<unknown>
          Compact(): Promise<unknown>
          Reset(): Promise<void>
          Status(): Promise<unknown>
          CloseSession(): Promise<void>
          ListSessions(): Promise<unknown>
          ResumeSession(id: string): Promise<unknown>
          NewSession(): Promise<unknown>
          PickWorkspaceFolder(): Promise<string>
          SwitchWorkspace(dir: string): Promise<unknown>
          LoadConfig(): Promise<unknown>
          SaveSettings(req: unknown): Promise<unknown>
          ListTools(): Promise<unknown>
          SetToolEnabled(name: string, scope: string, enabled: boolean): Promise<void>
          SetAgentEnabled(name: string, scope: string, enabled: boolean): Promise<void>
          SetSkillEnabled(name: string, scope: string, enabled: boolean): Promise<void>
          ListFiles(): Promise<unknown>
          ReadArtifact(relPath: string): Promise<string>
          OpenExternal(relPath: string): Promise<void>
          RevealInFolder(relPath: string): Promise<void>
          ResolveArtifactPath(relPath: string): Promise<string>
          ListSkills(): Promise<unknown>
          SaveSkill(req: unknown): Promise<unknown>
          DeleteSkill(name: string, scope: string): Promise<unknown>
          ImportSkill(scope: string): Promise<unknown>
          ListAgents(): Promise<unknown>
          SaveAgent(req: unknown): Promise<unknown>
          DeleteAgent(name: string, scope: string): Promise<unknown>
          ImportAgent(scope: string): Promise<unknown>
          PassportStatus(): Promise<unknown>
          PassportLogin(): Promise<unknown>
          PassportCancelLogin(): Promise<void>
          PassportLogout(): Promise<void>
          PassportTenants(): Promise<unknown>
          PassportModels(tenantId: string): Promise<unknown>
          ListCustomModels(): Promise<unknown>
          SaveCustomModel(m: unknown): Promise<unknown>
          DeleteCustomModel(name: string): Promise<unknown>
        }
      }
    }
    runtime: {
      EventsOn(name: string, callback: (data: unknown) => void): () => void
      EventsOff(name: string): void
      WindowMinimise(): void
      WindowToggleMaximise(): void
      WindowMaximise(): void
      WindowUnmaximise(): void
      Quit(): void
      BrowserOpenURL(url: string): void
      ClipboardSetText(text: string): Promise<boolean>
    }
  }
}
