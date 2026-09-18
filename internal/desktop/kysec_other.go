//go:build !linux

package desktop

import (
	"context"
	"errors"
)

// KYSEC 只存在于银河麒麟（Linux）。别的平台上执行控制恒为关，也就永远不会去加白。
var kysecExecControlOn = func() bool { return false }

func kysecTrust(context.Context, []elfFile, []string) error {
	return errors.New("本平台没有麒麟安全中心")
}

func envWith(map[string]string) []string { return nil }
