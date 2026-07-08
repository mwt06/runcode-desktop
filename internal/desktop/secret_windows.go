//go:build windows

package desktop

import (
	"encoding/base64"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows, credentials at rest are encrypted with DPAPI (CryptProtectData),
// which ties the ciphertext to the current user account, so desktop.json never
// holds a usable key in the clear even at file mode 0600.
var (
	crypt32                = windows.NewLazySystemDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
)

type dpapiBlob struct {
	cbData uint32
	pbData *byte
}

// protectSecret encrypts s with DPAPI and returns it base64-encoded. ok is false
// for an empty input or on any DPAPI failure (the caller then does not persist s).
func protectSecret(s string) (string, bool) {
	out, ok := dpapiTransform(procCryptProtectData, []byte(s))
	if !ok {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(out), true
}

// unprotectSecret reverses protectSecret for the same user/machine.
func unprotectSecret(protected string) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(protected)
	if err != nil {
		return "", false
	}
	out, ok := dpapiTransform(procCryptUnprotectData, raw)
	if !ok {
		return "", false
	}
	return string(out), true
}

func dpapiTransform(proc *windows.LazyProc, in []byte) ([]byte, bool) {
	if len(in) == 0 {
		return nil, false
	}
	inBlob := dpapiBlob{cbData: uint32(len(in)), pbData: &in[0]}
	var outBlob dpapiBlob
	r, _, _ := proc.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, // ppszDataDescr
		0, // pOptionalEntropy
		0, // pvReserved
		0, // pPromptStruct
		0, // dwFlags
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r == 0 || outBlob.pbData == nil {
		return nil, false
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(outBlob.pbData))))
	out := make([]byte, outBlob.cbData)
	copy(out, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	return out, true
}
