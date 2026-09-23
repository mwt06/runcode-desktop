package desktop

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type bundleZipEntry struct {
	name, data string
	mode       os.FileMode
}

func bundleZip(t *testing.T, entries []bundleZipEntry) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "update.zip")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Store}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		out, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestValidateMacUpdateZip(t *testing.T) {
	base := []bundleZipEntry{
		{"智开.app/Contents/Info.plist", "plist", 0},
		{"智开.app/Contents/MacOS/智开", "binary", 0o755},
	}
	for _, tc := range []struct {
		name  string
		extra []bundleZipEntry
		bad   bool
	}{
		{"plain", nil, false},
		{"resource forks", []bundleZipEntry{{"__MACOSX/智开.app/Contents/._Info.plist", "data", 0}}, false},
		{"framework links", []bundleZipEntry{
			{"智开.app/Contents/Frameworks/X.framework/Versions/A/X", "binary", 0o755},
			{"智开.app/Contents/Frameworks/X.framework/Versions/Current", "A", os.ModeSymlink | 0o777},
			{"智开.app/Contents/Frameworks/X.framework/X", "Versions/Current/X", os.ModeSymlink | 0o777},
		}, false},
		{"traversal", []bundleZipEntry{{"智开.app/../../outside", "x", 0}}, true},
		{"absolute", []bundleZipEntry{{"/tmp/outside", "x", 0}}, true},
		{"backslash", []bundleZipEntry{{`智开.app\Contents\evil`, "x", 0}}, true},
		{"colon", []bundleZipEntry{{"智开.app/Contents/a:b", "x", 0}}, true},
		{"control", []bundleZipEntry{{"智开.app/Contents/a\nb", "x", 0}}, true},
		{"two apps", []bundleZipEntry{{"Other.app/Contents/Info.plist", "x", 0}}, true},
		{"extra payload", []bundleZipEntry{{"outside", "x", 0}}, true},
		{"case collision", []bundleZipEntry{{"智开.app/Contents/INFO.plist", "x", 0}}, true},
		{"link escape", []bundleZipEntry{{"智开.app/Contents/link", "../../../outside", os.ModeSymlink | 0o777}}, true},
		{"absolute link", []bundleZipEntry{{"智开.app/Contents/link", "/etc", os.ModeSymlink | 0o777}}, true},
		{"write through link", []bundleZipEntry{
			{"智开.app/Contents/link", "Resources", os.ModeSymlink | 0o777},
			{"智开.app/Contents/link/file", "x", 0},
		}, true},
		{"write before link", []bundleZipEntry{
			{"智开.app/Contents/LINK/file", "x", 0},
			{"智开.app/Contents/link", "Resources", os.ModeSymlink | 0o777},
		}, true},
		{"resource fork link", []bundleZipEntry{{"__MACOSX/link", "x", os.ModeSymlink | 0o777}}, true},
		{"special file", []bundleZipEntry{{"智开.app/Contents/pipe", "", os.ModeNamedPipe | 0o600}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := append(append([]bundleZipEntry{}, base...), tc.extra...)
			root, err := validateMacUpdateZip(bundleZip(t, entries))
			if (err != nil) != tc.bad {
				t.Fatalf("root=%q err=%v, want bad=%v", root, err, tc.bad)
			}
			if !tc.bad && root != "智开.app" {
				t.Fatalf("root=%q", root)
			}
		})
	}
	for _, entries := range [][]bundleZipEntry{nil, {{"智开.app/Contents/MacOS/智开", "x", 0}}} {
		if _, err := validateMacUpdateZip(bundleZip(t, entries)); err == nil {
			t.Fatal("accepted missing Info.plist")
		}
	}
}

func TestMacUpdateZipLimits(t *testing.T) {
	file := filepath.Join(t.TempDir(), "large.zip")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	h := &zip.FileHeader{Name: "App.app/Contents/Info.plist", Method: zip.Store, UncompressedSize64: macUpdateMaxBytes + 1}
	if _, err := w.CreateRaw(h); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := validateMacUpdateZip(file); err == nil || !strings.Contains(err.Error(), "过大") {
		t.Fatalf("huge archive: %v", err)
	}
	if _, err := validateMacUpdateZip(bundleZip(t, []bundleZipEntry{
		{"App.app/Contents/Info.plist", "x", 0},
		{"App.app/Contents/link", strings.Repeat("x", 4097), os.ModeSymlink | 0o777},
	})); err == nil {
		t.Fatal("accepted huge symlink")
	}
}

func TestMacBundleRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "中文 空格.app")
	if got := macBundleRoot(filepath.Join(root, "Contents", "MacOS", "智开")); got != root {
		t.Fatalf("got %q", got)
	}
	for _, exe := range []string{"/usr/local/bin/app", "/tmp/App/Contents/MacOS/app", "/tmp/App.app/app"} {
		if got := macBundleRoot(exe); got != "" {
			t.Fatalf("%q => %q", exe, got)
		}
	}
}

func TestCodeSignTeam(t *testing.T) {
	for output, want := range map[string]string{
		"Executable=/a\nTeamIdentifier=ABC123\nRuntime Version=15": "ABC123",
		"TeamIdentifier=not set\n":                                 "",
		"not signed at all":                                        "",
	} {
		if got := codeSignTeam(output); got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestMacInstallerSafetyContract(t *testing.T) {
	if strings.Index(macInstallScript, "shasum -a 256") > strings.Index(macInstallScript, "ditto -x") {
		t.Fatal("extracts before checking snapshot hash")
	}
	commit := strings.LastIndex(macInstallScript, `/bin/mv "$target" "$work/previous.app"`)
	for _, check := range []string{"CFBundleIdentifier", "CFBundleShortVersionString", "lipo -verify_arch", "codesign --verify", "spctl --assess", "TeamIdentifier=$team"} {
		if index := strings.Index(macInstallScript, check); index < 0 || index > commit {
			t.Errorf("%s not checked before replacement", check)
		}
	}
	if !strings.Contains(macInstallScript, `! /bin/mv "$work/previous.app" "$target"`) {
		t.Fatal("rollback missing")
	}
	if strings.Contains(macInstallScript, "xattr") {
		t.Fatal("installer must not bypass quarantine")
	}
	if !strings.Contains(macRelaunchScript, `kill -0 "$1"`) || !strings.Contains(macRelaunchScript, `open -n "$2"`) {
		t.Fatal("relaunch must wait for old process and use normal-user open")
	}
}
