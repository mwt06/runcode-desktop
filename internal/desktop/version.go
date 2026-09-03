package desktop

import (
	"strconv"
	"strings"
)

// 应用版本号与产品标识——更新检查的两个前提。
//
// **版本号的唯一事实来源是 cmd/runcode-desktop/build/config.yml 的 info.version**。
// 那份文件已经在喂三样东西：Windows 可执行文件的版本资源（右键属性 → 详细信息）、
// NSIS 安装包的 VIProductVersion、macOS 的 Info.plist。再在 Go 里写一个数就是第二个
// 来源，两处迟早对不上——而对不上的表现是「关于里写 0.2.0，添加删除程序里写 0.1.0」
// 这种装完机才看得见的错配，正是 scripts/build-desktop.sh 存在的理由。所以打包脚本
// 读 config.yml，经 -ldflags 注进来：
//
//	-X github.com/wt68/runcode/internal/desktop.appVersion=0.2.0
//	-X github.com/wt68/runcode/internal/desktop.appProduct=zhikai
//
// 下面两个默认值只在**没经过打包脚本**的构建里出现（go build、裸 wails3 task build）。
var (
	// appVersion 故意默认成 0.0.0-dev 而不是某个真版本号：任何真版本号都会在几次
	// 发版之后变成一句过期的谎言，而 0.0.0-dev 一眼就能认出是开发构建。按版本序它
	// 比任何正式版都小，于是开发构建查更新时会看到「有新版」——这是对的，它确实比
	// 线上的旧，而且这样「检查更新」这条链路在开发机上就能整条走通。
	appVersion = "0.0.0-dev"

	// appProduct 随品牌走。更新清单必须按它分开：XRUN 与智开是两个安装包、两个
	// bundle 标识符、两条发布节奏——给智开推 XRUN 的安装包，等于把用户的应用换成
	// 另一个牌子。
	appProduct = "xrun"
)

// AppVersion 是当前构建的版本号。
func AppVersion() string { return appVersion }

// AppProduct 是当前构建的产品标识。
func AppProduct() string { return appProduct }

// compareVersions 比较两个版本号，返回 -1 / 0 / 1（a 小于 / 等于 / 大于 b）。
//
// 认 semver 的常见形状：可选的 v 前缀、点分数字、可选的 -预发布 与 +构建元数据。
// 三条规则值得写明，因为它们决定"要不要提示用户更新"：
//
//  1. 缺位的段按 0 算，所以 1.2 == 1.2.0——发布时少写一段不该被当成降级。
//  2. 数字段解不出数字（1.2.x 这种）按 0 算。宁可判成"相等、不提示"，也不要拿一个
//     没读懂的字符串去劝用户装东西。
//  3. 有预发布标记的**小于**同号正式版（0.2.0-rc.1 < 0.2.0），这是 semver 的规矩，
//     也正是 0.0.0-dev 比一切正式版都小的原因。
//
// +构建元数据整段忽略（semver 规定它不参与比较）。
func compareVersions(a, b string) int {
	an, ap := splitVersion(a)
	bn, bp := splitVersion(b)
	for i := 0; i < len(an) || i < len(bn); i++ {
		x, y := numAt(an, i), numAt(bn, i)
		if x != y {
			return sign(x - y)
		}
	}
	return comparePre(ap, bp)
}

// splitVersion 把版本串拆成「数字段」与「预发布段」。
func splitVersion(v string) (nums []string, pre []string) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if i := strings.IndexByte(v, '+'); i >= 0 { // 构建元数据不参与比较
		v = v[:i]
	}
	core := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core, pre = v[:i], strings.Split(v[i+1:], ".")
	}
	if core != "" {
		nums = strings.Split(core, ".")
	}
	return nums, pre
}

// numAt 取第 i 个数字段，越界或解不出来都算 0（见上面的规则 1、2）。
func numAt(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
	if err != nil {
		return 0
	}
	return n
}

// comparePre 比较预发布段：没有预发布的一方更大；都有则逐段比，数字段按数值比、
// 其余按字典序，前缀相同时段数多的更大（semver 的规则）。
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1 // 正式版 > 预发布
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		x, xErr := strconv.Atoi(a[i])
		y, yErr := strconv.Atoi(b[i])
		switch {
		case xErr == nil && yErr == nil:
			if x != y {
				return sign(x - y)
			}
		case xErr == nil: // 数字段 < 文字段
			return -1
		case yErr == nil:
			return 1
		default:
			if a[i] != b[i] {
				return sign(strings.Compare(a[i], b[i]))
			}
		}
	}
	return sign(len(a) - len(b))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
