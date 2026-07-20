// Package compaction condenses old conversation turns into a summary message so
// long sessions stay within a context budget without mechanically dropping
// history. It operates only on in-memory working history; the on-disk session
// store keeps the full, loss-less conversation.
package compaction

import (
	"context"
	"fmt"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// DefaultKeepRecentTurns is the number of most-recent turns kept verbatim when
// the caller does not specify one.
const DefaultKeepRecentTurns = 4

const summaryPrefix = "Summary of earlier conversation (condensed to save context):\n\n"

// summarySeparator joins an existing summary body with a newly summarized
// increment so previously condensed facts are retained verbatim.
const summarySeparator = "\n\n"

// Summarizer condenses a slice of messages into a short text summary. It is
// supplied by the caller (which owns the LLM provider) so this package has no
// provider dependency.
type Summarizer func(ctx context.Context, messages []llm.Message) (string, error)

// Options bounds compaction.
type Options struct {
	// KeepRecentTurns is how many of the most recent turns to keep verbatim.
	KeepRecentTurns int
	// SummaryCharBudget bounds the retained summary body. While the accumulated
	// summary body stays under it, compaction is incremental: the prior summary
	// is kept verbatim and only the newly aged-out turns are summarized and
	// appended, so already-condensed facts never pass through the LLM twice.
	// Once the body exceeds the budget, the whole summary is recompacted once
	// (a deliberate, low-frequency lossy pass). 0 disables the cap, so the
	// summary grows unbounded — incremental forever.
	SummaryCharBudget int
	// Summarize produces the summary of the older turns; required.
	Summarize Summarizer
}

// Compact condenses the oldest turns of history into a single leading summary
// message and keeps the most recent turns verbatim. It is incremental: when the
// history already begins with a summary message, that summary's text is retained
// verbatim and only the turns that have aged out since then are summarized and
// appended — already-condensed facts never pass through the LLM a second time
// (which is how repeated compaction silently drops information). The whole
// summary is only re-summarized when its body outgrows opts.SummaryCharBudget.
//
// It returns the history unchanged when there is nothing safe to compact: no
// Summarizer, no newly aged-out turns beyond the kept tail, an empty summary, or
// a boundary that would orphan a tool_use/tool_result pair.
func Compact(ctx context.Context, history []llm.Message, opts Options) ([]llm.Message, error) {
	keep := opts.KeepRecentTurns
	if keep <= 0 {
		keep = DefaultKeepRecentTurns
	}
	if opts.Summarize == nil {
		return history, nil
	}

	// Peel off a leading summary message so it is never re-summarized as part of
	// "older": its body is carried forward verbatim.
	priorBody := ""
	rest := history
	if len(history) > 0 && isSummaryMessage(history[0]) {
		priorBody = summaryBody(history[0])
		rest = history[1:]
	}

	turns := splitTurns(rest)
	if len(turns) <= keep {
		// No turn has aged out since the last summary — nothing new to fold in.
		return history, nil
	}

	older := flatten(turns[:len(turns)-keep])
	recent := flatten(turns[len(turns)-keep:])

	// Only compact when both halves are self-contained: the summary replaces a
	// clean prefix, and the kept tail starts a fresh user turn. Otherwise leave
	// history intact rather than send a discontinuous conversation.
	if !pairedClean(older) || !pairedClean(recent) || len(recent) == 0 || recent[0].Role != llm.RoleUser {
		return history, nil
	}

	newBody, err := buildSummaryBody(ctx, opts, priorBody, older)
	if err != nil {
		return nil, err
	}
	if newBody == "" {
		return history, nil
	}

	compacted := make([]llm.Message, 0, 1+len(recent))
	compacted = append(compacted, makeSummaryMessage(newBody))
	compacted = append(compacted, recent...)
	return compacted, nil
}

// buildSummaryBody produces the next summary body. With no prior summary, or a
// prior summary still within budget, it summarizes only the newly aged-out
// turns and appends them to the prior body (incremental, no re-summarization of
// existing summary text). Once the prior body exceeds the budget, it folds the
// prior summary into the first older turn and re-summarizes the whole thing —
// folding (rather than prepending a second user message) keeps the sequence
// strictly user/assistant-alternating for the provider.
func buildSummaryBody(ctx context.Context, opts Options, priorBody string, older []llm.Message) (string, error) {
	recompact := priorBody != "" && opts.SummaryCharBudget > 0 && len(priorBody) > opts.SummaryCharBudget

	input := older
	if recompact {
		input = foldSummaryIntoFirstTurn(priorBody, older)
	}

	summary, err := opts.Summarize(ctx, input)
	if err != nil {
		return "", fmt.Errorf("summarize history: %w", err)
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "", nil
	}

	if priorBody == "" || recompact {
		return summary, nil
	}
	return priorBody + summarySeparator + summary, nil
}

// isSummaryMessage reports whether a message is a compaction summary message.
// Summary messages are produced by makeSummaryMessage as a single text block
// carrying summaryPrefix; requiring that exact shape (not just any user message
// whose text starts with the prefix) avoids misreading a user message that
// happens to begin with the prefix.
func isSummaryMessage(m llm.Message) bool {
	if m.Role != llm.RoleUser || len(m.Content) != 1 {
		return false
	}
	block := m.Content[0]
	return block.Type == llm.ContentBlockTypeText && strings.HasPrefix(block.Text, summaryPrefix)
}

// summaryBody returns the summary text of a summary message, without the prefix.
func summaryBody(m llm.Message) string {
	for _, block := range m.Content {
		if block.Type == llm.ContentBlockTypeText {
			return strings.TrimPrefix(block.Text, summaryPrefix)
		}
	}
	return ""
}

// makeSummaryMessage wraps a summary body in a prefixed user message.
func makeSummaryMessage(body string) llm.Message {
	return llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: summaryPrefix + body}},
	}
}

