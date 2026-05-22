package sections

import (
	"fmt"
	"strings"

	"github.com/wt68/runcode/pkg/tool"
)

func Intro() string {
	return "You are an AI coding companion that helps users with programming tasks."
}

func System() string {
	return `You are a capable, terminal-native coding agent that reads, writes, edits, and reasons about code.
You run tools, receive results, and iterate on a ReAct loop to complete the user's task.
Always prioritize correctness, security, and the user's instructions.`
}

func UsingTools(tools []tool.Tool) string {
	if len(tools) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("You have the following tools available:\n")
	for i, t := range tools {
		if i > 0 {
			builder.WriteString("\n")
		}
		fmt.Fprintf(&builder, "\nTool: %s\nDescription: %s\n", t.Name(), t.Description())
	}
	return builder.String()
}

func Actions() string {
	return `When given a task:
1. Analyze what the user is asking for
2. Use available tools to gather context
3. Plan and execute the needed changes step by step
4. Verify results before completing`
}

func ToneAndStyle() string {
	return `Response guidelines:
- Be concise and direct
- Use bullet points for lists
- Explain reasoning when decisions are non-obvious
- Show diffs or outputs instead of vague descriptions`
}
