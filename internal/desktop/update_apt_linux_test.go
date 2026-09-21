//go:build linux

package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestRelaunchScriptWaitsForOldProcess 新版本必须等旧进程退干净才起来：单实例锁攥在
// 旧进程手里，抢先起来的新版本会把焦点交给旧窗口然后自己退出。
func TestRelaunchScriptWaitsForOldProcess(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "started")
	next := filepath.Join(dir, "next.sh")
	if err := os.WriteFile(next, []byte("#!/bin/sh\ntouch '"+mark+"'\n"), 0o700); err != nil { //nolint:gosec // 测试用的临时脚本
		t.Fatal(err)
	}
	old := exec.Command("sleep", "1")
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = old.Wait() }()

	relaunch := exec.Command("/bin/sh", "-c", relaunchScript, "relaunch", strconv.Itoa(old.Process.Pid), next)
	if err := relaunch.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("旧进程还在，新版本就被拉起来了")
	}
	if err := relaunch.Wait(); err != nil {
		t.Fatalf("relaunch: %v", err)
	}
	if _, err := os.Stat(mark); err != nil {
		t.Fatal("旧进程退出之后，新版本没有被拉起来")
	}
}
