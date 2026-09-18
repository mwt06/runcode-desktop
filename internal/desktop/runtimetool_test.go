package desktop

// install_runtime 的外壳侧。安装器本身与设置页共用（runtimes_install_test.go 已走过真
// 文件、真 HTTP），这里盯住只有模型那条路才有的几件事：清单没取过先取、已装好不重下、
// 设置页正在装就等、取消必须让模型知道、回给模型的那段话、审批弹窗那句话。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"

	"github.com/wt68/runcode/internal/plantool"
	"github.com/wt68/runcode/internal/protocol"
	"github.com/wt68/runcode/internal/runtimetool"
)

func TestEnsureRuntimeFetchesManifestThenInstalls(t *testing.T) {
	f := newRuntimeFixture(t)
	// 故意不先 CheckRuntimes：模型完全可能在启动后那趟自动检查之前就调了工具。
	out, err := f.app.ensureRuntime(context.Background(), protocol.RuntimePackPython)
	if err != nil {
		t.Fatalf("ensureRuntime: %v", err)
	}
	dest := filepath.Join(f.dir, protocol.RuntimePackPython, "3.12.11")
	// 回话要给出：版本、完整位置（PATH 之外的兜底）、"这个对话里就能用"、预装了什么。
	for _, want := range []string{"Python 3.12.11", dest, "in this conversation", "python-docx", "`" + promptCommand(protocol.RuntimePackPython) + "`"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if p := pythonPack(f.app.RuntimeStatus()); p.Stage != protocol.RuntimeStageReady {
		t.Fatalf("stage after install = %q, want ready", p.Stage)
	}
	// 已经开着的对话下一条命令就要找得到它：进程 PATH 当场更新。
	if !strings.Contains(os.Getenv("PATH"), dest) {
		t.Errorf("pack dir not on the process PATH after the tool install: %q", os.Getenv("PATH"))
	}

	// 再调一次（模型读着开对话时那份"没有 Python"的提示词，很可能会）：装好了就只报告，
	// 不再下一遍几十 MB。
	before := f.downloads.Load()
	again, err := f.app.ensureRuntime(context.Background(), protocol.RuntimePackPython)
	if err != nil {
		t.Fatalf("second ensureRuntime: %v", err)
	}
	if f.downloads.Load() != before {
		t.Error("an installed runtime was downloaded again")
	}
	if !strings.Contains(again, "3.12.11") {
		t.Errorf("second report does not describe the runtime:\n%s", again)
	}
}

func TestEnsureRuntimeCancelledTellsTheModel(t *testing.T) {
	f := newRuntimeFixture(t)
	if _, err := f.app.CheckRuntimes(); err != nil {
		t.Fatalf("CheckRuntimes: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 用户按了停止
	_, err := f.app.ensureRuntime(ctx, protocol.RuntimePackPython)
	// 必须是错误：设置页把取消当成功，模型那条要是也当成功，它会接着去跑 python3。
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v, want a cancellation the model can read", err)
	}
	// 而界面上仍然是干净的"未安装"，没有一句错误。
	p := pythonPack(f.app.RuntimeStatus())
	if p.Stage != protocol.RuntimeStageAbsent || p.Error != "" {
		t.Errorf("after a cancelled install: stage=%q error=%q, want absent and no error", p.Stage, p.Error)
	}
}

func TestEnsureRuntimeWaitsForRunningInstall(t *testing.T) {
	f := newRuntimeFixture(t)
	m := f.app.rt
	dir := filepath.Join(f.dir, protocol.RuntimePackPython, "3.12.11")
	// 设置页那边正在装。
	m.mu.Lock()
	st := m.packs[protocol.RuntimePackPython]
	st.cancel, st.stage = func() {}, protocol.RuntimeStageDownloading
	m.mu.Unlock()

	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := f.app.ensureRuntime(context.Background(), protocol.RuntimePackPython)
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("returned while another install was still running: %+v", r)
	case <-time.After(3 * runtimeBusyPoll):
	}

	// 那一趟装完了：等着的这一次直接报告结果，不自己再装一遍。
	m.update(protocol.RuntimePackPython, func(s *packState) {
		s.cancel, s.stage, s.version, s.dir = nil, protocol.RuntimeStageReady, "3.12.11", dir
	})
	select {
	case r := <-done:
		if r.err != nil || !strings.Contains(r.out, "3.12.11") {
			t.Fatalf("after the other install finished: out=%q err=%v", r.out, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ensureRuntime kept waiting after the other install finished")
	}
	if n := f.downloads.Load(); n != 0 {
		t.Errorf("downloaded %d times while only waiting on another install", n)
	}
}

func TestEnsureRuntimeNoPackagePublished(t *testing.T) {
	f := newRuntimeFixture(t) // 清单里只有 Python
	_, err := f.app.ensureRuntime(context.Background(), protocol.RuntimePackNode)
	if err == nil || !strings.Contains(err.Error(), "no Node.js package is published") {
		t.Fatalf("err = %v, want 'no package published'", err)
	}
	// 失败时也要把"别换法子装"说出来——工具失败后模型最常见的反应就是自己动手。
	if !strings.Contains(err.Error(), "do not install it any other way") {
		t.Errorf("error does not steer the model away from self-installing: %v", err)
	}
}

func TestEnsureRuntimeUnpublishedPlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git 在 Windows 上有包")
	}
	f := newRuntimeFixture(t)
	_, err := f.app.ensureRuntime(context.Background(), protocol.RuntimePackGit)
	// 本平台根本不发 Git：指到设置页是死胡同，要说"用系统包管理器、先问用户"。
	if err == nil || !strings.Contains(err.Error(), "does not ship Git") || !strings.Contains(err.Error(), "system package manager") {
		t.Fatalf("err = %v, want the system-package-manager route", err)
	}
}

func TestRuntimeResolverDescribesTheInstall(t *testing.T) {
	m := newTestManager()
	m.packs[protocol.RuntimePackPython].avail = protocol.RuntimeManifestPack{Version: "3.12.11", Size: 45<<20 + 1}
	r := runtimeResolver{inner: permissions.WithToolClasses(nil, hostToolClasses), rt: m}

	action, err := r.Resolve(context.Background(), permissions.ResolveRequest{
		ToolName: runtimetool.Name,
		Input:    json.RawMessage(`{"runtime":"python"}`),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// 展示层不能改变分类：仍是每次都要人批准的外部操作。
	if action.Operation != permissions.OperationExternal {
		t.Errorf("operation = %q, want external (approval every time)", action.Operation)
	}
	summary, _ := action.Metadata[permissions.MetadataCommandSummary].(string)
	for _, want := range []string{"Python 3.12.11", "46 MB", "不需要管理员权限"} {
		if !strings.Contains(summary, want) {
			t.Errorf("approval summary missing %q: %q", want, summary)
		}
	}
	// 授权键看的是参数本身（python ≠ node），这条元数据必须原样留着。
	if action.Metadata[permissions.MetadataHostToolArgs] == nil {
		t.Error("the host tool args (the grant key) were dropped")
	}

	// 别的工具一个字都不动。
	other, err := r.Resolve(context.Background(), permissions.ResolveRequest{ToolName: plantool.Name, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("Resolve plan_write: %v", err)
	}
	if _, ok := other.Metadata[permissions.MetadataCommandSummary]; ok {
		t.Error("runtimeResolver annotated a tool that is not install_runtime")
	}
}

func TestInstallSummaryForInstalledRuntime(t *testing.T) {
	m := newTestManager()
	st := m.packs[protocol.RuntimePackPython]
	st.stage, st.version, st.dir = protocol.RuntimeStageReady, "3.12.11", t.TempDir()
	// 已装好：用户要批准的其实什么都不下载，弹窗得这么说，别吓人。
	if s := m.installSummary(protocol.RuntimePackPython); !strings.Contains(s, "已经装好") {
		t.Errorf("summary for an installed runtime = %q", s)
	}
	if s := m.installSummary("ruby"); s != "" {
		t.Errorf("summary for an unknown runtime = %q, want empty", s)
	}
}
