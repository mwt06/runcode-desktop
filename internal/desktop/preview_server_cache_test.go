package desktop

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// 高保真预览把转换出来的 PDF 放在 .runcode/preview-cache 下，再让 iframe 通过预览
// 服务器去取。很多静态服务默认挡点目录——这里必须能取到，否则整条通路是断的。
func TestPreviewServerServesTheOfficeCache(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".runcode", previewCacheDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abc123.pdf"), []byte("%PDF-1.7 fake"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	ps := newPreviewServer()
	base, err := ps.start(ws)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ps.stop()

	resp, err := http.Get(base + ".runcode/" + previewCacheDir + "/abc123.pdf")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200——预览服务器挡住了缓存目录，高保真预览取不到 PDF", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "%PDF-1.7 fake" {
		t.Fatalf("body = %q, want the cached PDF bytes", body)
	}
}
