// 品牌变量：两份外壳（Wails v3 的 main.go 与麒麟 V10 的 main_kylin.go）共用，
// 所以这个文件**不带构建标记**。放错地方的表现是另一份外壳编译时报 undefined。
//
// 两个值都由 scripts/build-desktop.sh 在构建时经 -ldflags 注入，来源与前端的
// VITE_BRAND、config.yml 的 productIdentifier 是同一个。

package main

// brandTitle is the OS window title (taskbar / alt-tab; the frameless window has
// no visible title bar — the frontend draws its own brand). It defaults to the
// original and is overridden at build time to match the frontend brand, e.g.:
//
//	wails3 build -ldflags "-X main.brandTitle=智开"
//
// alongside VITE_BRAND=zhikai for the frontend. See frontend/src/core/brand.ts,
// which is the source of truth for the in-app brand.
var brandTitle = "XRUN"

// brandID 是单实例锁的标识符，和 brandTitle 一样按品牌在构建时注入：
//
//	wails3 build -ldflags "-X main.brandID=cn.ouconline.ai.zhikai"
//
// **每个品牌必须不同**，理由和 macOS 的 CFBundleIdentifier 一模一样：相同标识符
// 会让两个品牌被当成同一个应用。在 Windows 上它的表现尤其难查——装了智开的人
// 只要 XRUN 还开着，双击智开就是**什么都不发生**：进程起来、发现锁被占、以退出码
// 0 干净退出，没有窗口、没有报错、事件日志里也没有东西。
//
// 值取品牌的 bundle 标识符，与 scripts/build-desktop.sh 里的 BUNDLE_ID、
// wails.json 的 productIdentifier、macOS Info.plist 保持同一个来源。
var brandID = "cn.ouconline.ai.xrun"
