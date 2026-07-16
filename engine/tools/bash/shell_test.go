package bash

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

// Force bash for the real-command tests in this package so they run consistently
// across platforms — the Windows default is PowerShell, which lacks the bash-isms
// (printf, -lc, redirects) those tests rely on. Non-parallel tests (below) run to
// completion before any t.Parallel() test resumes, so this env stays stable for
// the exec tests.
func init() { os.Setenv("RUNCODE_SHELL", "bash") }

func TestShellInvocationBash(t *testing.T) {
	old := os.Getenv("RUNCODE_SHELL")
	defer os.Setenv("RUNCODE_SHELL", old)
	os.Setenv("RUNCODE_SHELL", "bash")

	name, args := shellInvocation("echo hi")
	if name != "bash" || len(args) != 2 || args[0] != "-lc" || args[1] != "echo hi" {
		t.Fatalf("bash invocation = %q %v", name, args)
	}
	if ShellName() != "bash" {
		t.Fatalf("ShellName = %q, want bash", ShellName())
	}
}

func TestShellInvocationCmd(t *testing.T) {
	old := os.Getenv("RUNCODE_SHELL")
	defer os.Setenv("RUNCODE_SHELL", old)
	os.Setenv("RUNCODE_SHELL", "cmd")

	name, args := shellInvocation("python primes.py")
	if name != "cmd.exe" || len(args) != 2 || args[0] != "/c" || args[1] != "python primes.py" {
		t.Fatalf("cmd invocation = %q %v", name, args)
	}
	if ShellName() != "cmd" {
		t.Fatalf("ShellName = %q, want cmd", ShellName())
	}
}

func TestCommandInvocationCmdQuotedUsesScript(t *testing.T) {
	old := os.Getenv("RUNCODE_SHELL")
	defer os.Setenv("RUNCODE_SHELL", old)
	os.Setenv("RUNCODE_SHELL", "cmd")

	// A command containing double quotes must be routed through a temp .cmd script
	// (cmd /c mangles embedded quotes), not run inline.
	name, args, cleanup, err := commandInvocation(`python -c "print(1)"`)
	if err != nil {
		t.Fatalf("commandInvocation: %v", err)
	}
	defer cleanup()
	if name != "cmd.exe" || len(args) != 3 || args[0] != "/c" || args[1] != "call" {
		t.Fatalf("invocation = %q %v, want cmd.exe /c call <script>", name, args)
	}
	if !strings.HasSuffix(args[2], ".cmd") {
		t.Fatalf("script path = %q, want a .cmd temp file", args[2])
	}
	if _, statErr := os.Stat(args[2]); statErr != nil {
		t.Fatalf("temp script missing: %v", statErr)
	}
}

func TestCommandInvocationCmdSimpleStaysInline(t *testing.T) {
	old := os.Getenv("RUNCODE_SHELL")
	defer os.Setenv("RUNCODE_SHELL", old)
	os.Setenv("RUNCODE_SHELL", "cmd")

	name, args, cleanup, err := commandInvocation("python primes.py")
	if err != nil {
		t.Fatalf("commandInvocation: %v", err)
	}
	defer cleanup()
	if name != "cmd.exe" || len(args) != 2 || args[0] != "/c" || args[1] != "python primes.py" {
		t.Fatalf("invocation = %q %v, want inline cmd.exe /c", name, args)
	}
}

func TestShellInvocationPowerShellEncodesCommand(t *testing.T) {
	old := os.Getenv("RUNCODE_SHELL")
	defer os.Setenv("RUNCODE_SHELL", old)
	os.Setenv("RUNCODE_SHELL", "powershell")

	name, args := shellInvocation("python primes.py")
	if name != "powershell.exe" || len(args) < 2 || args[len(args)-2] != "-EncodedCommand" {
		t.Fatalf("powershell invocation = %q %v", name, args)
	}
	raw, err := base64.StdEncoding.DecodeString(args[len(args)-1])
	if err != nil {
		t.Fatalf("decode encoded command: %v", err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[i*2]) | uint16(raw[i*2+1])<<8
	}
	if got := string(utf16.Decode(units)); got != "python primes.py" {
		t.Fatalf("decoded command = %q, want round-trip", got)
	}
	if ShellName() != "powershell" {
		t.Fatalf("ShellName = %q, want powershell", ShellName())
	}
}
