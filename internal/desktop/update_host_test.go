package desktop

import (
	"runtime"
	"testing"
)

func TestUpdateHostCapabilities(t *testing.T) {
	isolateConfigDir(t)
	a := New(&recordingSink{})
	info := a.UpdateStatus()
	if info.Current != AppVersion() || info.CanInstall != canLaunchInstaller() || info.AutoRestart != willAutoRestart() || info.InstallHint != installHint() || info.InstallHint == "" {
		t.Fatalf("host capabilities not wired: %+v", info)
	}
	if got := updatePlatform(); got != wantUpdateOS+"/"+runtime.GOARCH {
		t.Fatalf("platform = %q", got)
	}
}
