//go:build windows

package desktop

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestWindowsSudoModeFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name          string
		n             uint64
		err           error
		missing, want uint64
	}{
		{"no setting", 0, registry.ErrNotExist, 0, 0},
		{"no policy", 0, registry.ErrNotExist, 3, 3},
		{"unreadable policy", 3, errors.New("access denied"), 3, 0},
		{"disabled", 0, nil, 3, 0},
		{"new window", 1, nil, 0, 1},
		{"disable input", 2, nil, 0, 2},
		{"inline", 3, nil, 0, 3},
		{"upstream clamps modes", 9, nil, 0, 3},
	} {
		if got := sudoModeValue(tc.n, tc.err, tc.missing); got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}

func TestWindowsPrivilegePromptMatchesCapability(t *testing.T) {
	for mode, want := range map[uint64]string{0: "unavailable or disabled", 1: "--new-window", 2: "--disable-input", 3: "--disable-input"} {
		got := windowsPrivilegePrompt(`C:\Windows\System32\sudo.exe`, mode)
		if !strings.Contains(got, want) {
			t.Errorf("mode %d: %s", mode, got)
		}
		if mode == 1 && strings.Contains(got, "--disable-input") {
			t.Fatal("new-window-only machines cannot use inline modes")
		}
		if mode > 0 && !strings.Contains(got, "Windows UAC") {
			t.Fatal("must explain system authorization")
		}
	}
	if askpassAvailable() {
		t.Fatal("Windows must not collect administrator passwords")
	}
}
