package tools

import (
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools/bash"
	"github.com/wt68/runcode/tools/edit"
	"github.com/wt68/runcode/tools/glob"
	"github.com/wt68/runcode/tools/grep"
	"github.com/wt68/runcode/tools/read"
	"github.com/wt68/runcode/tools/todo"
	"github.com/wt68/runcode/tools/webfetch"
	"github.com/wt68/runcode/tools/write"
)

// Builtins returns the in-tree tools in a stable, curated order. They are
// assembled through a tool.Registry so the order is explicit in one place and a
// duplicate name would fail loudly at startup rather than silently shadow.
func Builtins() []tool.Tool {
	r := tool.NewRegistry()
	for _, t := range []tool.Tool{
		read.New(),
		write.New(),
		edit.New(),
		glob.New(),
		grep.New(),
		bash.New(),
		todo.New(),
		webfetch.New(),
	} {
		r.MustRegister(t)
	}
	return r.All()
}
