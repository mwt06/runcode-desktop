package repl

import (
	"encoding/json"
	"unicode"

	"github.com/wt68/runcode/pkg/llm"
)

// EstimateContextTokens roughly approximates how many input tokens the next turn
// would send — the system prompt, tool schemas, and the full working history —
// without a tokenizer. It seeds the context-usage indicator when a session is
// resumed, before any turn has reported a provider-measured count; the first real
// turn then replaces it with the exact value. Returns 0 if the request cannot be
// assembled.
func (s *Session) EstimateContextTokens() int {
	req, err := s.buildRequestWithMessagesAndPrompt(s.historySnapshot(), s.prompt)
	if err != nil {
		return 0
	}
	return estimateRequestTokens(req)
}

func estimateRequestTokens(req llm.Request) int {
	total := 0
	for _, b := range req.System {
		total += estimateBlockTokens(b)
	}
	for _, m := range req.Messages {
		for _, b := range m.Content {
			total += estimateBlockTokens(b)
		}
	}
	// Tool schemas are sent to the model as JSON, so their serialized form is a fair
	// proxy for their token cost.
	if data, err := json.Marshal(req.Tools); err == nil {
		total += estimateTokens(string(data))
	}
	return total
}

// estimateBlockTokens estimates one content block, descending into tool_result
// payloads (which nest their content) and counting tool-call input JSON.
func estimateBlockTokens(b llm.ContentBlock) int {
	n := estimateTokens(b.Text)
	if len(b.Input) > 0 {
		n += estimateTokens(string(b.Input))
	}
	for _, inner := range b.Content {
		n += estimateBlockTokens(inner)
	}
	return n
}

// estimateTokens approximates a string's token count without a tokenizer: CJK
// codepoints count ~1 token each; other runes ~1 per 4 (typical for English, JSON,
// and code). Deliberately rough — it only seeds the resume-time bar.
func estimateTokens(text string) int {
	cjk, other := 0, 0
	for _, r := range text {
		if isCJKRune(r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}
