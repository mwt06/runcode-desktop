package desktop

import (
	"runtime"
	"strings"
	"testing"
)

func TestSecretHintIsActionable(t *testing.T) {
	reason, fix := secretHint()
	// 原因必须是一句人话，不能是空的或一个错误码——用户看到的就是这句。
	if strings.TrimSpace(reason) == "" {
		t.Fatal("secretHint returned no reason; the failure would stay silent")
	}
	if runtime.GOOS == "windows" {
		// Windows 走 DPAPI，不依赖守护进程或图形会话。真失败了也没有用户能照做的
		// 修法，所以这里**故意**不给建议——给一条假的比不给更糟。
		return
	}
	// 类 Unix 上必须给出可照抄的修法：这条链路有四层静默（见 secretstatus.go），
	// 少了这句，用户只能对着"怎么又要登录"猜。
	if strings.TrimSpace(fix) == "" {
		t.Errorf("secretHint gave a reason but no fix on %s: %q", runtime.GOOS, reason)
	}
}

func TestSecretStorageStatusIsCached(t *testing.T) {
	// 探测会跑外部命令（secret-tool / security），每次调用都探一遍会拖慢设置页。
	a, b := secretStorageStatus(), secretStorageStatus()
	if a != b {
		t.Errorf("status is not stable across calls: %+v vs %+v", a, b)
	}
}

func TestSecretStorageStatusWiring(t *testing.T) {
	app := New(&recordingSink{})
	got := app.SecretStorageStatus()
	if got.OK {
		// 本机有可用的凭据存储（开发机上通常如此）：那就不该带原因。
		if got.Reason != "" || got.Fix != "" {
			t.Errorf("ok=true but still reported a problem: %+v", got)
		}
		return
	}
	if got.Reason == "" {
		t.Error("ok=false must come with a reason — silence is the bug this exists to fix")
	}
}
