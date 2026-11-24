package mailtool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

type fakeHost struct {
	calls int
	last  SendInput
	meta  CallMeta
	ready DraftReady
	err   error
}

func (f *fakeHost) CreateProposal(_ context.Context, in SendInput, meta CallMeta) (DraftReady, error) {
	f.calls++
	f.last = in
	f.meta = meta
	if f.err != nil {
		return DraftReady{}, f.err
	}
	if f.ready.DraftID == "" {
		return DraftReady{Status: "draft_ready", DraftID: "d1", Revision: 1, Subject: in.Subject}, nil
	}
	return f.ready, nil
}

func TestSendEmailRejectsConfirmedBypass(t *testing.T) {
	h := &fakeHost{}
	toolInst := SendEmailTool{Host: h}
	raw := []byte(`{"subject":"s","bodyMarkdown":"b","to":[{"address":"a@b.c"}],"confirmed":true}`)
	_, err := toolInst.Run(context.Background(), raw, &tool.Context{SessionID: "s", MessageID: "m", ToolUseID: "t"}, nil)
	if err == nil || !strings.Contains(err.Error(), "confirmed") {
		t.Fatalf("want confirmed rejection, got %v", err)
	}
	if h.calls != 0 {
		t.Fatalf("host must not be called when confirmed present")
	}
}

func TestSendEmailCreatesDraftReadyOncePerCall(t *testing.T) {
	h := &fakeHost{}
	toolInst := SendEmailTool{Host: h}
	raw := []byte(`{"subject":"周报","bodyMarkdown":"内容","to":[{"address":"t@ex.com"}]}`)
	tc := &tool.Context{SessionID: "sess", MessageID: "msg1", ToolUseID: "tu1"}
	res, err := toolInst.Run(context.Background(), raw, tc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h.calls != 1 || h.meta.ToolUseID != "tu1" || h.meta.TurnID != "msg1" {
		t.Fatalf("meta=%+v calls=%d", h.meta, h.calls)
	}
	var ready DraftReady
	text := res.Content[0].Text
	if idx := strings.Index(text, "\n"); idx > 0 {
		text = text[:idx]
	}
	if err := json.Unmarshal([]byte(text), &ready); err != nil {
		t.Fatal(err)
	}
	if ready.Status != "draft_ready" || ready.DraftID == "" {
		t.Fatalf("ready=%+v", ready)
	}
	if !strings.Contains(res.Content[0].Text, "不要声称") {
		t.Fatal("result must remind model not to claim sent")
	}
}

func TestSendEmailNeedsMailSetupGuidesUser(t *testing.T) {
	h := &fakeHost{ready: DraftReady{Status: "draft_ready", DraftID: "d9", Revision: 2, NeedsMailSetup: true}}
	toolInst := SendEmailTool{Host: h}
	raw := []byte(`{"subject":"病假","bodyMarkdown":"请假","to":[{"address":"boss@ex.com"}]}`)
	res, err := toolInst.Run(context.Background(), raw, &tool.Context{SessionID: "s", MessageID: "m", ToolUseID: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, `"needsMailSetup":true`) {
		t.Fatalf("payload must carry needsMailSetup: %s", text)
	}
	if !strings.Contains(text, "设置 → 邮箱") {
		t.Fatalf("must guide user to mail settings: %s", text)
	}
}

func TestSearchContactsRequiresQuery(t *testing.T) {
	_, err := (SearchContactsTool{Searcher: searchFn(func(context.Context, string, int) ([]ContactHit, error) {
		return nil, nil
	})}).Run(context.Background(), []byte(`{"query":"  "}`), nil, nil)
	if err == nil {
		t.Fatal("empty query")
	}
}

type searchFn func(context.Context, string, int) ([]ContactHit, error)

func (f searchFn) SearchContacts(ctx context.Context, q string, limit int) ([]ContactHit, error) {
	return f(ctx, q, limit)
}
