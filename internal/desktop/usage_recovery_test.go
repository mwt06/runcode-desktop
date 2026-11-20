package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/localusage"
	"github.com/wt68/runcode/internal/protocol"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

func TestUsageStorageFailureIsNotTransient(t *testing.T) {
	app := New(&recordingSink{})
	app.usageInitErr = localusage.ErrSchemaCorrupt
	_, err := app.GetUsageOverview(protocol.UsageQuery{})
	var wire *hostproto.Error
	if !errors.As(err, &wire) || wire.Code != protocol.ErrCodeUsageStorage || !strings.Contains(wire.Message, "重建") {
		t.Fatalf("storage failure = %#v", err)
	}
}

func TestResetLocalUsageStoreArchivesBrokenDatabase(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	path, err := localUsagePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.usageInitErr = localusage.ErrSchemaCorrupt

	if err := app.ResetLocalUsageStore(); err != nil {
		t.Fatal(err)
	}
	app.usageMu.RLock()
	store := app.usageStore
	initErr := app.usageInitErr
	app.usageMu.RUnlock()
	if store == nil || initErr != nil {
		t.Fatalf("reopened store=%v err=%v", store, initErr)
	}
	t.Cleanup(func() { _ = store.Close() })
	backups, err := filepath.Glob(path + ".backup-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
}
