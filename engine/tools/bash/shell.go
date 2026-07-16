package bash

import (
	"encoding/base64"
	"io"
	"os"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/wt68/runcode/engine/internal/secenv"
)

// shellKind reports which shell the Bash tool runs commands in.
//
// The default is cmd on Windows and bash elsewhere. On Windows this matters: the
// file tools (Read/Write/Edit) operate on native paths like `D:\proj`, but if
// commands run through WSL bash they see `/mnt/d/proj` and native tools like
// `python` may be absent — so a command the model builds from the Windows
// workspace path fails with "command not found" or a bogus path. Running through
// cmd keeps the command's filesystem view consistent with the rest of the tools
// (and, unlike PowerShell, cmd does not serialize redirected stderr as CLIXML).
//
// RUNCODE_SHELL=bash|cmd|powershell overrides the default — e.g. a Windows user
// who prefers Git Bash can set RUNCODE_SHELL=bash.
func shellKind() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("RUNCODE_SHELL"))) {
	case "bash":
		return "bash"
	case "cmd":
		return "cmd"
	case "powershell", "pwsh":
		return "powershell"
	}
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "bash"
}

// ShellName returns a human label for the active shell, for the prompt's
// environment section so the model writes commands for the right shell.
func ShellName() string {
	switch shellKind() {
	case "cmd":
		return "cmd"
	case "powershell":
		return "powershell"
	}
	if s := strings.TrimSpace(os.Getenv("SHELL")); s != "" {
		return s
	}
	return "bash"
}

// shellInvocation returns the program and args to run a single command string.
// The PowerShell path uses -EncodedCommand (UTF-16LE base64) so an arbitrary
// command needs no shell quoting.
func shellInvocation(command string) (string, []string) {
	switch shellKind() {
	case "cmd":
		return "cmd.exe", []string{"/c", command}
	case "powershell":
		return "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(command)}
	default:
		return "bash", []string{"-lc", command}
	}
}

// commandInvocation returns the program, args, and a cleanup func to run a
// command. Single-line commands run inline via shellInvocation. A multi-line
// command on cmd is materialized to a temp .cmd script, because `cmd.exe /c`
// cannot take an argument with embedded newlines; bash (-lc) and PowerShell
// (-EncodedCommand) accept multi-line input inline and pass straight through.
// cleanup removes any temp script and is always non-nil and safe to call.
func commandInvocation(command string) (name string, args []string, cleanup func(), err error) {
	noop := func() {}
	// cmd.exe /c mangles an argument that contains newlines or embedded double
	// quotes (its quote-stripping rules corrupt e.g. `python -c "..."`). Running
	// such a command from a temp .cmd script sidesteps the /c re-parsing entirely.
	// bash (-lc) and PowerShell (-EncodedCommand) pass the command through intact,
	// so they never need a script.
	if shellKind() == "cmd" && strings.ContainsAny(command, "\r\n\"") {
		path, err := writeCmdScript(command)
		if err != nil {
			return "", nil, noop, err
		}
		return "cmd.exe", []string{"/c", "call", path}, func() { os.Remove(path) }, nil
	}
	name, args = shellInvocation(command)
	return name, args, noop, nil
}

// cmdScriptPreamble disables command echo and switches the console to UTF-8 so a
// batch script's own output (and any tool it runs) is not mangled by a legacy
// code page like cp936 (GBK) on Chinese Windows.
const cmdScriptPreamble = "@echo off\r\nchcp 65001 >nul\r\n"

// writeCmdScript writes a multi-line command to a temp .cmd file with CRLF line
// endings (batch files are line-oriented and unreliable with bare LF) and returns
// its path. The caller removes it after the process exits.
func writeCmdScript(command string) (string, error) {
	f, err := os.CreateTemp("", "runcode-*.cmd")
	if err != nil {
		return "", err
	}
	defer f.Close()
	body := strings.ReplaceAll(strings.ReplaceAll(command, "\r\n", "\n"), "\n", "\r\n")
	if _, err := io.WriteString(f, cmdScriptPreamble+body+"\r\n"); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// childEnv is the environment for spawned commands. It forces UTF-8 output from
// Python so non-ASCII prints (e.g. Chinese) are not garbled by a legacy console
// code page; PYTHONUTF8 covers 3.7+ and PYTHONIOENCODING is the older fallback.
func childEnv() []string {
	// Scrub credential-looking variables (API keys, tokens) so a permitted or
	// injected command can't read the agent's secrets and exfiltrate them.
	return append(secenv.Sanitize(os.Environ()), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
}

func encodePowerShell(command string) string {
	units := utf16.Encode([]rune(command))
	buf := make([]byte, len(units)*2)
	for i, u := range units {
		buf[i*2] = byte(u)
		buf[i*2+1] = byte(u >> 8)
	}
	return base64.StdEncoding.EncodeToString(buf)
}
