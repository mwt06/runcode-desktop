//go:build darwin

package desktop

import (
	"context"
	"errors"
	"fmt"
	"github.com/wt68/runcode/internal/appupdate"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// 只更新装进 Applications 的那一份。开发目录、挂载盘和 Gatekeeper 的临时
// AppTranslocation 都不能作为覆盖目标；它们仍可下载并打开安装包所在文件夹。
func installedMacBundle() string {
	exe, err := selfExecutable()
	if err != nil {
		return ""
	}
	root := macBundleRoot(exe)
	if root == "" {
		return ""
	}
	bases := []string{"/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		bases = append(bases, filepath.Join(home, "Applications"))
	}
	for _, base := range bases {
		if strings.HasPrefix(root, base+string(filepath.Separator)) {
			return root
		}
	}
	return ""
}

func canLaunchInstaller() bool { return installedMacBundle() != "" }
func willAutoRestart() bool    { return canLaunchInstaller() }
func installHint() string {
	if !canLaunchInstaller() {
		return "把新版本拖进「应用程序」覆盖旧版即可；从「应用程序」启动后可自动更新"
	}
	return "校验应用签名后自动替换并重新打开；需要管理员权限时会请你输入系统密码"
}
func cleanStaleWatchers() {}

func (a *App) runInstaller(file, expect, hash string) error {
	target := installedMacBundle()
	if target == "" {
		return errors.New("请先把应用移入「应用程序」，再使用自动更新")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cache, err := updateCacheDir()
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(cache, "mac-update-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	// 校验与预解压针对同一份私有快照；提权后还会复制并重验这份 zip。
	archive := filepath.Join(stage, "package.zip")
	if err := copyFileStreaming(file, archive); err != nil {
		return err
	}
	if err := appupdate.VerifyFileSHA256(archive, hash); err != nil {
		return err
	}
	appname, err := validateMacUpdateZip(archive)
	if err != nil {
		return err
	}
	unpacked := filepath.Join(stage, "unpacked")
	if _, err := macUpdateCommand(ctx, "/usr/bin/ditto", "-x", "-k", archive, unpacked); err != nil {
		return err
	}
	candidate := filepath.Join(unpacked, appname)
	id, err := macPlistValue(ctx, target, "CFBundleIdentifier")
	if err != nil {
		return err
	}
	sig, signErr := macUpdateCommand(ctx, "/usr/bin/codesign", "-d", "--verbose=4", target)
	if signErr != nil && !strings.Contains(string(sig), "code object is not signed at all") {
		return fmt.Errorf("无法确认当前应用的签名身份，未修改当前应用: %w", signErr)
	}
	team := codeSignTeam(string(sig))
	arch := "arm64"
	if runtime.GOARCH == "amd64" {
		arch = "x86_64"
	}
	// 先在普通权限下验证，坏包不值得打断用户去输密码。
	if err := verifyMacCandidate(ctx, candidate, id, expect, team, arch); err != nil {
		return err
	}
	if err := a.replaceMacBundle(ctx, archive, target, appname, hash, id, expect, team, arch); err != nil {
		return err
	}
	if err := relaunchMacAfterExit(target); err != nil {
		return fmt.Errorf("新版本已安装，但无法安排自动重启，请退出后手动重新打开: %w", err)
	}
	return nil
}

func macPlistValue(ctx context.Context, bundle, key string) (string, error) {
	out, err := macUpdateCommand(ctx, "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("应用的 %s 为空", key)
	}
	return value, nil
}

func verifyMacCandidate(ctx context.Context, bundle, id, version, team, arch string) error {
	for key, want := range map[string]string{"CFBundleIdentifier": id, "CFBundleShortVersionString": version, "CFBundlePackageType": "APPL"} {
		got, err := macPlistValue(ctx, bundle, key)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("更新包的 %s 不符（%s，预期 %s），未修改当前应用", key, got, want)
		}
	}
	entry, err := macPlistValue(ctx, bundle, "CFBundleExecutable")
	if err != nil {
		return err
	}
	if entry == "." || entry == ".." || strings.ContainsAny(entry, "/\\") {
		return errors.New("更新包的应用入口不合法")
	}
	executable := filepath.Join(bundle, "Contents", "MacOS", entry)
	resolved, err := filepath.EvalSymlinks(executable)
	root, rootErr := filepath.EvalSymlinks(bundle)
	if err != nil || rootErr != nil || !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return errors.New("更新包的应用入口缺失或指向应用包之外")
	}
	st, err := os.Stat(resolved)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
		return errors.New("更新包的应用入口不是可执行文件")
	}
	if _, err := macUpdateCommand(ctx, "/usr/bin/lipo", "-verify_arch", arch, executable); err != nil {
		return err
	}
	if _, err := macUpdateCommand(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", bundle); err != nil {
		return err
	}
	if _, err := macUpdateCommand(ctx, "/usr/sbin/spctl", "--assess", "--type", "execute", bundle); err != nil {
		return fmt.Errorf("macOS 未认可更新包的签名/公证，未修改当前应用；请联系发布方或改为手动安装: %w", err)
	}
	out, err := macUpdateCommand(ctx, "/usr/bin/codesign", "-d", "--verbose=4", bundle)
	if err != nil {
		return err
	}
	if team != "" && codeSignTeam(string(out)) != team {
		return errors.New("更新包的签名团队与当前应用不符，未修改当前应用")
	}
	return nil
}

