//go:build !windows

package bash

import "os/exec"

// hideConsoleWindow is a no-op off Windows, where there is no console window to
// hide.
func hideConsoleWindow(_ *exec.Cmd) {}
