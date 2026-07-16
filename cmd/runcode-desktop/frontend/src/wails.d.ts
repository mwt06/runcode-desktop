// Minimal typings for the Wails v2 runtime globals the app uses, so the frontend
// builds without depending on Wails' generated bindings (which only appear after
// `wails dev`/`wails generate`). Bound Go methods of *desktop.App are not declared
// here: they are reached exclusively through the generated, fully typed command
// layer in src/protocol/commands.ts, which resolves window.go itself.
export {}

declare global {
  interface Window {
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
