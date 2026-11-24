package desktop

import (
	"encoding/json"

	"github.com/wt68/runcode/internal/imagegentool"
	appwire "github.com/wt68/runcode/internal/protocol"
)

// imageGenerationBlocks 把 image_generate 结果挂回重开的对话。会话账本是权威
// （终态与已记的记账观察）；持久化工具正文是回落——分叉会话有源历史前缀但没有
// 自己的账本。账本有记录、工具结果却从未写入历史（进程在保存后中断）时，按原
// 提问补一条。回放绝不重新提交：中断的提交显示为 unknown。
func imageGenerationBlocks(workspace, sessionID string, blocks []ResumedBlock) []ResumedBlock {
	records, err := imageGenerationReplayLedgers(workspace, sessionID)
	if err != nil {
		debugLog("image generation ledger for %s unreadable: %v", sessionID, err)
		records = nil
	}
	byToolUse := make(map[string]imageGenerationLedger, len(records))
	for _, rec := range records {
		if rec.ToolUseID != "" {
			byToolUse[rec.ToolUseID] = rec
		}
	}
	seen := map[string]bool{}
	questions := map[string]bool{}
	for i := range blocks {
		if blocks[i].Kind == "user" && blocks[i].QuestionID != "" {
			questions[blocks[i].QuestionID] = true
		}
		t := blocks[i].Tool
		if blocks[i].Kind != "tool" || t == nil || t.ToolName != imagegentool.Name {
			continue
		}
		seen[t.ToolUseID] = true
		if rec, ok := byToolUse[t.ToolUseID]; ok {
			result := rec.effectiveResult()
			t.ImageGeneration = &result
		} else if result, ok := imageGenerationResultFromContent(t.Output); ok {
			t.ImageGeneration = &result
		}
		if t.ImageGeneration != nil {
			t.IsError = t.ImageGeneration.Status != "succeeded"
		}
	}

	recovered := map[string][]ResumedBlock{}
	var orphans []ResumedBlock
	for _, rec := range records {
		toolUseID := rec.ToolUseID
		if toolUseID == "" {
			toolUseID = "image_generation_" + rec.GenerationID
		}
		if seen[toolUseID] {
			continue
		}
		result := rec.effectiveResult()
		block := ResumedBlock{Kind: "tool", Tool: &ResumedTool{
			ToolName: imagegentool.Name, ToolUseID: toolUseID, IsError: result.Status != "succeeded",
			Output: "已从生图账本恢复：" + result.Status, ImageGeneration: &result,
		}}
		if questions[rec.QuestionID] {
			recovered[rec.QuestionID] = append(recovered[rec.QuestionID], block)
		} else {
			orphans = append(orphans, block)
		}
	}
	if len(recovered) == 0 && len(orphans) == 0 {
		return blocks
	}
	out := make([]ResumedBlock, 0, len(blocks)+len(records))
	question := ""
	for _, b := range blocks {
		if b.Kind == "user" {
			out = append(out, recovered[question]...)
			question = b.QuestionID
		}
		out = append(out, b)
	}
	out = append(out, recovered[question]...)
	return append(out, orphans...)
}

// imageGenerationReplayLedgers is read-only and preserves every readable
// authoritative record when a different file is damaged. Only that record
// falls back to persisted tool content. Submission and budget paths retain the
// strict listImageGenerationLedgers contract.
func imageGenerationReplayLedgers(workspace, sessionID string) ([]imageGenerationLedger, error) {
	ids, err := imageGenerationLedgerIDs(workspace, sessionID)
	if err != nil {
		return nil, err
	}
	records := make([]imageGenerationLedger, 0, len(ids))
	for _, generationID := range ids {
		record, err := readImageGenerationLedger(workspace, sessionID, generationID)
		if err != nil {
			debugLog("image generation replay ledger %s/%s unreadable: %v", sessionID, generationID, err)
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

// imageGenerationResultFromContent 读取工具回给模型的有界结果 JSON（与现场卡片同源）。
func imageGenerationResultFromContent(content string) (appwire.ImageGenerationResult, bool) {
	var r appwire.ImageGenerationResult
	if json.Unmarshal([]byte(content), &r) != nil || r.Version != 1 || r.Status == "" {
		return appwire.ImageGenerationResult{}, false
	}
	if r.Images == nil || r.Status != "succeeded" {
		r.Images = []appwire.ImageGenerationArtifact{}
	}
	r.AccountingStatus = normalizeAccountingStatus(r.AccountingStatus)
	r.SubmissionStatus = normalizeSubmissionStatus(r.SubmissionStatus)
	return r, true
}
