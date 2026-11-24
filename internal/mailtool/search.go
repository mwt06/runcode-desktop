package mailtool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// ContactHit 是给模型看的通信录命中；同名多条时全部返回，禁止工具侧猜地址。
type ContactHit struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Emails []string `json:"emails"`
}

// ContactSearcher 由 desktop 注入，只查当前 owner 的本地通信录。
type ContactSearcher interface {
	SearchContacts(ctx context.Context, query string, limit int) ([]ContactHit, error)
}

type searchInput struct {
	Query string `json:"query"`
}

// SearchContactsTool 是只读联系人查询。
type SearchContactsTool struct {
	Searcher ContactSearcher
}

// Name 返回 search_contacts。
func (SearchContactsTool) Name() string { return SearchContactsName }

// InputPresentation 预览查询词。
func (SearchContactsTool) InputPresentation() tool.InputPresentation {
	return tool.InputPresentation{PrimaryField: "query"}
}

// Description 告诉模型：本地通信录、最多 20 条、同名须让用户选。
func (SearchContactsTool) Description() string {
	return "Search the user's local mail contacts by name or email substring. " +
		"Returns at most 20 matches. Duplicate names are returned as separate rows — " +
		"never guess which address to use; ask the user or pass the chosen contact id/email into send_email. " +
		"This tool does not send mail and does not access OA or the network."
}

// InputSchema 声明 query。
func (SearchContactsTool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"query": {Type: tool.SchemaTypeString, Description: "Name or email substring to search."},
		},
		Required:             []string{"query"},
		AdditionalProperties: false,
	}
}

// IsConcurrencySafe 报告可读并发安全。
func (SearchContactsTool) IsConcurrencySafe() bool { return true }

// Run 执行查询并返回 JSON 文本结果。
func (t SearchContactsTool) Run(ctx context.Context, raw json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in searchInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse search_contacts input: %w", err)
	}
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return tool.Result{}, fmt.Errorf("query is required")
	}
	if t.Searcher == nil {
		return tool.Result{}, fmt.Errorf("contacts search is unavailable")
	}
	hits, err := t.Searcher.SearchContacts(ctx, q, 20)
	if err != nil {
		return tool.Result{}, err
	}
	if hits == nil {
		hits = []ContactHit{}
	}
	payload, err := json.Marshal(map[string]any{
		"count":    len(hits),
		"contacts": hits,
		"note":     "If multiple contacts share a name, ask the user which one; do not guess.",
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: string(payload)}}}, nil
}
