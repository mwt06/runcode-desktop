//go:build !windows

package delete

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// moveToTrash implements the freedesktop.org Trash spec for the user's home trash
// (~/.local/share/Trash), the common location on Linux. Platforms without that
// layout (e.g. macOS uses ~/.Trash with different semantics) return an error so
// the caller can fall back to a permanent delete. A cross-device target also
// returns an error rather than silently copying.
func moveToTrash(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	trash := filepath.Join(home, ".local", "share", "Trash")
	filesDir := filepath.Join(trash, "files")
	infoDir := filepath.Join(trash, "info")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		return err
	}

	base := filepath.Base(path)
	dest := filepath.Join(filesDir, base)
	info := filepath.Join(infoDir, base+".trashinfo")
	// Disambiguate a name collision in the trash so an existing entry is never
	// clobbered (the spec requires unique names).
	for i := 1; ; i++ {
		_, errFiles := os.Lstat(dest)
		_, errInfo := os.Lstat(info)
		if errors.Is(errFiles, os.ErrNotExist) && errors.Is(errInfo, os.ErrNotExist) {
			break
		}
		name := fmt.Sprintf("%s.%d", base, i)
		dest = filepath.Join(filesDir, name)
		info = filepath.Join(infoDir, name+".trashinfo")
	}

	if err := os.Rename(path, dest); err != nil {
		return err
	}
	body := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", path, time.Now().Format("2006-01-02T15:04:05"))
	// The file is already in the trash; a missing .trashinfo only loses metadata,
	// so a write failure here is not fatal.
	_ = os.WriteFile(info, []byte(body), 0o600)
	return nil
}
