//go:build linux && askpass_e2e

package desktop

// 在真 Linux 上用真 sudo 把整条 askpass 链路走一遍。它要系统密码，所以默认不跑：
//
//	go test -c -tags askpass_e2e -o askpass.test ./internal/desktop/
//	ASKPASS_E2E_PW='…' ./askpass.test -test.run TestAskpassE2E -test.v
//
// 单测里的 /proc 内容是手写的样本；这里验的是**真的** sudo、真的 SO_PEERCRED、真的
// /proc——来者核验是整个功能的安全支点，不在真机上跑过就不算数。

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

// 测试二进制兼任 askpass 的那个钩子在 main_test.go 的 TestMain 里。

type e2eHarness struct {
	srv      *askpassServer
	gate     *privilegeGate
	requests chan protocol.AskpassRequest
}

func newE2E(t *testing.T) *e2eHarness {
	t.Helper()
	pw := os.Getenv("ASKPASS_E2E_PW")
	if pw == "" {
		t.Skip("需要 ASKPASS_E2E_PW")
	}
	h := &e2eHarness{gate: newPrivilegeGate(), requests: make(chan protocol.AskpassRequest, 8)}
	var b *askpassBroker
	b = newAskpassBroker(func(name string, payload any) {
		if r, ok := payload.(protocol.AskpassRequest); ok {
			h.requests <- r
			// 扮演用户：看到弹框就输密码。
			go b.answer(r.ID, pw)
		}
	}, h.gate)
	srv, err := startAskpassServer(b)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(srv.close)
	h.srv = srv
	return h
}

// env 是注给工具子进程的那几个变量——与 App.askpassEnv 同形。
func (h *e2eHarness) env(session string) []string {
	return append(os.Environ(),
		"SUDO_ASKPASS="+h.srv.exe,
		envAskpassSocket+"="+h.srv.path,
		envAskpassToken+"="+h.srv.token,
		envAskpassSession+"="+session,
		// 与生产一致：桌面会话里 DISPLAY 总在（麒麟 Wayland 经 XWayland 给 :0）。从 SSH
		// 跑这个测试时没有它，sudo 就不会自动改走 askpass。
		"DISPLAY=:0",
	)
}

// run 在没有控制终端的新会话里跑一条命令——与 Bash 工具的子进程处境一致。
func (h *e2eHarness) run(t *testing.T, session string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("setsid", append([]string{name}, args...)...)
	cmd.Env = h.env(session)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (h *e2eHarness) gotRequest() (protocol.AskpassRequest, bool) {
	select {
	case r := <-h.requests:
		return r, true
	case <-time.After(300 * time.Millisecond):
		return protocol.AskpassRequest{}, false
	}
}

func TestAskpassE2E_RealSudoGetsPassword(t *testing.T) {
	h := newE2E(t)
	h.gate.arm("s1")
	out, err := h.run(t, "s1", "sudo", "-k", "id", "-un")
	if err != nil || !strings.HasSuffix(out, "root") {
		t.Fatalf("sudo via askpass failed: %v\n%s", err, out)
	}
	r, ok := h.gotRequest()
	if !ok {
		t.Fatal("UI never saw the request")
	}
	// 弹框里显示的是 sudo **实际**要跑的命令，取自它自己的 /proc/<pid>/cmdline。
	if r.Command != "sudo -k id -un" {
		t.Errorf("dialog showed %q, want the real sudo command line", r.Command)
	}
	// 密码不能出现在任何工具输出里。
	if strings.Contains(out, os.Getenv("ASKPASS_E2E_PW")) {
		t.Error("password leaked into command output")
	}
}

func TestAskpassE2E_ModelCallingHelperDirectlyIsRefused(t *testing.T) {
	h := newE2E(t)
	h.gate.arm("s1")
	// 模型绕开 sudo，直接把 askpass 当普通程序跑——如果放行，密码就会被打印到它看得见
	// 的标准输出里。
	out, err := h.run(t, "s1", h.srv.exe, "请输入密码")
	if err == nil {
		t.Fatalf("direct helper call succeeded: %q", out)
	}
	if _, ok := h.gotRequest(); ok {
		t.Error("a direct helper call reached the UI")
	}
	if strings.Contains(out, os.Getenv("ASKPASS_E2E_PW")) {
		t.Fatal("PASSWORD LEAKED to a direct helper call")
	}
}

func TestAskpassE2E_FakeSudoScriptIsRefused(t *testing.T) {
	h := newE2E(t)
	h.gate.arm("s1")
	// 一个名叫 sudo 的脚本：/proc 里 Name 会显示 sudo，但有效 UID 不是 0。
	dir := t.TempDir()
	fake := filepath.Join(dir, "sudo")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec \"$SUDO_ASKPASS\" x\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := h.run(t, "s1", fake)
	if err == nil {
		t.Fatalf("fake sudo got a password: %q", out)
	}
	if _, ok := h.gotRequest(); ok {
		t.Error("fake sudo reached the UI")
	}
}

func TestAskpassE2E_UnarmedSessionIsRefused(t *testing.T) {
	h := newE2E(t)
	// 没批准过 sudo 的会话：真 sudo 发起的请求也不弹框。
	out, err := h.run(t, "s-unarmed", "sudo", "-k", "true")
	if err == nil {
		t.Fatalf("sudo succeeded without an approval: %q", out)
	}
	if _, ok := h.gotRequest(); ok {
		t.Error("unarmed session reached the UI")
	}
}

func TestAskpassE2E_AppTrustsRuntimeFiles(t *testing.T) {
	if !kysecExecControlOn() {
		t.Skip("本机没开 KYSEC 执行控制")
	}
	h := newE2E(t)
	// 一个复制出来的 ELF：内容与 /bin/true 一样，但不是 dpkg 装的，所以是 unknown。
	dir := t.TempDir()
	data, err := os.ReadFile("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin", "t")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	pending, files, err := needsTrust(dir)
	if err != nil || !pending || len(files) != 1 {
		t.Fatalf("needsTrust: pending=%v files=%d err=%v", pending, len(files), err)
	}

	// 与 App.trustRuntime 同样的走法：用 app: 开头的键上膛并附用途，做完即撤。
	key := "app:runtime:e2e"
	h.gate.armFor(key, "让麒麟安全中心放行 e2e", time.Minute)
	defer h.gate.disarm(key)
	env := envWith(map[string]string{
		"SUDO_ASKPASS":    h.srv.exe,
		envAskpassSocket:  h.srv.path,
		envAskpassToken:   h.srv.token,
		envAskpassSession: key,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := kysecTrust(ctx, files, env); err != nil {
		t.Fatalf("kysecTrust: %v", err)
	}
	r, ok := h.gotRequest()
	if !ok {
		t.Fatal("UI never saw the password request")
	}
	// 应用自己发起的提权要带用途；弹框里显示的命令仍是 sudo 实际要跑的那一行。
	if r.Purpose == "" || !strings.Contains(r.Command, "kysec_set -n exectl -v verified") {
		t.Errorf("request = %+v", r)
	}

	// 加白之后应当一个框都不弹、直接跑完（没加白时这里会等 30 秒然后退出码 126）。
	start := time.Now()
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("trusted binary still blocked: %v %s", err, out)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("trusted binary took %v — a security prompt probably popped up", time.Since(start))
	}
}
