package repl

import (
	"encoding/json"
	"fmt"

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/tool"
)

func ToolResultBlock(result ExecuteResult) (llm.ContentBlock, error) {
	block := llm.ContentBlock{Type: llm.ContentBlockTypeToolResult, ToolUseID: result.ToolUseID, IsError: result.Result.IsError}
	if len(result.Result.Content) == 0 {
		return block, nil
	}

	content := make([]llm.ContentBlock, 0, len(result.Result.Content))
	for _, item := range result.Result.Content {
		switch item.Type {
		case tool.ResultContentTypeText:
			content = append(content, llm.ContentBlock{Type: llm.ContentBlockTypeText, Text: item.Text})
		case tool.ResultContentTypeJSON:
			data, err := json.Marshal(item.Data)
			if err != nil {
				return llm.ContentBlock{}, fmt.Errorf("marshal tool result json: %w", err)
			}
			content = append(content, llm.ContentBlock{Type: llm.ContentBlockTypeText, Text: string(data)})
		case tool.ResultContentTypeImage:
			if item.Image == nil {
				continue
			}
			content = append(content, llm.ContentBlock{
				Type: llm.ContentBlockTypeImage,
				Source: &llm.ImageSource{
					MediaType: item.Image.MediaType,
					Data:      item.Image.Data,
					URL:       item.Image.URL,
				},
			})
		default:
			return llm.ContentBlock{}, fmt.Errorf("unknown tool result content type: %q", item.Type)
		}
	}
	block.Content = content
	return block, nil
}
