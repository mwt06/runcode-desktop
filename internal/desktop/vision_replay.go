package desktop

import (
	"context"
	"encoding/json"

	"github.com/wt68/runcode/internal/vision"
	"gitlab.ouc-online.com.cn/aibase/agentloop/imageinput"
)

func imageAnalysisBlocks(ctx context.Context, workspace, id string, blocks []ResumedBlock) []ResumedBlock {
	records, err := vision.NewStore(workspace, id).List(ctx)
	if err != nil {
		return blocks
	}
	grouped := map[string][]ResumedBlock{}
	for _, r := range records {
		if r.QuestionID == "" || !r.Automatic {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"images": r.Refs, "question": r.Question, "model": r.Answer.Model})
		text := r.Answer.Text
		if r.Answer.Thinking != "" {
			text += "\n\n识图模型的理解与判断：\n" + r.Answer.Thinking
		}
		grouped[r.QuestionID] = append(grouped[r.QuestionID], ResumedBlock{Kind: "tool", Tool: &ResumedTool{
			InputTokens: r.Answer.Usage.InputTokens, OutputTokens: r.Answer.Usage.OutputTokens, ToolName: imageinput.ToolName, ToolUseID: "image_saved_" + r.Key, Input: string(raw), Output: "已保存的识图结果 · " + r.Answer.Model + "\n" + text,
		}})
	}
	out := make([]ResumedBlock, 0, len(blocks)+len(records))
	question := ""
	for _, b := range blocks {
		if b.Kind == "user" {
			out = append(out, grouped[question]...)
			question = b.QuestionID
		}
		out = append(out, b)
	}
	return append(out, grouped[question]...)
}
