//go:build linux

package desktop

// Linux 上的 askpass：本机套接字服务 + 来者核验 + 助手模式。
//
// # 来者核验（为什么这一段是整个功能的安全支点）
//
// askpass 把密码**打印到标准输出**交给 sudo。如果模型不经 sudo、直接把 askpass 当普通
// 程序跑，弹框照样出现，用户输了密码——密码就进了工具输出，模型看得见。所以服务端对
// 每个连接都要验明正身，三条全过才弹框：
//
//  1. 对端与本应用是同一个用户（SO_PEERCRED 的 uid）。
//  2. 对端进程是本应用的二进制（/proc/<pid>/exe）。
//  3. 对端的父进程是**真的 sudo**：名字是 sudo，且**有效 UID 为 0**。
//
// 第 3 条只看名字是不够的，这是在麒麟 V10 SP1 上实测过的：一个名叫 sudo 的普通脚本去
// 调 askpass，父进程的 Name 同样显示 sudo；但它的有效 UID 是 1000。真 sudo 是 setuid
// root，有效 UID 必然是 0，普通进程造不出来。
//
// 通过之后，密码框里显示的命令取自那个 sudo 进程自己的 /proc/<pid>/cmdline——是它
// **实际**要执行的东西，而不是审批时那段文本。

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// askpassWire 是助手发给服务端的那一行请求（不含任何秘密）。
type askpassWire struct {
	Token   string `json:"token"`
	Session string `json:"session"`
	Prompt  string `json:"prompt"`
}

// 服务端的回答是纯文本两行："OK\n<密码>\n" 或 "ERR <原因>\n"。
//
// 不用 JSON：密码经过 JSON 编码会在内存里多出几份无法清零的字符串副本。纯文本只需要
// 一份 []byte，写出去就清掉。
const (
	askpassOK  = "OK"
	askpassErr = "ERR"
)

// startAskpassServer 在本用户私有的目录里开一个 Unix 套接字。
func startAskpassServer(b *askpassBroker) (*askpassServer, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir, err := askpassDir()
	if err != nil {
		return nil, err
	}
	sock := filepath.Join(dir, fmt.Sprintf("runcode-askpass-%d.sock", os.Getpid()))
	_ = os.Remove(sock) // 上次崩溃留下的同名残骸
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return nil, err
	}
	// 目录已是 0700，套接字本身再收一道：同机其他用户连 connect 都做不到。
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	s := &askpassServer{path: sock, token: randomID() + randomID(), exe: exe}
	s.stop = func() {
		_ = ln.Close()
		_ = os.Remove(sock)
	}
	go serveAskpass(ln, s, b)
	return s, nil
}

// askpassDir 选套接字的落脚处：优先 XDG_RUNTIME_DIR（每用户一份、0700、在内存里、
// 注销即清），没有就退到用户缓存目录下自建一个 0700 的。
func askpassDir() (string, error) {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		if st, err := os.Stat(d); err == nil && st.IsDir() && st.Mode().Perm()&0o077 == 0 {
			return d, nil
		}
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "runcode", "askpass")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	// MkdirAll 不会收紧一个已存在的目录，这里显式再收一次。
	return d, os.Chmod(d, 0o700) //nolint:gosec // 这是目录：没有 x 位就进不去，0700 已是本用户独占
}

func serveAskpass(ln *net.UnixListener, s *askpassServer, b *askpassBroker) {
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go handleAskpass(conn, s, b)
	}
}

func handleAskpass(conn *net.UnixConn, s *askpassServer, b *askpassBroker) {
	defer func() { _ = conn.Close() }()
	refuse := func(reason string) { _, _ = fmt.Fprintf(conn, "%s %s\n", askpassErr, reason) }

	sudoCmd, err := verifyAskpassPeer(conn, s.exe)
	if err != nil {
		// 原因只进调试日志，不回给对端：对一个冒名者，"哪一条没过"本身就是情报。
		debugLog("askpass: refused peer: %v", err)
		refuse("来源核验未通过，拒绝提供密码")
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	var req askpassWire
	if err := json.Unmarshal(line, &req); err != nil {
		refuse("请求格式不对")
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.token)) != 1 {
		debugLog("askpass: bad token")
		refuse("来源核验未通过，拒绝提供密码")
		return
	}

	// 对端一走（sudo 被杀、命令被用户停掉）就撤回弹框——否则会剩一个不知道为谁而开的
	// 密码框，而用户一旦往里输，密码就交给了一个已经不存在的请求。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		var one [1]byte
		_, _ = conn.Read(one[:])
		cancel()
	}()

	pw, err := b.ask(ctx, req.Session, sudoCmd, req.Prompt)
	if err != nil {
		refuse(err.Error())
		return
	}
	defer zero(pw)
	if strings.ContainsAny(string(pw), "\r\n") {
		refuse("密码里不能有换行")
		return
	}
	_, _ = conn.Write([]byte(askpassOK + "\n"))
	_, _ = conn.Write(pw)
	_, _ = conn.Write([]byte("\n"))
}

