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
		return wireError(err)
	}
	return wireError(startAndReap(openCommand(full)))
}

// RevealInFolder shows the workspace file in the OS file manager.
func (a *App) RevealInFolder(relPath string) error {
	full, err := a.resolveArtifact(relPath)
	if err != nil {
		return wireError(err)
	}
	return wireError(startAndReap(revealCommand(full)))
}

// startAndReap starts cmd and reaps it in the background once it exits. The
// launchers here (explorer/open/xdg-open, browser handoff) are fire-and-forget,
// but a child that is never Wait()ed lingers as a zombie on POSIX until the app
// itself exits — and open/reveal are high-frequency actions over a long session.
func startAndReap(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// ResolveArtifactPath returns the absolute path of a workspace file, for the
// UI's "copy path" action.
func (a *App) ResolveArtifactPath(relPath string) (string, error) {
	full, err := a.resolveArtifact(relPath)
	return full, wireError(err)
}

func (a *App) resolveArtifact(relPath string) (string, error) {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	return resolveWithinWorkspace(ws, relPath)
}

// openCommand builds the command to open path with the OS default app WITHOUT a
// shell, so an attacker-chosen filename (the workspace is AI-written) cannot inject
// commands — each binary is invoked directly with the path as an inert argv element.
// Windows uses explorer.exe (opens a file with its default handler); rundll32 was
// tried but is unreliable for local files. If explorer ever proves flaky for some
// type, switch to the ShellExecute API (build-tagged) — do NOT reintroduce cmd /c start.
func openCommand(path string) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", path)
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
	// G204 在这里是误报,原因见上:二进制名是常量、不经 shell、path 已过
	// resolveWithinWorkspace,拼进 argv 的都是惰性元素而非可解释的命令串。
	case "windows":
		return exec.Command("explorer", "/select,"+path) //nolint:gosec // G204
	case "darwin":
		return exec.Command("open", "-R", path) //nolint:gosec // G204
	default:
		return exec.Command("xdg-open", filepath.Dir(path)) //nolint:gosec // G204
	}
}
