//go:build darwin && cgo

package desktop

// libproc 取的是内核记录的可执行路径，不信 argv[0] 或进程的自报身份。
// 无 cgo 的 CLI/服务端仍可编译，只是不开放密码通道（askpass_other.go）。

/*
#cgo LDFLAGS: -lproc
#include <libproc.h>
#include <sys/proc_info.h>

struct askpass_process {
    struct proc_bsdinfo info;
    char path[PROC_PIDPATHINFO_MAXSIZE];
};
static int askpass_process_info(int pid, struct askpass_process *out) {
    if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &out->info, sizeof(out->info)) != sizeof(out->info)) return 0;
    return proc_pidpath(pid, out->path, sizeof(out->path)) > 0;
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func verifyAskpassPeer(conn *net.UnixConn, selfExe string) (string, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return "", err
	}
	var pid int
	var cred *unix.Xucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if credErr == nil {
			pid, credErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		}
	}); err != nil {
		return "", err
	}
	if credErr != nil {
		return "", credErr
	}
	if int(cred.Uid) != os.Getuid() || pid <= 0 || pid > math.MaxInt32 {
		return "", errors.New("askpass peer uid/pid mismatch")
	}
	var peer, parent C.struct_askpass_process
	if C.askpass_process_info(C.int(pid), &peer) == 0 || !sameExecutable(C.GoString(&peer.path[0]), selfExe) {
		return "", errors.New("askpass peer is not this application")
	}
	ppid := int(peer.info.pbi_ppid)
	if ppid <= 0 || ppid > math.MaxInt32 {
		return "", errors.New("invalid askpass parent pid")
	}
	if C.askpass_process_info(C.int(ppid), &parent) == 0 || parent.info.pbi_uid != 0 ||
		!sameExecutable(C.GoString(&parent.path[0]), "/usr/bin/sudo") {
		return "", errors.New("askpass parent is not system sudo with effective uid 0")
	}
	// ps 仅供显示命令，不参与身份判定。核验失败时不会走到这一步。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/ps", "-ww", "-p", strconv.Itoa(ppid), "-o", "command=").Output() //nolint:gosec // 固定 ps 查询，pid 来自已通过内核核验的父进程
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "", fmt.Errorf("cannot read sudo command (pid %d)", ppid)
	}
	return strings.TrimSpace(string(out)), nil
}
