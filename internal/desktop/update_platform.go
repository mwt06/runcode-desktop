//go:build !kylin

package desktop

import "runtime"

// updateOS 是更新平台键的前一半。除麒麟 V10 那份外壳之外就是 GOOS——麒麟 V11 与
// 其它 Linux 发行版用的是同一个 v3 的包，报 linux 正合适。V10 为什么要单独一个名字，
// 见 update_platform_kylin.go。
const updateOS = runtime.GOOS
