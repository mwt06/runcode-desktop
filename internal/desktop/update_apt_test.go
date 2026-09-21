package desktop

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestAptInstallArgs 盯住三件事：走 askpass（-A，否则从终端启动时 sudo 去终端问密码，
// 应用的密码框永远等不到）；不交互（-y）；安装包路径原样放在最后（apt 靠开头的 / 认出
// 这是本地文件，拼进别的参数里就会被当成包名）。
func TestAptInstallArgs(t *testing.T) {
	const file = "/home/u/.cache/runcode/updates/zhikai-1.0.19-arm64-abcd1234.deb"
	args := aptInstallArgs(file)
	if args[0] != "-A" {
		t.Errorf("sudo 必须带 -A，实际 %v", args)
	}
	if !slices.Contains(args, "apt-get") || !slices.Contains(args, "install") || !slices.Contains(args, "-y") {
		t.Errorf("应当是 apt-get install -y，实际 %v", args)
	}
	if args[len(args)-1] != file {
		t.Errorf("安装包路径应当原样在最后，实际 %v", args)
	}
	if !slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "DPkg::Lock::Timeout=") }) {
		t.Errorf("应当等 dpkg 锁而不是撞上就失败，实际 %v", args)
	}
}

// TestAptInstallErrorIsActionable 认得出的几种失败翻成用户能照做的话；认不出的带上
// apt 的原话（最后几行），那是用户报给管理员时最有用的东西。
func TestAptInstallErrorIsActionable(t *testing.T) {
	cases := []struct{ out, want string }{
		{"sudo: no password was provided\nsudo: a password is required", "已取消授权"},
		{"sudo: 1 incorrect password attempt", "密码不正确"},
		{"jybzd is not in the sudoers file.  This incident will be reported.", "没有管理员权限"},
		{"E: Could not get lock /var/lib/dpkg/lock-frontend - open (11: Resource temporarily unavailable)", "别的软件"},
		{"Reading package lists...\n\nE: 软件包 zhikai 有未满足的依赖关系\nE: 依赖关系有问题\n", "依赖关系有问题"},
		// 麒麟来源检查拒绝（真机原话）：安全中心弹框 30 秒没人点，按禁止处理。
		{"dpkg: 处理归档 /home/u/zhikai.deb (--unpack)时出错：\n 软件包/home/u/zhikai.deb验证失败，拒绝安装！\nE: Sub-process /usr/bin/dpkg returned an error code (1)", "允许安装"},
	}
	for _, c := range cases {
		if got := aptInstallError(c.out, errors.New("exit status 100")).Error(); !strings.Contains(got, c.want) {
			t.Errorf("输出 %q → %q，应当包含 %q", c.out, got, c.want)
		}
	}
	if got := aptInstallError("", errors.New("signal: killed")).Error(); !strings.Contains(got, "signal: killed") {
		t.Errorf("apt 什么都没说时应当带上进程错误，实际 %q", got)
	}
}

// TestParseSignStatus 认得出 getsignstatus 的三档（真机输出是全角冒号）。
func TestParseSignStatus(t *testing.T) {
	for out, want := range map[string]string{
		"麒麟签名状态：警告\n":       "warning",
		"麒麟签名状态：关闭":         "off",
		"麒麟签名状态：开启":         "on",
		"sign status: 警告":   "warning",
		"":                  "",
		"command not found": "",
	} {
		if got := parseSignStatus(out); got != want {
			t.Errorf("parseSignStatus(%q) = %q，期望 %q", out, got, want)
		}
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("a\n\n b \nc\nd\n", 2); got != "c；d" {
		t.Errorf("lastLines = %q", got)
	}
	if got := lastLines("  \n", 3); got != "" {
		t.Errorf("全是空行时应当为空，实际 %q", got)
	}
}
