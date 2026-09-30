package appupdate

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

func TestCancelAndBusyOperations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	entered := make(chan struct{})
	u := New(Options{
		CacheDir: func() (string, error) { return dir, nil },
		FetchToFile: func(ctx context.Context, _, _ string, _ *os.File, limit int64, _ func(int64, int64)) (string, int64, error) {
			if limit != updateMaxBytes {
				t.Errorf("missing download size limit: %d", limit)
			}
			close(entered)
			<-ctx.Done()
			return "", 0, ctx.Err()
		},
	})
	u.available(releaseWire{Version: "1.0.0", URL: "https://example.invalid/setup.exe", SHA256: strings.Repeat("a", 64)})
	done := make(chan error, 1)
	go func() { _, err := u.DownloadUpdate(); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	if _, err := u.CheckUpdate(); err == nil {
		t.Error("check interleaved with download")
	}
	if _, err := u.DownloadUpdate(); err == nil {
		t.Error("second download was allowed")
	}
	if err := u.InstallUpdate(); err == nil {
		t.Error("install interleaved with download")
	}
	u.CancelUpdateDownload()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not finish")
	}
	if info := u.UpdateStatus(); info.Stage != protocol.UpdateAvailable || info.Error != "" {
		t.Fatalf("cancel became failure: %+v", info)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial package left after cancel: %v, %v", entries, err)
	}
}

func TestUpdateInstancesAreIndependent(t *testing.T) {
	t.Parallel()
	a := New(Options{Current: "1.0.0"})
	b := New(Options{Current: "2.0.0"})
	if err := a.begin("install", nil); err != nil {
		t.Fatal(err)
	}
	defer a.finish()
	a.fail(errors.New("only a"))
	if err := b.begin("check", nil); err != nil {
		t.Fatal(err)
	}
	defer b.finish()
	info := b.UpdateStatus()
	if info.Current != "2.0.0" || info.Error != "" || info.Stage != protocol.UpdateIdle {
		t.Fatalf("instance state leaked: %+v", info)
	}
}

func TestUpdateEventsCanReadState(t *testing.T) {
	t.Parallel()
	var u *Service
	called := make(chan struct{}, 1)
	u = New(Options{Emit: func(info protocol.UpdateInfo) {
		if u.UpdateStatus().Stage != info.Stage {
			t.Error("event and snapshot disagree")
		}
		called <- struct{}{}
	}})
	go u.checking()
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("event callback held service lock")
	}
}

func TestAutoCheckOnlyRecordsSuccess(t *testing.T) {
	t.Parallel()
	f := newUpdateFixture(t, "0.1.0")
	f.status = 500
	f.app.AutoCheck()
	if f.app.autoDone {
		t.Fatal("failed automatic check consumed retry")
	}
	f.status = 200
	f.manifest.Version = "0.2.0"
	f.app.AutoCheck()
	if !f.app.autoDone {
		t.Fatal("successful automatic check not recorded")
	}
	requests := len(f.requests)
	f.app.AutoCheck()
	if len(f.requests) != requests {
		t.Fatal("successful automatic check repeated")
	}
}
