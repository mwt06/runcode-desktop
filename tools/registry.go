package tools

import (
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools/analyze"
	"github.com/wt68/runcode/tools/askuser"
	"github.com/wt68/runcode/tools/bash"
	"github.com/wt68/runcode/tools/delete"
	"github.com/wt68/runcode/tools/edit"
	"github.com/wt68/runcode/tools/glob"
	"github.com/wt68/runcode/tools/grep"
	"github.com/wt68/runcode/tools/read"
	"github.com/wt68/runcode/tools/todo"
	"github.com/wt68/runcode/tools/webfetch"
	"github.com/wt68/runcode/tools/websearch"
	"github.com/wt68/runcode/tools/write"
)

// Builtins returns the in-tree tools with a self-contained background-shell
// manager. Callers that need to terminate background shells on shutdown should
// use BuiltinsWithShells and Close the manager themselves.
func Builtins() []tool.Tool {
	return BuiltinsWithShells(bash.NewManager())
}

// BuiltinsWithShells returns the in-tree tools in a stable, curated order, wiring
// the Bash/BashOutput/KillShell trio to the shared shell manager so background
// launches are readable and killable. They are assembled through a tool.Registry
// so the order is explicit in one place and a duplicate name fails loudly.
func BuiltinsWithShells(shells *bash.Manager) []tool.Tool {
	r := tool.NewRegistry()
	for _, t := range []tool.Tool{
		read.New(),
		write.New(),
		edit.New(),
		delete.New(),
		glob.New(),
		grep.New(),
		bash.NewWithManager(shells),
		bash.NewBashOutput(shells),
		bash.NewKillShell(shells),
		todo.New(),
		webfetch.New(),
		websearch.New(),
		analyze.New(),
		askuser.New(),
	} {
		r.MustRegister(t)
	}
	return r.All()
}
