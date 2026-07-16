package tools

import (
	"net/http"

	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/tools/analyze"
	"github.com/wt68/runcode/engine/tools/askuser"
	"github.com/wt68/runcode/engine/tools/bash"
	"github.com/wt68/runcode/engine/tools/delete"
	"github.com/wt68/runcode/engine/tools/edit"
	"github.com/wt68/runcode/engine/tools/glob"
	"github.com/wt68/runcode/engine/tools/grep"
	"github.com/wt68/runcode/engine/tools/read"
	"github.com/wt68/runcode/engine/tools/todo"
	"github.com/wt68/runcode/engine/tools/webfetch"
	"github.com/wt68/runcode/engine/tools/websearch"
	"github.com/wt68/runcode/engine/tools/write"
)

// Config carries per-session tool construction options; the zero value
// reproduces the historical defaults.
type Config struct {
	// WebClient, when set, backs the WebFetch/WebSearch tools so a host can route
	// a session's web traffic through its own (e.g. per-session proxied) client.
	// nil keeps each tool's own webclient default, including the proxy env
	// fallback.
	WebClient *http.Client
	// ShellEnv is extra child-process environment merged into every Bash command
	// (foreground and background), see bash.Manager. It is host-supplied and
	// overrides inherited variables of the same name; nil adds nothing — the
	// historical inherit-only behavior.
	ShellEnv map[string]string
}

// Builtins returns the in-tree tools with a self-contained background-shell
// manager. Callers that need to terminate background shells on shutdown should
// use BuiltinsWithShells and Close the manager themselves.
func Builtins() []tool.Tool {
	return BuiltinsWithShells(bash.NewManager())
}

// BuiltinsWithShells returns the in-tree tools with historical defaults, wiring
// the Bash/BashOutput/KillShell trio to the shared shell manager so background
// launches are readable and killable.
func BuiltinsWithShells(shells *bash.Manager) []tool.Tool {
	return BuiltinsWithConfig(shells, Config{})
}

// BuiltinsWithConfig returns the in-tree tools in a stable, curated order,
// applying cfg's per-session construction options (a zero cfg reproduces
// BuiltinsWithShells exactly). The tools are assembled through a tool.Registry
// so the order is explicit in one place and a duplicate name fails loudly.
func BuiltinsWithConfig(shells *bash.Manager, cfg Config) []tool.Tool {
	r := tool.NewRegistry()
	for _, t := range []tool.Tool{
		read.New(),
		write.New(),
		edit.New(),
		delete.New(),
		glob.New(),
		grep.New(),
		bash.NewWithConfig(shells, cfg.ShellEnv),
		bash.NewBashOutput(shells),
		bash.NewKillShell(shells),
		todo.New(),
		webfetch.NewWithClient(cfg.WebClient),
		websearch.NewWithClient(cfg.WebClient),
		analyze.New(),
		askuser.New(),
	} {
		r.MustRegister(t)
	}
	return r.All()
}
