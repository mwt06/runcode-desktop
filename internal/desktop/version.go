package desktop

import (
	"github.com/wt68/runcode/internal/appupdate"
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

func compareVersions(a, b string) int { return appupdate.CompareVersions(a, b) }
