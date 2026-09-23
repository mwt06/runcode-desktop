//go:build linux || (darwin && cgo)

package desktop

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

// 不需要真实密码或 root：直接调用助手、伪造同名 sudo 都必须在弹框前被拒。
func TestAskpassTransportRejectsImpersonation(t *testing.T) {
	gate := newPrivilegeGate()
	gate.arm("transport-test")
	requests := make(chan bool, 4)
	var broker *askpassBroker
	broker = newAskpassBroker(func(_ string, payload any) {
		if req, ok := payload.(protocol.AskpassRequest); ok {
			requests <- true
			broker.answer(req.ID, "sentinel-not-a-real-password")
		}
	}, gate)
	srv, err := startAskpassServer(broker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.close)
	fake := filepath.Join(t.TempDir(), "sudo")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n\"$SUDO_ASKPASS\" prompt\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{srv.exe, fake} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, bin, "prompt")
		cmd.Env = envWith(map[string]string{
			"SUDO_ASKPASS": srv.exe, envAskpassSocket: srv.path,
			envAskpassToken: srv.token, envAskpassSession: "transport-test",
		})
		out, err := cmd.CombinedOutput()
		cancel()
		if err == nil {
			t.Fatalf("%q was allowed: %s", bin, out)
		}
		if strings.Contains(string(out), "sentinel-not-a-real-password") {
			t.Fatal("helper leaked a password")
		}
		select {
		case <-requests:
			t.Fatal("untrusted caller reached password dialog")
		default:
		}
	}
}
