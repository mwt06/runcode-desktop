package download

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedDownloadDoesNotSendCredentials(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("download sent credentials")
		}
		_, _ = w.Write([]byte("too many bytes"))
	}))
	defer srv.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "partial"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, _, err = ToFile(t.Context(), srv.URL, "安装包", f, 4, func(int64, int64) {})
	if err == nil || !strings.Contains(err.Error(), "超过") {
		t.Fatalf("limit not enforced: %v", err)
	}
	info, err := f.Stat()
	if err != nil || info.Size() > 5 {
		t.Fatalf("unbounded write: %v %v", info, err)
	}
}
