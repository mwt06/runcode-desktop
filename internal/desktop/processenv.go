package desktop

import (
	"os"
	"runtime"
	"strings"
)

// envWith 在本进程环境上叠加 extra（同名覆盖，不修改本进程）。
// 原来只服务于 Linux 的 KYSEC；macOS 更新与两平台的 askpass 都需要它，
// 不能沿用非 Linux 返回 nil 的占位实现，否则 sudo 收不到密码助手地址。
func envWith(extra map[string]string) []string {
	key := func(s string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(s)
		}
		return s
	}
	overrides := make(map[string]bool, len(extra))
	for k := range extra {
		overrides[key(k)] = true
	}
	inherited := os.Environ()
	env := make([]string, 0, len(inherited)+len(extra))
	for _, kv := range inherited {
		k, _, _ := strings.Cut(kv, "=")
		if !overrides[key(k)] {
			env = append(env, kv)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
