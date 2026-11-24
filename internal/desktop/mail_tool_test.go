package desktop

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wt68/runcode/internal/mail"
	"github.com/wt68/runcode/internal/mailtool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

func TestMailSendEmailIdempotentAndNoSMTP(t *testing.T) {
	isolateConfigDir(t)
	resetMailStoreForTest()
	t.Cleanup(resetMailStoreForTest)
	db := filepath.Join(t.TempDir(), "mail.sqlite3")
	openMailStore = func(string) (*mail.Store, error) { return mail.OpenStore(db) }
	t.Cleanup(func() {
		openMailStore = mail.OpenStore
	})

	app := &App{}
	host := mailDraftHost{app: app, sessionID: "sess1"}
	in := mailtool.SendInput{
		Subject: "周报", BodyMarkdown: "内容",
		To: []mailtool.AddressIn{{Address: "a@ex.com"}},
	}
	meta := mailtool.CallMeta{SessionID: "sess1", TurnID: "turn1", ToolUseID: "tu1"}
	a, err := host.CreateProposal(context.Background(), in, meta)
	if err != nil {
		t.Fatal(err)
	}
	b, err := host.CreateProposal(context.Background(), in, meta)
	if err != nil {
		t.Fatal(err)
	}
	if a.DraftID != b.DraftID || a.Revision != b.Revision {
		t.Fatalf("idempotent mismatch %+v vs %+v", a, b)
	}
	if a.Status != "draft_ready" {
		t.Fatalf("status=%s", a.Status)
	}
	if !a.NeedsMailSetup {
		t.Fatal("no mailbox configured: want NeedsMailSetup")
	}
}

func TestMailCreateProposalNeedsMailSetupClearsWhenAccountReady(t *testing.T) {
	isolateConfigDir(t)
	resetMailStoreForTest()
	t.Cleanup(resetMailStoreForTest)
	db := filepath.Join(t.TempDir(), "mail.sqlite3")
	openMailStore = func(string) (*mail.Store, error) { return mail.OpenStore(db) }
	t.Cleanup(func() { openMailStore = mail.OpenStore })

	if err := updateRawConfig(func(cfg *desktopConfig) error {
		cfg.MailAccount = &mailAccountRecord{
			ID: "acc1", Address: "me@ex.com", Enabled: true,
			PasswordProtected: "opaque", Host: "smtp.example.com", Port: 465,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	host := mailDraftHost{app: &App{}, sessionID: "sess1"}
	ready, err := host.CreateProposal(context.Background(), mailtool.SendInput{
		Subject: "s", BodyMarkdown: "b", To: []mailtool.AddressIn{{Address: "a@ex.com"}},
	}, mailtool.CallMeta{SessionID: "sess1", TurnID: "t", ToolUseID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if ready.NeedsMailSetup {
		t.Fatalf("configured account must clear NeedsMailSetup: %+v", ready)
	}
}

func TestMailSendEmailOALockedRejected(t *testing.T) {
	isolateConfigDir(t)
	app := &App{}
	app.mu.Lock()
	if app.sessions == nil {
		app.sessions = map[string]*sessionEntry{}
	}
	app.sessions["sess-oa"] = &sessionEntry{id: "sess-oa", oaLocalModel: "qwen3.6-27b"}
	app.mu.Unlock()

	host := mailDraftHost{app: app, sessionID: "sess-oa"}
	_, err := host.CreateProposal(context.Background(), mailtool.SendInput{
		Subject: "s", BodyMarkdown: "b", To: []mailtool.AddressIn{{Address: "a@b.c"}},
	}, mailtool.CallMeta{SessionID: "sess-oa", TurnID: "t", ToolUseID: "u"})
	if err == nil {
		t.Fatal("OA lock must refuse proposal")
	}
}

func TestMailToolsRegisteredOnExtraToolsList(t *testing.T) {
	app := &App{}
	tools := app.mailTools(nil, "s1")
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name()] = true
	}
	if !names[mailtool.SearchContactsName] || !names[mailtool.SendEmailName] || !names[mailtool.AddMailContactName] {
		t.Fatalf("tools=%v", names)
	}
	if _, ok := hostToolClasses[mailtool.SendEmailName]; !ok {
		t.Fatal("hostToolClasses missing send_email")
	}
	if _, ok := hostToolClasses[mailtool.SearchContactsName]; !ok {
		t.Fatal("hostToolClasses missing search_contacts")
	}
	if hostToolClasses[mailtool.AddMailContactName] != permissions.ClassMutating {
		t.Fatal("hostToolClasses missing add_mail_contact as mutating")
	}
	_ = tool.Result{}
}
