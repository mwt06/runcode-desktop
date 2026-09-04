//go:build !windows && !darwin && !linux

package desktop

// 三大桌面平台之外没有接系统钥匙串，protectSecret 一律报 ok=false，调用方于是
// 把凭据从 desktop.json 里丢掉，而不是明文存下来（用户重新输入，或经环境变量提供）。
//
// 各平台的实现：Windows 走 DPAPI（secret_windows.go），macOS 走钥匙串、Linux 走
// Secret Service（secret_darwin.go / secret_linux.go，共用 secret_keyring.go 里的
// 主密钥与 AES-GCM 那一层）。
func protectSecret(string) (string, bool) { return "", false }

func unprotectSecret(string) (string, bool) { return "", false }
