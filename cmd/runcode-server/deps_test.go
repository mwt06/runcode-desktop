package main

// 依赖审计：本模块「只 import engine 模块公开面与标准库」的机器证明——
// 这是服务端交接面完整性的硬性约束（engine 之外不需要任何 runcode 内部包）。
//
//  1. 传递闭包（go list -deps ./...）内不允许出现 engine 模块与本模块之外的
//     任何 github.com/wt68/runcode/... 包（根模块、internal/、pkg/ 一律禁止）。
//  2. 本模块源码（含测试文件）的直接 import 只能是标准库、engine 模块、本模块
//     ——零第三方依赖（engine 自身的第三方依赖不在此列，它们随 engine 而来）。

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	enginePrefix = "github.com/wt68/runcode/engine"
	selfPrefix   = "github.com/wt68/runcode/cmd/runcode-server"
)

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	cmd := exec.Command("go", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, ee.Stderr)
		}
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// allowedRuncodeDep 判定一个 runcode 域内的包是否允许出现在依赖闭包里：
// 只有 engine 模块（github.com/wt68/runcode/engine[/...]）和本模块自身。
func allowedRuncodeDep(pkg string) bool {
	if pkg == selfPrefix || strings.HasPrefix(pkg, selfPrefix+"/") {
		return true
	}
	return pkg == enginePrefix || strings.HasPrefix(pkg, enginePrefix+"/")
}

func TestOnlyEnginePublicImports(t *testing.T) {
	// (1) 传递依赖闭包审计。
	for _, dep := range goList(t, "list", "-deps", "./...") {
		if !strings.HasPrefix(dep, "github.com/wt68/runcode") {
			continue
		}
		if !allowedRuncodeDep(dep) {
			t.Errorf("forbidden runcode dependency in closure: %s (only the engine module is allowed)", dep)
		}
	}

	// (2) 直接 import 审计（含 _test.go 的 TestImports/XTestImports）：
	// 标准库（首段无点号）之外，只允许 engine 与本模块。
	const sep = "\x1f"
	lines := goList(t, "list", "-f",
		"{{.ImportPath}}"+sep+"{{join .Imports \",\"}}"+sep+"{{join .TestImports \",\"}}"+sep+"{{join .XTestImports \",\"}}",
		"./...")
	for _, line := range lines {
		parts := strings.Split(line, sep)
		if len(parts) != 4 || !strings.HasPrefix(parts[0], selfPrefix) {
			continue
		}
		for _, imp := range strings.Split(strings.Join(parts[1:], ","), ",") {
			if imp == "" {
				continue
			}
			first := imp
			if i := strings.IndexByte(imp, '/'); i >= 0 {
				first = imp[:i]
			}
			if !strings.Contains(first, ".") {
				continue // 标准库
			}
			if !allowedRuncodeDep(imp) {
				t.Errorf("package %s directly imports %s; only stdlib and the engine module are allowed", parts[0], imp)
			}
		}
	}
}
