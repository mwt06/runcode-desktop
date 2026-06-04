package tools

import (
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools/bash"
	"github.com/wt68/runcode/tools/edit"
	"github.com/wt68/runcode/tools/glob"
	"github.com/wt68/runcode/tools/grep"
	"github.com/wt68/runcode/tools/read"
	"github.com/wt68/runcode/tools/todo"
	"github.com/wt68/runcode/tools/write"
)

func Builtins() []tool.Tool {
	return []tool.Tool{
		read.New(),
		write.New(),
		edit.New(),
		glob.New(),
		grep.New(),
		bash.New(),
		todo.New(),
	}
}
