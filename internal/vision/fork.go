package vision

import (
	"context"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/imageinput"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// Fork copies only observations made for questions and images inside the retained
// prefix. Analyses from later questions must not leak backwards into a branch.
func Fork(ctx context.Context, workspace, source, target string, prefix []llm.Message) error {
	records, err := NewStore(workspace, source).List(ctx)
	if err != nil {
		return ctx.Err()
	} // Derived cache corruption never invents a result.
	refs := map[string]string{}
	questions := map[string]bool{}
	var visit func([]llm.ContentBlock)
	visit = func(blocks []llm.ContentBlock) {
		for _, b := range blocks {
			if b.Type == llm.ContentBlockTypeImage && b.Source != nil {
				refs[imageinput.Ref(source, *b.Source)] = imageinput.Ref(target, *b.Source)
			}
			visit(b.Content)
		}
	}
	for _, m := range prefix {
		if m.Role == llm.RoleUser && m.ID != "" {
			questions[m.ID] = true
		}
		visit(m.Content)
	}
	store := NewStore(workspace, target)
	for _, r := range records {
		if !questions[r.QuestionID] {
			continue
		}
		next := make([]string, len(r.Refs))
		valid := true
		for i, ref := range r.Refs {
			next[i] = refs[ref]
			if next[i] == "" {
				valid = false
			}
		}
		if !valid {
			continue
		}
		for i, ref := range r.Refs {
			r.Question = strings.ReplaceAll(r.Question, ref, next[i])
			r.Answer.Text = strings.ReplaceAll(r.Answer.Text, ref, next[i])
			r.Answer.Thinking = strings.ReplaceAll(r.Answer.Thinking, ref, next[i])
		}
		r.Refs = next
		r.Key = imageinput.CacheKey(r.RouteKey, next, r.Question)
		if err := store.Put(ctx, r); err != nil {
			return err
		}
	}
	return ctx.Err()
}
