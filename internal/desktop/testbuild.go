package desktop

// testBuild 是"测试版构建"标记，构建时经 ldflags 注入：
//
//	go build -ldflags "-X github.com/wt68/runcode/internal/desktop.testBuild=1"
//
// （打包走 scripts/build-desktop.sh --test，它会连同品牌 ldflags 一起配好。）
// 仅测试版存在的功能（上下文审核）以它为总开关：正式包不注入，后端命令拒绝开启、
// 设置页不渲染入口、会话也不接观测器——功能整体不存在，而不是"藏起来"。
var testBuild = ""

// IsTestBuild reports whether this binary was built as a 测试版 (ldflags-injected).
func IsTestBuild() bool { return testBuild != "" }
