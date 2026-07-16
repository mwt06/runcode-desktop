// The engine is a nested module holding the transport-agnostic session engine
// (Build/Session facade, LLM providers, tools, permissions, persistence). It is
// the public reference shared by every shell: the CLI/TUI and desktop app in
// this repo, and any future server. It must never depend on the root module or
// on remote infrastructure (Redis clients, gateway SDKs) — ports are defined
// here, implementations live in the shells (dependency inversion).
module github.com/wt68/runcode/engine

go 1.26
