//go:build !linux

package desktop

// askpass 只在 Linux 上开放：来者核验靠 /proc 与 SO_PEERCRED，别处没有等价物。没有
// 这一步，密码框就可能被模型直接拉起、把密码读进工具输出——那比没有 sudo 糟得多。
// 于是这里的 sudo 保持引擎原来的硬拒（askpassReady 为假，privilegePolicy 不放行）。

import "errors"

var errAskpassUnsupported = errors.New("本平台不支持应用内的 sudo 密码框")

func startAskpassServer(*askpassBroker) (*askpassServer, error) {
	return nil, errAskpassUnsupported
}

// IsAskpass 在本平台恒为假：没有人会把本应用当成 askpass 拉起。
func IsAskpass() bool { return false }

// RunAskpass 在本平台不该被调到。
func RunAskpass([]string) int { return 1 }
