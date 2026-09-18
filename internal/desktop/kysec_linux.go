//go:build linux

package desktop

// 真正调用 getstatus 与 kysec_set 的那一半。背景与修法见 kysec.go。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// kysecBatch 是一次 kysec_set 带的文件数。实测它一次能收多个文件；分批只是为了不碰
// 命令行长度上限。分几批也只弹一次密码框：sudo 按父进程缓存凭据，这几次 sudo 的父进程
// 都是本应用。
const kysecBatch = 200

// kysecStatus 探一次执行控制开没开。getstatus 不用 root 就能跑（实测）。
var kysecStatus = sync.OnceValue(func() bool {
	if _, err := exec.LookPath("kysec_set"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "getstatus").Output()
	if err != nil {
		return false
	}
	return parseExecControl(string(out))
})

// kysecExecControlOn 是个变量只为可测。
var kysecExecControlOn = func() bool { return kysecStatus() }

// kysecTrust 以 root 身份把 files 标成 verified。env 是整份子进程环境（含 askpass 那几个
// 变量）。
//
// 用 `sudo -A` 而不是指望 sudo 自己发现没有终端：从终端里启动应用时（开发机上常见）
// 它是有终端的，那样 sudo 会去终端上问密码，而应用的密码框永远等不到请求。
func kysecTrust(ctx context.Context, files []elfFile, env []string) error {
	for start := 0; start < len(files); start += kysecBatch {
		end := min(start+kysecBatch, len(files))
		args := []string{"-A", "kysec_set", "-n", "exectl", "-v", "verified"}
		for _, f := range files[start:end] {
			args = append(args, f.path)
		}
		cmd := exec.CommandContext(ctx, "sudo", args...) //nolint:gosec // 固定的 kysec_set 调用，文件是本应用刚解压的运行时包
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return kysecError(strings.TrimSpace(string(out)), err)
		}
	}
	// 撤掉这几次 sudo 留下的凭据缓存。它按父进程（本应用）记，留着的话十五分钟内应用
	// 再发起的 sudo 就不问密码了——目前没有别的地方会这样做，但"没有"不该靠巧合维持。
	_ = exec.Command("sudo", "-k").Run() //nolint:gosec // 固定参数
	return nil
}

// kysecError 把 sudo 的报错翻译成一句人话。
func kysecError(out string, err error) error {
	switch {
	case strings.Contains(out, "no password was provided"), strings.Contains(out, errAskpassCanceled.Error()):
		return errors.New("已取消授权。不授权也能用，只是每次运行都会弹安全框；随时可以在这里重新授权")
	case strings.Contains(out, "incorrect password"), strings.Contains(out, "密码不正确"):
		return errors.New("密码不正确，请重试")
	case strings.Contains(out, "not in the sudoers"), strings.Contains(out, "不在 sudoers"):
		return errors.New("当前用户没有管理员权限（不在 sudoers 中），请联系管理员授权")
	}
	if out == "" {
		out = err.Error()
	}
	return fmt.Errorf("加入安全中心白名单失败：%s", out)
}

// envWith 在本进程环境上叠加 extra（同名覆盖）。
func envWith(extra map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, dup := extra[k]; dup {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
