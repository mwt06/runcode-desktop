package mail

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPrepareAndConfirmOnce(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	d, err := s.CreateUserDraft(ctx, "local", "acc1")
	if err != nil {
		t.Fatal(err)
	}
	edit := DraftEdit{
		AccountID: "acc1", Subject: "主题", BodyMarkdown: "正文",
		To: []MailAddress{{Address: "a@ex.com"}}, SendMode: ModeTogether, DispatchMode: ModeImmediate,
	}
	d2, err := ApplyDraftEdit(d, edit, now)
	if err != nil {
		t.Fatal(err)
	}
	d2, err = s.SaveDraft(ctx, d2, d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := BuildPreparedSnapshot(d2, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Digest == "" || snap.ID == "" {
		t.Fatalf("snap=%+v", snap)
	}
	if err := s.SavePreparedSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	conf := NewConfirmation(snap, now, time.Minute)
	if err := s.SaveConfirmation(ctx, conf); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConfirmation(ctx, conf.ID, []string{"local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfirmationStillValid(got, d2, now); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkConfirmationUsed(ctx, conf.ID, "local", now); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkConfirmationUsed(ctx, conf.ID, "local", now); err == nil {
		t.Fatal("reuse must fail")
	}
}

func TestPrepareRejectsUnfrozenAttachment(t *testing.T) {
	d := Draft{
		ID: "d", OwnerScope: "o", AccountID: "a", Revision: 1, Status: StatusDraft,
		Subject: "s", BodyMarkdown: "b", To: []MailAddress{{Address: "a@b.c"}},
		SendMode: ModeTogether, DispatchMode: ModeImmediate,
	}
	_, err := BuildPreparedSnapshot(d, []AttachmentRef{{RelPath: "x.docx", Name: "x.docx"}}, time.Now().UTC())
	if err == nil {
		t.Fatal("want freeze error")
	}
}

func TestConfirmationInvalidOnRevisionChange(t *testing.T) {
	now := time.Now().UTC()
	d := Draft{ID: "d", OwnerScope: "o", Revision: 2}
	c := Confirmation{ID: "c", OwnerScope: "o", DraftID: "d", DraftRevision: 1, ExpiresAtUTC: now.Add(time.Minute)}
	if err := ConfirmationStillValid(c, d, now); err == nil {
		t.Fatal("revised draft must invalidate")
	}
}
