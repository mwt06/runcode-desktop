//go:build windows

package desktop

import (
	"encoding/base64"
	"math"
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

// dpapiTransform 调用 DPAPI 的加/解密。整段是 Win32 契约的直译:blob 用
// unsafe.Pointer 传地址、长度是 uint32——gosec 的 G103(unsafe)在这里无从避免,
// 逐点注明而非全局排除,以免真正可疑的 unsafe 用法将来被一起放过。
func dpapiTransform(proc *windows.LazyProc, in []byte) ([]byte, bool) {
	if len(in) == 0 {
		return nil, false
	}
	// DPAPI 的 cbData 是 uint32。持久化的 token blob 远达不到 4GiB,但截断会把
	// 一段合法密文变成另一段"看起来合法"的输入,故显式挡在调用之外。
	if uint64(len(in)) > math.MaxUint32 {
		return nil, false
	}
	inBlob := dpapiBlob{cbData: uint32(len(in)), pbData: &in[0]} //nolint:gosec // G115: 上面的上界检查已保证不会截断
	var outBlob dpapiBlob
	r, _, _ := proc.Call(
		uintptr(unsafe.Pointer(&inBlob)),  //nolint:gosec // G103: Win32 blob 按地址传递
		0,                                 // ppszDataDescr
		0,                                 // pOptionalEntropy
		0,                                 // pvReserved
		0,                                 // pPromptStruct
		0,                                 // dwFlags
		uintptr(unsafe.Pointer(&outBlob)), //nolint:gosec // G103: 同上
	)
	if r == 0 || outBlob.pbData == nil {
		return nil, false
	}
	// G103: 释放 DPAPI 分配的输出缓冲,句柄即其地址。
	defer func() { _, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(outBlob.pbData)))) }() //nolint:gosec
	out := make([]byte, outBlob.cbData)
	copy(out, unsafe.Slice(outBlob.pbData, outBlob.cbData)) //nolint:gosec // G103: 按 cbData 长度读取 DPAPI 返回的缓冲
	return out, true
}
