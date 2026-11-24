package mail

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "mail.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleDraft(id string) Draft {
	now := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	return Draft{
		ID: id, OwnerScope: "owner-a", AccountID: "acc1", Revision: 1, Status: StatusDraft,
		Source:  DraftSource{SessionID: "s1", TurnID: "t1", ToolUseID: "tool-1", ProposalMD: "提案", CreatedAtUTC: now},
		Subject: "主题", BodyMarkdown: "正文",
		To:       []MailAddress{{Address: "a@example.com"}},
		SendMode: ModeTogether, DispatchMode: ModeImmediate,
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}
}

func TestStoreReopenKeepsDraft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.sqlite3")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d, err := s1.CreateDraftFromProposal(ctx, sampleDraft("d1"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.GetDraft(ctx, d.ID, []string{"owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "主题" || got.Source.ProposalMD != "提案" {
		t.Fatalf("%+v", got)
	}
}

func TestStoreProposalIdempotent(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	d1, err := s.CreateDraftFromProposal(ctx, sampleDraft("d1"))
	if err != nil {
		t.Fatal(err)
	}
	d2 := sampleDraft("d-other")
	d2.Subject = "第二次"
	got, err := s.CreateDraftFromProposal(ctx, d2)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != d1.ID || got.Subject != d1.Subject {
		t.Fatalf("idempotent failed: %+v vs %+v", got, d1)
	}
}

func TestStoreRevisionCAS(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	d, err := s.CreateDraftFromProposal(ctx, sampleDraft("d1"))
	if err != nil {
		t.Fatal(err)
	}
	edit := DraftEdit{
		AccountID: "acc1", Subject: "改", BodyMarkdown: "新正文",
		To:       []MailAddress{{Address: "a@example.com"}},
		SendMode: ModeTogether, DispatchMode: ModeImmediate,
	}
	next, err := ApplyDraftEdit(d, edit, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDraft(ctx, next, d.Revision); err != nil {
		t.Fatal(err)
	}
	next.Subject = "冲突"
	if _, err := s.SaveDraft(ctx, next, d.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict got %v", err)
	}
}

func TestStoreForeignOwnerHidden(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	d, err := s.CreateDraftFromProposal(ctx, sampleDraft("d1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDraft(ctx, d.ID, []string{"owner-b"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestStoreDeleteDraft(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	d, err := s.CreateDraftFromProposal(ctx, sampleDraft("d-del"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDraft(ctx, d.ID, []string{"owner-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDraft(ctx, d.ID, []string{"owner-a"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want gone, got %v", err)
	}
}

func TestStoreSubmissionIdempotent(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sub := Submission{
		ID: "sub1", OwnerScope: "owner-a", DraftID: "d1", DraftRevision: 1,
		SnapshotID: "snap1", ConfirmationID: "c1", Status: StatusQueued,
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}
	items := []SubmissionItem{{
		ID: "item1", Recipient: MailAddress{Address: "a@example.com"}, Status: StatusQueued,
	}}
	a, err := s.CreateSubmission(ctx, sub, items)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateSubmission(ctx, sub, items)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatal("not idempotent")
	}
}

func TestStoreCancelVersusClaimRace(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sub := Submission{
		ID: "sub1", OwnerScope: "owner-a", DraftID: "d1", DraftRevision: 1,
		SnapshotID: "snap1", ConfirmationID: "c1", Status: StatusQueued,
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}
	sched := now.Add(-time.Minute)
	_, err := s.CreateSubmission(ctx, sub, []SubmissionItem{{
		ID: "item1", Recipient: MailAddress{Address: "a@example.com"},
		Status: StatusQueued, ScheduledAtUTC: &sched,
	}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimDueItem(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != StatusSending {
		t.Fatalf("%s", claimed.Status)
	}
	_, err = s.CancelItem(ctx, "item1", []string{"owner-a"}, now)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict after claim, got %v", err)
	}
}

func TestStoreQuotaCap300(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	owner, acc, day := "owner-a", "acc1", "2026-11-20"
	if err := s.ReserveQuota(ctx, owner, acc, day, "pre", 299, DailyQuotaDefault); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.ReserveQuota(ctx, owner, acc, day, fmt.Sprintf("item-%d", i), 1, DailyQuotaDefault)
		}(i)
	}
	wg.Wait()
	close(errs)
	ok, fail := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrQuotaExceeded):
			fail++
		default:
			t.Fatalf("unexpected %v", err)
		}
	}
	if ok != 1 || fail != 7 {
		t.Fatalf("ok=%d fail=%d", ok, fail)
	}
	used, err := s.QuotaUsed(ctx, owner, acc, day)
	if err != nil {
		t.Fatal(err)
	}
	if used != 300 {
		t.Fatalf("used=%d", used)
	}
}

func TestStoreRecoverSendingToUnknown(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := s.CreateSubmission(ctx, Submission{
		ID: "sub1", OwnerScope: "owner-a", DraftID: "d1", DraftRevision: 1,
		SnapshotID: "snap", ConfirmationID: "c", Status: StatusQueued,
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}, []SubmissionItem{{
		ID: "item1", Recipient: MailAddress{Address: "a@example.com"}, Status: StatusQueued, ScheduledAtUTC: &now,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDueItem(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(ctx, now); err != nil {
		t.Fatal(err)
	}
	it, err := s.loadItem(ctx, "item1")
	if err != nil {
		t.Fatal(err)
	}
	if it.Status != StatusUnknown {
		t.Fatalf("%s", it.Status)
	}
}

func TestStoreSchemaTooNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.sqlite3")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (99, datetime('now'))`)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_, err = OpenStore(path)
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("want schema too new, got %v", err)
	}
}

func TestStoreFinishAcceptedConsumesMonotonic(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := s.CreateSubmission(ctx, Submission{
		ID: "sub1", OwnerScope: "owner-a", DraftID: "d1", DraftRevision: 1,
		SnapshotID: "snap", ConfirmationID: "c", Status: StatusQueued,
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}, []SubmissionItem{{
		ID: "item1", Recipient: MailAddress{Address: "a@example.com"}, Status: StatusQueued, ScheduledAtUTC: &now,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDueItem(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishItem(ctx, "item1", StatusAccepted, "", now); err != nil {
		t.Fatal(err)
	}
	// late failed must not overwrite accepted
	if err := s.FinishItem(ctx, "item1", StatusFailed, "late", now); err != nil {
		t.Fatal(err)
	}
	it, err := s.loadItem(ctx, "item1")
	if err != nil {
		t.Fatal(err)
	}
	if it.Status != StatusAccepted {
		t.Fatalf("%s", it.Status)
	}
}
