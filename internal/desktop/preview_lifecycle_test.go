package desktop

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestStartPreviewServesThenStops(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "x.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(&recordingSink{})
	// previewBaseURL 报的是**聚焦会话**那个工作区的地址,所以这里要有个会话条目。
	// 生产路径上它由 openSessionWithConnectionHeld 建,测试里直接摆一个。
	focusOn(a, "s1", ws)

	a.startPreview(ws)
	base := a.previewBaseURL()
	if base == "" {
		t.Fatal("previewBaseURL empty after startPreview")
	}
	resp, err := http.Get(base + "x.txt")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("serve after start = %v %v", resp, err)
	}
	resp.Body.Close()

	a.stopPreview(ws)
	if a.previewBaseURL() != "" {
		t.Fatal("previewBaseURL not cleared after stopPreview")
	}
}

// TestPreviewServerSharedPerWorkspace 盯住预览服务器按工作区共享、引用归零才停。
//
// 多会话下同一目录不该起两台服务器:两个端口、两份文件句柄,两边看到的 URL 还不
// 一样。更要紧的是关掉其中一个会话时,另一个的预览不能跟着断。
func TestPreviewServerSharedPerWorkspace(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "x.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(&recordingSink{})
	focusOn(a, "s1", ws)

	a.startPreview(ws)
	first := a.previewBaseURL()
	if first == "" {
		t.Fatal("第一次 startPreview 没起来")
	}
	a.startPreview(ws) // 第二个会话进同一个工作区
	if got := a.previewBaseURL(); got != first {
		t.Fatalf("同一工作区的第二个会话拿到了不同地址: %q vs %q", got, first)
	}
	a.mu.Lock()
	n, refs := len(a.previews), a.previews[ws].refs
	a.mu.Unlock()
	if n != 1 || refs != 2 {
		t.Fatalf("同一工作区起了 %d 台服务器、引用数 %d,应为 1 台 2 次引用", n, refs)
	}

	// 关掉其中一个会话:另一个还在用,服务器不能停。
	a.stopPreview(ws)
	resp, err := http.Get(first + "x.txt")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("还有会话在用时预览就断了: %v %v", resp, err)
	}
	resp.Body.Close()

	// 最后一个也走了才停。
	a.stopPreview(ws)
	a.mu.Lock()
	n = len(a.previews)
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("引用归零后仍留着 %d 台服务器", n)
	}
	if a.previewBaseURL() != "" {
		t.Fatal("服务器停了,previewBaseURL 仍然有值")
	}
}
