package mail

import (
	"context"
	"testing"
	"time"
)

func TestListOutboxAndReconcileDraftStatus(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	d, err := s.CreateUserDraft(ctx, "owner-a", "acc")
	if err != nil {
		t.Fatal(err)
	}
	edited, err := ApplyDraftEdit(d, DraftEdit{
		AccountID: "acc", Subject: "请假", BodyMarkdown: "正文",
		To: []MailAddress{{Address: "to@ex.com"}},
		SendMode: ModeTogether, DispatchMode: ModeImmediate,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveDraft(ctx, edited, d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snap := PreparedSnapshot{
		ID: "snap1", DraftID: saved.ID, DraftRevision: saved.Revision, OwnerScope: "owner-a",
		AccountID: "acc", Subject: saved.Subject, BodyMarkdown: saved.BodyMarkdown,
		ResolvedTo: saved.To, SendMode: ModeTogether, DispatchMode: ModeImmediate,
		Digest: "dg", CreatedAtUTC: now,
	}
	if err := s.SavePreparedSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	saved.Status = StatusQueued
	if _, err := s.SaveDraft(ctx, saved, saved.Revision); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateSubmission(ctx, Submission{
		ID: "sub1", OwnerScope: "owner-a", DraftID: saved.ID, DraftRevision: saved.Revision,
		SnapshotID: snap.ID, ConfirmationID: "c1", Status: StatusQueued,
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}, []SubmissionItem{{
		ID: "item1", Recipient: MailAddress{Address: "to@ex.com"}, Status: StatusQueued,
	}})
	if err != nil {
		t.Fatal(err)
	}

	list, err := s.ListOutbox(ctx, []string{"owner-a"}, 20, 0)
	if err != nil || len(list) != 1 || list[0].Subject != "请假" {
		t.Fatalf("list=%v err=%v", list, err)
	}

	claimed, err := s.ClaimDueItem(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishItem(ctx, claimed.ID, StatusAccepted, "", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileSubmission(ctx, "sub1", now); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDraft(ctx, saved.ID, []string{"owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusAccepted {
		t.Fatalf("draft status=%s", got.Status)
	}
	list, err = s.ListOutbox(ctx, []string{"owner-a"}, 20, 0)
	if err != nil || list[0].Status != StatusAccepted {
		t.Fatalf("outbox=%v err=%v", list, err)
	}
}
