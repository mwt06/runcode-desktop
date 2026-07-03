//go:build windows

package delete

import (
	"os"
	"path/filepath"
	"testing"
)

// Validates the SHFileOperationW struct layout and call end-to-end: a real temp
// file is sent to the Recycle Bin and must no longer exist at its path. Windows
// only (the recycle bin is a Windows concept); the recycled temp file is
// harmless.
func TestMoveToTrashRecyclesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runcode-trash-test.txt")
	if err := os.WriteFile(path, []byte("recycle me"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := moveToTrash(path); err != nil {
		t.Fatalf("moveToTrash: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still at path after recycle: %v", err)
	}
}
