package tools

import (
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools/read"
)

func Builtins() []tool.Tool {
	return []tool.Tool{
		read.New(),
	}
}
