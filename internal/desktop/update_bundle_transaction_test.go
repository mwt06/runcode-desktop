package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 运行真正的事务脚本，只把 macOS 的签名/解压工具换成测试替身；mv/rm/cp/trap
// 仍是真实文件操作。Linux CI 和装有 Git Bash 的 Windows 也能验证失败回滚。
func TestMacInstallTransaction(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("requires a POSIX shell (Git Bash on Windows)")
	}
	for _, fault := range []string{"", "extract", "signature", "identity", "hash", "move", "restore", "signal"} {
		t.Run("fault="+fault, func(t *testing.T) {
			base := t.TempDir()
			target := filepath.Join(base, "中文 App's $literal.app")
			work := filepath.Join(base, "work")
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(work, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "old"), []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(base, "package with spaces.zip")
			if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
				t.Fatal(err)
			}
			tools := map[string]string{
				"/usr/bin/shasum -a 256": `if [ "$TEST_FAULT" = hash ]; then echo bad; else echo 'abc  package.zip'; fi`,
				"/usr/bin/ditto": `
if [ "$TEST_FAULT" = extract ]; then exit 1; fi
/bin/mkdir -p "$4/Package.app/Contents/MacOS"
printf '#!/bin/sh
# new
' > "$4/Package.app/Contents/MacOS/app"
/bin/chmod 700 "$4/Package.app/Contents/MacOS/app"
`,
				"/usr/bin/plutil": `case "$2" in
CFBundleIdentifier) if [ "$TEST_FAULT" = identity ]; then echo wrong; else echo test.app; fi;;
CFBundleShortVersionString) echo 1.2.3;;
CFBundleExecutable) echo app;;
CFBundlePackageType) echo APPL;;
esac`,
				"/usr/bin/lipo": `exit 0`,
				"/usr/bin/codesign": `
if [ "$TEST_FAULT" = signature ]; then exit 1; fi
if [ "$1" = -d ]; then echo TeamIdentifier=TEAM; fi
`,
				"/usr/sbin/spctl": `exit 0`,
				"/bin/mv": `
case "$1" in
*/unpacked/Package.app) if [ "$TEST_FAULT" = move ] || [ "$TEST_FAULT" = restore ]; then exit 1; fi;;
*/previous.app) if [ "$TEST_FAULT" = restore ]; then exit 1; fi;;
esac
/bin/mv "$@" || exit 1
case "$2" in */previous.app) if [ "$TEST_FAULT" = signal ]; then kill -TERM "$PPID"; fi;; esac
`,
			}
			script := macInstallScript
			for bin, body := range tools {
				file := filepath.Join(base, "mock-"+strings.ReplaceAll(filepath.Base(bin), " ", "-"))
				if err := os.WriteFile(file, []byte(body+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				quoted := "'" + strings.ReplaceAll(filepath.ToSlash(file), "'", "'\"'\"'") + "'"
				script = strings.ReplaceAll(script, bin, "/bin/sh "+quoted)
			}
			cmd := exec.Command(sh, "-c", script, "test-install", filepath.ToSlash(archive), filepath.ToSlash(target), "Package.app", "abc", "test.app", "1.2.3", "TEAM", "arm64", filepath.ToSlash(work))
			cmd.Env = append(os.Environ(), "TEST_FAULT="+fault)
			out, err := cmd.CombinedOutput()
			if (err != nil) != (fault != "") {
				t.Fatalf("fault=%s err=%v output=%s", fault, err, out)
			}
			switch fault {
			case "":
				if data, err := os.ReadFile(filepath.Join(target, "Contents", "MacOS", "app")); err != nil || !strings.Contains(string(data), "# new") {
					t.Fatalf("new app not installed: %q %v", data, err)
				}
			case "restore":
				if data, err := os.ReadFile(filepath.Join(work, "previous.app", "old")); err != nil || string(data) != "old" {
					t.Fatalf("lost recovery backup: %q %v", data, err)
				}
			default:
				if data, err := os.ReadFile(filepath.Join(target, "old")); err != nil || string(data) != "old" {
					t.Fatalf("old app not preserved: %q %v\n%s", data, err, out)
				}
			}
			if fault != "restore" {
				if _, err := os.Stat(work); !os.IsNotExist(err) {
					t.Fatalf("staging not cleaned: %v\n%s", err, out)
				}
			}
		})
	}
}
