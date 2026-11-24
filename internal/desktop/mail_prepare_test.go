package desktop

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/mail"
	"github.com/wt68/runcode/internal/protocol"
)

func TestPrepareConfirmRejectsStaleRevisionAndReuse(t *testing.T) {
	isolateConfigDir(t)
	resetMailStoreForTest()
	t.Cleanup(resetMailStoreForTest)
	db := filepath.Join(t.TempDir(), "mail.sqlite3")
	openMailStore = func(string) (*mail.Store, error) { return mail.OpenStore(db) }
	t.Cleanup(func() {
		openMailStore = mail.OpenStore
	})
	mock := &mockMailSMTP{sendStatus: mail.StatusAccepted}
	withMockMailSMTP(t, mock)
	prevU := mailUnprotectSecret
	mailUnprotectSecret = func(string) (string, bool) { return "pw", true }
	t.Cleanup(func() { mailUnprotectSecret = prevU })

	if err := updateRawConfig(func(cfg *desktopConfig) error {
		cfg.MailAccount = &mailAccountRecord{
			ID: "acc1", Address: "me@ex.com", Enabled: true,
			PasswordProtected: "opaque", Host: "smtp.example.com", Port: 465,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	t.Cleanup(func() {
		if d := app.mailDispatcher(); d != nil {
			d.Stop()
		}
		app.setMailUnavailable("test done")
		resetMailStoreForTest()
	})
	st, err := app.mailStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	d, err := st.CreateUserDraft(ctx, mailOwnerLocal, "acc1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	edited, err := mail.ApplyDraftEdit(d, mail.DraftEdit{
		AccountID: "acc1", Subject: "主题", BodyMarkdown: "正文",
		To:       []mail.MailAddress{{Address: "to@ex.com"}},
		SendMode: mail.ModeTogether, DispatchMode: mail.ModeImmediate,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := st.SaveDraft(ctx, edited, d.Revision)
	if err != nil {
		t.Fatal(err)
	}

	_, err = app.PrepareMailSend(protocol.PrepareMailSendRequest{DraftID: saved.ID, ExpectedRevision: saved.Revision - 1})
	if err == nil {
		t.Fatal("stale revision must fail")
	}

	prep, err := app.PrepareMailSend(protocol.PrepareMailSendRequest{DraftID: saved.ID, ExpectedRevision: saved.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if prep.ConfirmationID == "" || prep.Preview.Digest == "" || prep.Preview.Revision != saved.Revision {
		t.Fatalf("prep=%+v", prep)
	}

	res, err := app.ConfirmMailSend(protocol.ConfirmMailSendRequest{ConfirmationID: prep.ConfirmationID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != mail.StatusAccepted || res.SubmissionID == "" {
		t.Fatalf("immediate confirm must sync-deliver, res=%+v", res)
	}
	if mock.sends.Load() < 1 {
		t.Fatal("confirm must SMTP like self-test")
	}
	outbox, err := app.ListMailOutbox(protocol.ListMailOutboxRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) == 0 || outbox[0].Status != mail.StatusAccepted {
		t.Fatalf("outbox=%+v", outbox)
	}
	_, err = app.ConfirmMailSend(protocol.ConfirmMailSendRequest{ConfirmationID: prep.ConfirmationID})
	if err == nil {
		t.Fatal("reuse must fail")
	}
}
