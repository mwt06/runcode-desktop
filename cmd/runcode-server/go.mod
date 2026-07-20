// runcode-server 是服务端交接骨架：一个只依赖 engine 模块公开面的可跑参考实现。
//
// HANDOFF(module): 拷贝到独立仓库后——
//   1. 把 module 路径改成你们自己的（例如 gitlab.ouc-online.com.cn/aibase/runcode-server）；
//   2. require gitlab.ouc-online.com.cn/aibase/agentloop 固定到已发布 tag，
//      构建环境设 GOPRIVATE=gitlab.ouc-online.com.cn（私有模块直连内网 GitLab 拉取）。
// 除此之外代码零改动即可编译运行。
module github.com/wt68/runcode/cmd/runcode-server

go 1.26

require gitlab.ouc-online.com.cn/aibase/agentloop v0.3.0

require (
	github.com/anthropics/anthropic-sdk-go v1.45.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/invopop/jsonschema v0.13.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pelletier/go-toml/v2 v2.3.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	golang.org/x/net v0.41.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.30.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.52.0 // indirect
)
