package desktop

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// OpenExternal opens the workspace file with the OS default application. It is
// bounded to the workspace (rejects escapes before launching).
func (a *App) OpenExternal(relPath string) error {
	full, err := a.resolveArtifact(relPath)
	if err != nil {
		return err
	}
	return openCommand(full).Start()
}

// RevealInFolder shows the workspace file in the OS file manager.
func (a *App) RevealInFolder(relPath string) error {
	full, err := a.resolveArtifact(relPath)
	if err != nil {
		return err
	}
	return revealCommand(full).Start()
}

// ResolveArtifactPath returns the absolute path of a workspace file, for the
// UI's "copy path" action.
func (a *App) ResolveArtifactPath(relPath string) (string, error) {
	return a.resolveArtifact(relPath)
}

func (a *App) resolveArtifact(relPath string) (string, error) {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	return resolveWithinWorkspace(ws, relPath)
}

// openCommand builds the command to open path with the OS default app WITHOUT a
// shell, so an attacker-chosen filename (the workspace is AI-written) cannot inject
// commands. On Windows, rundll32 is invoked directly (path passed as inert argv),
// never cmd.exe's parser. If rundll32+FileProtocolHandler ever proves flaky for a
// path shape in manual testing, switch to the ShellExecute API — do NOT reintroduce
// cmd /c start.
func openCommand(path string) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	case "darwin":
		return exec.Command("open", path)
	default:
		return exec.Command("xdg-open", path)
	}
}

// revealCommand builds the command to reveal path in the OS file manager, also
// shell-free (explorer/open/xdg-open invoked directly).
func revealCommand(path string) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", "/select,"+path)
	case "darwin":
		return exec.Command("open", "-R", path)
	default:
		return exec.Command("xdg-open", filepath.Dir(path))
	}
}
