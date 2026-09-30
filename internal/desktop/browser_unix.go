//go:build !windows

package desktop

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

func openBrowserContext(ctx context.Context, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("/usr/bin/open", url)
	} else {
		cmd = exec.Command("xdg-open", url)
	}
	return startBrowserLauncher(ctx, cmd)
}

func startBrowserLauncher(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// Catch immediate failures (missing handler/display), but do not kill the
	// launcher on a timeout: some xdg-open handlers wait until the browser closes.
	// The reaper remains responsible for it after this bounded handoff wait.
	timer := time.NewTimer(750 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("system browser launcher: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
