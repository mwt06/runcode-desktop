// ccshim 是 windows-arm64 交叉编译时套在 clang 外面的一层壳：把 -mthreads 滤掉，
// 其余参数原样转交。
//
// 为什么需要它：-mthreads 不是本仓传的，是 Go 自己给 runtime/cgo 加的 cgo 指令
// （MinGW 的线程安全异常处理开关），改不了也关不掉。而 clang 对
// aarch64-pc-windows-msvc 这个目标：
//
//	· 20.1.6 —— 接受并忽略
//	· 22.1.8 —— 硬报错 "unsupported option '-mthreads'"
//
// runner 镜像升级到后者那天起，windows-arm64 整条编不过（2026-09-04 全绿，
// 2026-09-15 全红，两次之间代码只差一个引擎版本号）。对 MSVC 目标来说这个 flag
// 本来就没有意义，丢掉不改变任何语义。
//
// CGO 在本项目里关不掉（录音采集走 malgo），而镜像自带的 gcc 是 x86 的、编不了
// ARM64 汇编，所以只能用 clang——于是只剩"包一层"这一条路。
//
// 本目录以 . 开头，Go 工具链的 ./... 不会匹配到它，不影响任何自检。
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	args := make([]string, 0, len(os.Args))
	for _, a := range os.Args[1:] {
		if a == "-mthreads" {
			continue
		}
		args = append(args, a)
	}
	cmd := exec.Command("clang", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// clang 自己失败了：它已经把诊断打到 stderr，原样转达退出码即可，
			// 这里再多说一句只会把真正的编译错误顶出屏幕。
			os.Exit(ee.ExitCode())
		}
		// 没能把 clang 跑起来（不在 PATH 上、权限不对……）。这一支必须出声：
		// 悄悄 exit 1 的表现是"编译失败但一行错误都没有"，在 CI 上无从查起。
		fmt.Fprintf(os.Stderr, "ccshim: 无法执行 clang: %v\n", err)
		os.Exit(1)
	}
}
