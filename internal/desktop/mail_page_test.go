package desktop

import (
	"path/filepath"
	"testing"

	"github.com/wt68/runcode/internal/mail"
	"github.com/wt68/runcode/internal/protocol"
)

func TestMailPageContactsAndDraftCAS(t *testing.T) {
	isolateConfigDir(t)
	path := filepath.Join(t.TempDir(), "mail.sqlite3")
	prev := openMailStore
	openMailStore = func(string) (*mail.Store, error) { return mail.OpenStore(path) }
	t.Cleanup(func() { openMailStore = prev; resetMailStoreForTest() })
	resetMailStoreForTest()

	app := New(&recordingSink{})
	c, err := app.SaveMailContact(protocol.SaveMailContactRequest{Name: "王老师", Emails: []string{"a@x.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveMailContact(protocol.SaveMailContactRequest{Name: "重", Emails: []string{"a@x.com"}}); err == nil {
		t.Fatal("want duplicate rejected")
	}
	d, err := app.CreateMailDraft(protocol.CreateMailDraftRequest{})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := app.SaveMailDraft(protocol.SaveMailDraftRequest{
		ID: d.ID, ExpectedRevision: d.Revision, AccountID: "acc",
		Subject: "主题", BodyMarkdown: "正文",
		To:           []protocol.MailAddressView{{Address: "a@x.com"}},
		SendMode:     "together",
		DispatchMode: "immediate",
	})
	if err != nil || saved.Revision != d.Revision+1 {
		t.Fatalf("%+v %v", saved, err)
	}
	if _, err := app.SaveMailDraft(protocol.SaveMailDraftRequest{
		ID: d.ID, ExpectedRevision: d.Revision, AccountID: "acc",
		Subject: "冲突", BodyMarkdown: "正文",
		To:           []protocol.MailAddressView{{Address: "a@x.com"}},
		SendMode:     "together",
		DispatchMode: "immediate",
	}); err == nil {
		t.Fatal("want conflict")
	}
	list, err := app.ListMailDrafts(protocol.ListMailDraftsRequest{Limit: 20})
	if err != nil || len(list) != 1 || list[0].Subject != "主题" {
		t.Fatalf("%+v %v", list, err)
	}
	_ = c
}
