package bash

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Windows CREATE_NO_WINDOW process-creation flag: the child
// shell runs without allocating/flashing a console window. Output is still
// captured via the redirected stdout/stderr pipes.
const createNoWindow = 0x08000000

// hideConsoleWindow prevents the shell child process from popping a console
// window on Windows.
func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
