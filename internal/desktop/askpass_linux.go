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
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

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