// verifyAskpassPeer 验明来者，并返回它的父进程（sudo）实际要执行的命令行。
func verifyAskpassPeer(conn *net.UnixConn, selfExe string) (string, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return "", err
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return "", err
	}
	if credErr != nil {
		return "", credErr
	}
	if int(cred.Uid) != os.Getuid() {
		return "", fmt.Errorf("peer uid %d != %d", cred.Uid, os.Getuid())
	}

	pid := int(cred.Pid)
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return "", fmt.Errorf("read peer exe: %w", err)
	}
	if !sameExecutable(exe, selfExe) {
		return "", fmt.Errorf("peer exe %q is not this app", exe)
	}

	self, ok := readProcStatus(pid)
	if !ok {
		return "", errors.New("read peer status")
	}
	parent, ok := readProcStatus(self.ppid)
	if !ok {
		return "", errors.New("read parent status")
	}
	if parent.name != "sudo" || parent.euid != 0 {
		return "", fmt.Errorf("parent is %q with euid %d, not sudo", parent.name, parent.euid)
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", self.ppid))
	if err != nil {
		return "", fmt.Errorf("read sudo cmdline: %w", err)
	}
	return cmdlineString(cmdline), nil
}

func readProcStatus(pid int) (procStatus, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return procStatus{}, false
	}
	return parseProcStatus(string(data))
}

// sameExecutable 比较两个可执行文件是不是同一个。路径相同最常见；用 SameFile 兜住
// 经符号链接安装的情形（/usr/local/bin/zhikai → /opt/…）。
func sameExecutable(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}

// ---- 助手模式 ------------------------------------------------------------

// IsAskpass 报告这次启动是不是被 sudo 当成密码程序拉起来的。
//
// 判据是那几个只注进工具子进程的环境变量：本应用正常启动时它们不存在。
func IsAskpass() bool {
	return os.Getenv(envAskpassSocket) != "" && os.Getenv(envAskpassToken) != ""
}

// askpassClientTimeout 比服务端等用户的上限略长，免得助手先放弃、留下一个还开着的弹框。
const askpassClientTimeout = askpassTimeout + 30*time.Second

// RunAskpass 是助手模式的全部：连回本应用、等用户输密码、把它打印给 sudo。
//
// sudo 以 argv[1] 传来提示语。成功时向标准输出写一行密码并以 0 退出；失败时把原因写到
// 标准错误并以 1 退出——sudo 会报"没有提供密码"，模型在工具输出里看到的是那句原因。
func RunAskpass(args []string) int {
	prompt := ""
	if len(args) > 1 {
		prompt = args[1]
	}
	conn, err := net.DialTimeout("unix", os.Getenv(envAskpassSocket), 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法连接应用来输入密码：", err)
		return 1
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(askpassClientTimeout))

	req, _ := json.Marshal(askpassWire{
		Token:   os.Getenv(envAskpassToken),
		Session: os.Getenv(envAskpassSession),
		Prompt:  prompt,
	})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "无法连接应用来输入密码：", err)
		return 1
	}
	r := bufio.NewReader(conn)
	status, err := r.ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "等待密码时连接中断")
		return 1
	}
	status = strings.TrimSpace(status)
	if status != askpassOK {
		fmt.Fprintln(os.Stderr, strings.TrimSpace(strings.TrimPrefix(status, askpassErr)))
		return 1
	}
	pw, err := r.ReadBytes('\n')
	defer zero(pw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取密码失败")
		return 1
	}
	_, _ = os.Stdout.Write(pw)
	return 0
}
