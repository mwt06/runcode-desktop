// The engine is a nested module holding the transport-agnostic session engine
// (Build/Session facade, LLM providers, tools, permissions, persistence). It is
// the public reference shared by every shell: the CLI/TUI and desktop app in
// this repo, and any future server. It must never depend on the root module or
// on remote infrastructure (Redis clients, gateway SDKs) — ports are defined
// here, implementations live in the shells (dependency inversion).
module github.com/wt68/runcode/engine

go 1.26

require (
	github.com/anthropics/anthropic-sdk-go v1.45.0
	golang.org/x/net v0.41.0
)

require (
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/invopop/jsonschema v0.13.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/text v0.30.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
