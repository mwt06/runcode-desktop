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
	return openInOS(full)
}

// RevealInFolder shows the workspace file in the OS file manager.
func (a *App) RevealInFolder(relPath string) error {
	full, err := a.resolveArtifact(relPath)
	if err != nil {
		return err
	}
	return revealInOS(full)
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

func openInOS(path string) error {
	switch runtime.GOOS {
	case "windows":
		// start needs an (empty) title arg first so a quoted path isn't taken as one.
		return exec.Command("cmd", "/c", "start", "", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

func revealInOS(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", "/select,"+path).Start()
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}
