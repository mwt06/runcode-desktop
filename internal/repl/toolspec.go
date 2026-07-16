package repl

import (
	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/tool"
)

func ToolSpecs(tools []tool.Tool) []llm.ToolSpec {
	if tools == nil {
		return nil
	}
	specs := make([]llm.ToolSpec, 0, len(tools))
	for _, t := range tools {
		specs = append(specs, llm.ToolSpec{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return specs
}
