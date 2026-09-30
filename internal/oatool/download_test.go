package oatool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

const downloadArgs = `{"sourceType":"document","sourceId":"123","attachmentId":"abc"}`

func TestDownloadGateBeforeSideEffects(t *testing.T) {
	ws := t.TempDir()
	tl := DownloadTool{cfg: Config{Gate: func(context.Context, string, string) error { return errors.New("local model required") }, Token: func() (string, error) { t.Fatal("token acquired before gate"); return "", nil }}}
	res, err := tl.Run(context.Background(), json.RawMessage(downloadArgs), &tool.Context{WorkingDirectory: ws}, nil)
	if err != nil || !res.IsError {
		t.Fatalf("gate result: %+v %v", res, err)
	}
	files, err := os.ReadDir(ws)
	if err != nil || len(files) != 0 {
		t.Fatalf("gate wrote files: %v %v", files, err)
	}
}

func TestDownloadBinaryAndRefresh(t *testing.T) {
	payload := []byte{0, 255, 1, 128, 42}
	refreshed := false
	srv, calls := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/attachments/download" || r.Method != "POST" {
			t.Errorf("request: %s %s", r.Method, r.URL.Path)
		}
		if !refreshed {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer new-token" {
			t.Error("old token")
		}
		w.Header().Set("X-OA-Attachment-Contract", "2")
		w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''%E9%99%84%E4%BB%B6.pdf`)
		_, _ = w.Write(payload)
	})
	tools := New(Config{Endpoint: srv.URL, Token: func() (string, error) {
		if refreshed {
			return "new-token", nil
		}
		return "old-token", nil
	}, OnUnauthorized: func() { refreshed = true }})
	tl := toolNamed(t, tools, DownloadName)
	ws := t.TempDir()
	paths := map[string]bool{}
	for range 2 {
		res, err := tl.Run(context.Background(), json.RawMessage(downloadArgs), &tool.Context{WorkingDirectory: ws}, nil)
		if err != nil || res.IsError {
			t.Fatalf("download: %+v %v", res, err)
		}
		var data struct {
			Path, Name, SHA256 string
			Size               int
		}
		if err = json.Unmarshal([]byte(resultText(res)), &data); err != nil {
			t.Fatal(err)
		}
		if paths[data.Path] {
			t.Fatal("same name reused")
		}
		paths[data.Path] = true
		got, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(data.Path)))
		sum := sha256.Sum256(payload)
		if err != nil || string(got) != string(payload) || data.Size != len(payload) || data.SHA256 != hex.EncodeToString(sum[:]) || data.Name != "附件.pdf" {
			t.Fatalf("result: %+v bytes=%v err=%v", data, got, err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestDownloadRejectsInputAndNonFiles(t *testing.T) {
	for _, status := range []int{200, 302, 401, 413} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "/login")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "login")
			})
			tools := New(Config{Endpoint: srv.URL, Token: staticToken("")})
			ws := t.TempDir()
			res, err := toolNamed(t, tools, DownloadName).Run(context.Background(), json.RawMessage(downloadArgs), &tool.Context{WorkingDirectory: ws}, nil)
			if err == nil && !res.IsError {
				t.Fatal("accepted non-file")
			}
			files, _ := os.ReadDir(ws)
			if len(files) != 0 {
				t.Fatal("wrote non-file")
			}
		})
	}
	tl := DownloadTool{cfg: Config{Token: func() (string, error) { t.Fatal("invalid input reached network"); return "", nil }}}
	for _, raw := range []string{`{}`, `{"sourceType":"document","sourceId":"1","attachmentId":"../x"}`, `{"sourceType":"document","sourceId":"1","attachmentId":"a","userid":"other"}`} {
		_, err := tl.Run(context.Background(), json.RawMessage(raw), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
		if err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestSaveAttachmentFailureCleanup(t *testing.T) {
	for _, tc := range []struct {
		name            string
		expected, limit int64
		canceled        bool
	}{{"oversize", -1, 2, false}, {"truncated", 9, 10, false}, {"canceled", 3, 10, true}} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			res, err := saveAttachment(ctx, ws, "x.pdf", strings.NewReader("abc"), tc.expected, tc.limit)
			if err == nil && !res.IsError {
				t.Fatal("accepted incomplete download")
			}
			entries, err := os.ReadDir(filepath.Join(ws, "OA附件"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("leaked partial file: %v %v", entries, err)
			}
		})
	}
}

func TestSaveAttachmentRejectsEscapingSymlink(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws, "OA附件")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	res, err := saveAttachment(context.Background(), ws, "x", strings.NewReader("x"), 1, 5)
	if err == nil && !res.IsError {
		t.Fatal("followed escaping symlink")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside workspace")
	}
}

func TestSafeAttachmentName(t *testing.T) {
	for input, want := range map[string]string{"../../x.pdf": "x.pdf", `C:\a\附件.docx`: "附件.docx", "CON.txt": "_CON.txt", "..": "附件.bin", "bad:name.txt": "bad_name.txt"} {
		if got := safeAttachmentName(input); got != want {
			t.Errorf("%q -> %q, want %q", input, got, want)
		}
	}
	if got := safeAttachmentName(strings.Repeat("附", 200) + ".pdf"); len(got) > 184 || !strings.HasSuffix(got, ".pdf") {
		t.Fatalf("long filename %q", got)
	}
}

func TestDownloadTruncatedHTTPBody(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-OA-Attachment-Contract", "2")
		w.Header().Set("Content-Disposition", `attachment; filename="report.pdf"`)
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "short")
	})
	tl := toolNamed(t, New(Config{Endpoint: srv.URL, Token: staticToken("")}), DownloadName)
	ws := t.TempDir()
	res, err := tl.Run(context.Background(), json.RawMessage(downloadArgs), &tool.Context{WorkingDirectory: ws}, nil)
	if err == nil && !res.IsError {
		t.Fatal("accepted truncated HTTP file")
	}
	entries, err := os.ReadDir(filepath.Join(ws, "OA附件"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial files: %v %v", entries, err)
	}
}

func TestConcurrentAttachmentPaths(t *testing.T) {
	ws := t.TempDir()
	paths := make(chan string, 8)
	for range 8 {
		go func() {
			res, err := saveAttachment(context.Background(), ws, "same.pdf", strings.NewReader("file"), 4, 10)
			if err != nil || res.IsError {
				paths <- ""
				return
			}
			var data struct{ Path string }
			if json.Unmarshal([]byte(resultText(res)), &data) != nil {
				paths <- ""
				return
			}
			paths <- data.Path
		}()
	}
	seen := map[string]bool{}
	for range 8 {
		path := <-paths
		if path == "" || seen[path] {
			t.Fatalf("duplicate or failed concurrent download %q", path)
		}
		seen[path] = true
	}
}

func TestDownloadRequiresPatchedServiceBeforeSaving(t *testing.T) {
	for _, marker := range []string{"", "1"} {
		t.Run("version="+marker, func(t *testing.T) {
			srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Disposition", `attachment; filename="wrong.doc"`)
				if marker != "" {
					w.Header().Set("X-OA-Attachment-Contract", marker)
				}
				_, _ = io.WriteString(w, "unrelated file")
			})
			tl := toolNamed(t, New(Config{Endpoint: srv.URL, Token: staticToken("")}), DownloadName)
			ws := t.TempDir()
			res, err := tl.Run(context.Background(), json.RawMessage(downloadArgs), &tool.Context{WorkingDirectory: ws}, nil)
			if err != nil || !res.IsError || !strings.Contains(resultText(res), "升级") {
				t.Fatalf("result=%+v err=%v", res, err)
			}
			files, _ := os.ReadDir(ws)
			if len(files) != 0 {
				t.Fatal("saved old service file")
			}
		})
	}
}

func TestDownloadReportsMismatchWithoutSaving(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409)
		_, _ = io.WriteString(w, `{"message":"OA 附件文件名与来源不一致，已拒绝下载。"}`)
	})
	ws := t.TempDir()
	res, err := toolNamed(t, New(Config{Endpoint: srv.URL, Token: staticToken("")}), DownloadName).Run(context.Background(), json.RawMessage(downloadArgs), &tool.Context{WorkingDirectory: ws}, nil)
	if err != nil || !res.IsError || !strings.Contains(resultText(res), "不一致") {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	files, _ := os.ReadDir(ws)
	if len(files) != 0 {
		t.Fatal("saved mismatched file")
	}
}

func TestDownloadKeepsLiteralPercentInRFC5987Filename(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-OA-Attachment-Contract", "2")
		w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''%E6%8A%A5%E5%91%8A%2520%2B.docx`)
		_, _ = io.WriteString(w, "file")
	})
	ws := t.TempDir()
	res, err := toolNamed(t, New(Config{Endpoint: srv.URL, Token: staticToken("")}), DownloadName).Run(context.Background(), json.RawMessage(downloadArgs), &tool.Context{WorkingDirectory: ws}, nil)
	if err != nil || res.IsError {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	var data struct{ Path, Name string }
	if err = json.Unmarshal([]byte(resultText(res)), &data); err != nil {
		t.Fatal(err)
	}
	if data.Name != "报告%20+.docx" {
		t.Fatalf("double-decoded filename %q", data.Name)
	}
	if _, err = os.Stat(filepath.Join(ws, filepath.FromSlash(data.Path))); err != nil {
		t.Fatal(err)
	}
}
