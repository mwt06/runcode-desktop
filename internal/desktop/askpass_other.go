//go:build (!linux && !darwin) || (darwin && !cgo)

package desktop

// Windows 用系统 UAC；macOS 的无 cgo 构建缺少 libproc 核验，保持密码通道关闭。

import "errors"

var errAskpassUnsupported = errors.New("本平台不支持应用内的 sudo 密码框")

func startAskpassServer(*askpassBroker) (*askpassServer, error) {
	return nil, errAskpassUnsupported
}

// IsAskpass 在本平台恒为假：没有人会把本应用当成 askpass 拉起。
func IsAskpass() bool { return false }

// RunAskpass 在本平台不该被调到。
func RunAskpass([]string) int { return 1 }
