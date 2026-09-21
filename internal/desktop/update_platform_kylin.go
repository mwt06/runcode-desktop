//go:build kylin

package desktop

// updateOS 在麒麟 V10 那份外壳（-tags kylin，Wails v2）里是 kylin10 而不是 linux。
//
// V10 与 V11 的 GOOS/GOARCH 一模一样，可它们**装不了同一个包**：V10 的包依赖
// libwebkit2gtk-4.0-37，V11 的依赖 libwebkit2gtk-4.1-0，各自的系统里都没有对方那个
// （见 CLAUDE.md「两份外壳」）。平台键只到 linux/arm64 的话，Bridge 上这一格只能放
// 其中一个，另一边的用户点了更新会下载、校验一路绿灯，最后装不上——而且是每一版都这样。
//
// 用构建标记而不是 ldflags 注入：kylin 这个标记本身就是「这是 V10 的包」的定义
// （它选中 main_kylin.go 那份外壳），拿它当依据就不会出现「外壳是 V10、上报的却是
// linux」这种两处对不上的构建。
//
// 1.0.17 及以前的 V10 客户端上报的仍是 linux/<架构>，那一格要等它们都升上来之后
// 才能改放 V11 的包。
const updateOS = "kylin10"
