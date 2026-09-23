//go:build darwin

package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacAskpassDoesNotNeedOrForgeDisplay(t *testing.T) {
	t.Setenv("DISPLAY", "")
	a := &App{}
	a.askpassSrv.Store(&askpassServer{exe: "/Applications/Test.app/Contents/MacOS/Test", path: "/tmp/test.sock", token: "test"})
	if !a.askpassReady() {
		t.Fatal("macOS GUI does not provide DISPLAY")
	}
	env := a.askpassEnv("session")
	if env["SUDO_ASKPASS"] == "" || env[envAskpassSession] != "session" {
		t.Fatalf("missing askpass environment: %v", env)
	}
	if _, ok := env["DISPLAY"]; ok {
		t.Fatal("must not forge DISPLAY")
	}
	if !strings.Contains(a.platformPrivilegePrompt(), "/usr/bin/sudo -A -k") {
		t.Fatal("macOS needs explicit askpass flag")
	}
}

func TestMacCandidateIdentityCheckedBeforeSignature(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "中文 App.app")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0o700); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>test.bundle</string>
<key>CFBundleShortVersionString</key><string>1.2.3</string>
<key>CFBundleExecutable</key><string>app</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if value, err := macPlistValue(ctx, bundle, "CFBundleIdentifier"); err != nil || value != "test.bundle" {
		t.Fatalf("plutil: %q %v", value, err)
	}
	for _, tc := range []struct{ id, version, key string }{
		{"other.bundle", "1.2.3", "CFBundleIdentifier"},
		{"test.bundle", "2.0.0", "CFBundleShortVersionString"},
	} {
		err := verifyMacCandidate(ctx, bundle, tc.id, tc.version, "", "arm64")
		if err == nil || !strings.Contains(err.Error(), tc.key+" 不符") {
			t.Fatalf("wrong identity not rejected: %v", err)
		}
	}
}
