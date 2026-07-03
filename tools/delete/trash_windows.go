//go:build windows

package delete

import (
	"fmt"
	"syscall"
	"unsafe"
)

// SHFileOperationW constants (shellapi.h).
const (
	foDelete          = 0x0003 // FO_DELETE
	fofSilent         = 0x0004 // FOF_SILENT
	fofNoConfirmation = 0x0010 // FOF_NOCONFIRMATION
	fofAllowUndo      = 0x0040 // FOF_ALLOWUNDO (send to recycle bin)
	fofNoErrorUI      = 0x0400 // FOF_NOERRORUI
)

// shFileOpStruct mirrors SHFILEOPSTRUCTW. The field order and types reproduce the
// Win32 struct's layout on amd64 (verified offsets: pFrom@16, fFlags@32,
// fAnyOperationsAborted@36), so the unmanaged call reads it correctly.
type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

var procSHFileOperationW = syscall.NewLazyDLL("shell32.dll").NewProc("SHFileOperationW")

// moveToTrash sends an absolute path to the Windows Recycle Bin via
// SHFileOperationW with FOF_ALLOWUNDO. pFrom must be a double-NUL-terminated list
// of NUL-terminated paths.
func moveToTrash(path string) error {
	from, err := syscall.UTF16FromString(path)
	if err != nil {
		return err
	}
	from = append(from, 0) // second NUL: terminate the path list

	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &from[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI,
	}
	ret, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if ret != 0 {
		return fmt.Errorf("SHFileOperation failed (code 0x%x)", ret)
	}
	if op.fAnyOperationsAborted != 0 {
		return fmt.Errorf("recycle operation was aborted")
	}
	return nil
}