func (a *App) replaceMacBundle(ctx context.Context, archive, target, appname, hash, id, version, team, arch string) error {
	// /Applications 往往允许 admin 组创建文件，但其中已有的 bundle 归 root。
	// 只检查父目录会导致能换名却删不掉旧版；同时检查旧 bundle 的可写性。
	var work string
	err := unix.Access(target, unix.W_OK)
	if err == nil {
		work, err = os.MkdirTemp(filepath.Dir(target), ".runcode-update-")
	}
	elevated := errors.Is(err, os.ErrPermission)
	if err != nil && !elevated {
		return err
	}
	args := []string{"-c", macInstallScript, "install-update", archive, target, appname, hash, id, version, team, arch, work}
	cmd := exec.CommandContext(ctx, "/bin/sh", args...) //nolint:gosec // 固定脚本，已校验路径与身份通过位置参数传递
	if elevated {
		const key = "app:update"
		env := a.askpassEnv(key)
		if env == nil {
			return errors.New("更新需要管理员权限，但密码通道不可用；请改为手动安装")
		}
		a.privGate.armFor(key, "安装已校验的新版本 "+version+"，装好后会自动重新打开。", askpassTimeout+time.Minute)
		defer a.privGate.disarm(key)
		cmd = exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-A", "-k", "--", "/bin/sh"}, args...)...) //nolint:gosec // 固定更新脚本，经应用密码框授权；不执行模型脚本
		cmd.Env = envWith(env)
	}
	// 给 shell 的回滚 trap 一次机会，不能在两次 rename 之间直接 SIGKILL。
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = time.Minute
	out, err := cmd.CombinedOutput()
	if cmd.Process == nil && work != "" {
		_ = os.RemoveAll(work)
	}
	// 启动后目录由脚本管理；不能 defer RemoveAll，否则会删掉回滚失败时保留的唯一备份。
	if err != nil {
		return fmt.Errorf("安装更新失败（旧版会尽可能恢复，可改为手动安装）: %w\n%s", err, lastLines(string(out), 8))
	}
	return nil
}

func macUpdateCommand(ctx context.Context, bin string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput() //nolint:gosec // 调用方仅传固定的 macOS 系统工具，数据为独立参数
	if err != nil {
		return out, fmt.Errorf("%s: %w\n%s", filepath.Base(bin), err, lastLines(string(out), 6))
	}
	return out, nil
}

func relaunchMacAfterExit(bundle string) error {
	cache, err := updateCacheDir()
	if err != nil {
		return err
	}
	cmd := exec.Command("/bin/sh", "-c", macRelaunchScript, "relaunch", strconv.Itoa(os.Getpid()), bundle, filepath.Join(cache, "relaunch.log")) //nolint:gosec // 固定脚本、位置参数，保持登录用户身份
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
