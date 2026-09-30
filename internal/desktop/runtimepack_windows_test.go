package desktop

import (
	"context"
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/executil"
	"golang.org/x/sys/windows"
)

func TestProbeVersionNoConsole(t *testing.T) {
	// Also run this test in a -H windowsgui test binary to reproduce a desktop
	// parent launching console programs, rather than inheriting a test terminal.
	if os.Getenv("RUNCODE_EXPECT_GUI_PARENT") == "1" {
		hwnd, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
		if hwnd != 0 {
			t.Fatalf("test parent still has a console: %d", hwnd)
		}
	}
	exe := filepath.Join(t.TempDir(), "runtime-probe.exe")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", exe, "./testdata/runtimeprobe")
	executil.HideConsoleWindow(build)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	image, err := pe.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	if h, ok := image.OptionalHeader.(*pe.OptionalHeader64); !ok || h.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		t.Fatal("fixture must be a console executable")
	}
	for _, tc := range []struct{ mode, want string }{
		{"stdout", "3.12.11"}, {"stderr", "2.7.18"},
		{"nonzero-version", "2.50.0"}, {"fail", ""}, {"wait", ""},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("RUNCODE_RUNTIME_PROBE_FIXTURE", tc.mode)
			start := time.Now()
			if got := probeVersion(t.Context(), exe); got != tc.want {
				t.Fatalf("version=%q, want %q", got, tc.want)
			}
			if tc.mode == "wait" && time.Since(start) > versionProbeTimeout+5*time.Second {
				t.Fatal("probe did not honor timeout")
			}
		})
	}
	ctx, stop := context.WithCancel(t.Context())
	stop()
	if got := probeVersion(ctx, exe); got != "" {
		t.Fatalf("canceled probe: %q", got)
	}
}