// foldSummaryIntoFirstTurn prepends the prior summary body into the first user
// turn's leading text block, returning a copy. The older slice always starts
// with a user message (splitTurns delimits on user turns), so the result stays
// user/assistant-alternating without inserting an extra message.
func foldSummaryIntoFirstTurn(priorBody string, older []llm.Message) []llm.Message {
	folded := make([]llm.Message, len(older))
	copy(folded, older)
	if len(folded) == 0 {
		return folded
	}
	head := folded[0]
	blocks := make([]llm.ContentBlock, len(head.Content))
	copy(blocks, head.Content)
	preamble := "Earlier summary to retain (fold its facts into your summary):\n" + priorBody + "\n\n--- conversation continues ---\n\n"
	injected := false
	for i, block := range blocks {
		if block.Type == llm.ContentBlockTypeText {
			blocks[i].Text = preamble + block.Text
			injected = true
			break
		}
	}
	if !injected {
		blocks = append([]llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: preamble}}, blocks...)
	}
	head.Content = blocks
	folded[0] = head
	return folded
}

// splitTurns groups messages into per-turn segments delimited by user messages.
func splitTurns(messages []llm.Message) [][]llm.Message {
	var turns [][]llm.Message
	start := 0
	for i, message := range messages {
		if i > start && message.Role == llm.RoleUser {
			turns = append(turns, messages[start:i])
			start = i
		}
	}
	if start < len(messages) {
		turns = append(turns, messages[start:])
	}
	return turns
}

func flatten(turns [][]llm.Message) []llm.Message {
	var out []llm.Message
	for _, turn := range turns {
		out = append(out, turn...)
	}
	return out
}

// pairedClean reports whether every assistant tool_use is immediately followed
// by a tool message carrying exactly the matching tool_result IDs, with no
// orphan tool messages.
func pairedClean(messages []llm.Message) bool {
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		switch message.Role {
		case llm.RoleTool:
			return false
		case llm.RoleAssistant:
			if len(toolUseIDs(message)) == 0 {
				continue
			}
			if i+1 >= len(messages) || messages[i+1].Role != llm.RoleTool {
				return false
			}
			if !resultsMatch(message, messages[i+1]) {
				return false
			}
			i++
		}
	}
	return true
}

func resultsMatch(assistant llm.Message, toolMessage llm.Message) bool {
	uses := toolUseIDs(assistant)
	results := toolResultIDs(toolMessage)
	if len(uses) != len(results) {
		return false
	}
	for id := range uses {
		if _, ok := results[id]; !ok {
			return false
		}
	}
	return true
}

func toolUseIDs(message llm.Message) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, block := range message.Content {
		if block.Type == llm.ContentBlockTypeToolUse && block.ID != "" {
			ids[block.ID] = struct{}{}
		}
	}
	return ids
}

func toolResultIDs(message llm.Message) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, block := range message.Content {
		if block.Type == llm.ContentBlockTypeToolResult && block.ToolUseID != "" {
			ids[block.ToolUseID] = struct{}{}
		}
	}
	return ids
}
