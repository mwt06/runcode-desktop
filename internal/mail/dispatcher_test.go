package mail

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatcherLateWindowBoundaries(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		offset time.Duration
		want   string
	}{
		{"plus9m59", 9*time.Minute + 59*time.Second, StatusAccepted},
		{"plus10m00", 10 * time.Minute, StatusAccepted},
		{"plus10m01", 10*time.Minute + time.Second, StatusMissed},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := "item-" + tc.name
			subID := "sub-" + tc.name
			nowCreate := base
			_, err := s.CreateSubmission(ctx, Submission{
				ID: subID, OwnerScope: "owner-a", DraftID: "d", DraftRevision: 1,
				SnapshotID: "snap-" + tc.name, ConfirmationID: "c", Status: StatusScheduled,
				CreatedAtUTC: nowCreate, UpdatedAtUTC: nowCreate,
			}, []SubmissionItem{{
				ID: id, Recipient: MailAddress{Address: "a@example.com"},
				Status: StatusScheduled, ScheduledAtUTC: &base,
			}})
			if err != nil {
				t.Fatal(err)
			}
			_ = s.SavePreparedSnapshot(ctx, PreparedSnapshot{
				ID: "snap-" + tc.name, DraftID: "d", DraftRevision: 1, OwnerScope: "owner-a",
				AccountID: "acc", Subject: "s", BodyMarkdown: "b",
				ResolvedTo: []MailAddress{{Address: "a@example.com"}},
				SendMode: ModeTogether, DispatchMode: ModeScheduledAt, Digest: "d", CreatedAtUTC: nowCreate,
			})
			clock := newFakeClock(base.Add(tc.offset))
			var delivered atomic.Int32
			d := NewDispatcher(s, clock, func(context.Context, SubmissionItem, PreparedSnapshot, Submission) (string, string, error) {
				delivered.Add(1)
				return StatusAccepted, "", nil
			}, nil)
			if err := d.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer d.Stop()
			d.Wake()
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				it, err := s.LoadItem(ctx, id, []string{"owner-a"})
				if err == nil && (it.Status == StatusAccepted || it.Status == StatusMissed || it.Status == StatusFailed) {
					if it.Status != tc.want {
						t.Fatalf("status=%s want %s delivered=%d", it.Status, tc.want, delivered.Load())
					}
					if tc.want == StatusAccepted && delivered.Load() != 1 {
						t.Fatalf("delivered=%d", delivered.Load())
					}
					if tc.want == StatusMissed && delivered.Load() != 0 {
						t.Fatalf("should not deliver missed")
					}
					return
				}
				time.Sleep(20 * time.Millisecond)
				d.Wake()
			}
			t.Fatalf("timeout case %d", i)
		})
	}
}

func TestDispatcherUnknownNotRedelivered(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := s.CreateSubmission(ctx, Submission{
		ID: "sub1", OwnerScope: "owner-a", DraftID: "d", DraftRevision: 1,
		SnapshotID: "snap1", ConfirmationID: "c", Status: StatusQueued,
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}, []SubmissionItem{{
		ID: "item1", Recipient: MailAddress{Address: "a@example.com"}, Status: StatusQueued,
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SavePreparedSnapshot(ctx, PreparedSnapshot{
		ID: "snap1", DraftID: "d", DraftRevision: 1, OwnerScope: "owner-a", AccountID: "acc",
		Subject: "s", BodyMarkdown: "b", ResolvedTo: []MailAddress{{Address: "a@example.com"}},
		SendMode: ModeTogether, DispatchMode: ModeImmediate, Digest: "d", CreatedAtUTC: now,
	})
	var n atomic.Int32
	d := NewDispatcher(s, SystemClock{}, func(context.Context, SubmissionItem, PreparedSnapshot, Submission) (string, string, error) {
		n.Add(1)
		return StatusUnknown, "data lost", errors.New("lost")
	}, nil)
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	d.Wake()
	waitStatus(t, s, "item1", StatusUnknown)
	d.Wake()
	time.Sleep(50 * time.Millisecond)
	if n.Load() != 1 {
		t.Fatalf("redelivered n=%d", n.Load())
	}
}

func TestDispatcherAccountDisabled(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := s.CreateSubmission(ctx, Submission{
		ID: "sub1", OwnerScope: "owner-a", DraftID: "d", DraftRevision: 1,
		SnapshotID: "snap1", ConfirmationID: "c", Status: StatusQueued,
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}, []SubmissionItem{{
		ID: "item1", Recipient: MailAddress{Address: "a@example.com"}, Status: StatusQueued,
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SavePreparedSnapshot(ctx, PreparedSnapshot{
		ID: "snap1", DraftID: "d", DraftRevision: 1, OwnerScope: "owner-a", AccountID: "acc",
		Subject: "s", BodyMarkdown: "b", ResolvedTo: []MailAddress{{Address: "a@example.com"}},
		SendMode: ModeTogether, DispatchMode: ModeImmediate, Digest: "d", CreatedAtUTC: now,
	})
	d := NewDispatcher(s, SystemClock{}, func(context.Context, SubmissionItem, PreparedSnapshot, Submission) (string, string, error) {
		t.Fatal("must not deliver")
		return StatusAccepted, "", nil
	}, func(context.Context, string) (bool, string) { return false, "account_disabled" })
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	d.Wake()
	waitStatus(t, s, "item1", StatusFailed)
}

func TestDispatcherRecoverIndependent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.sqlite3")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	_, err = s.CreateSubmission(ctx, Submission{
		ID: "sub1", OwnerScope: "owner-a", DraftID: "d", DraftRevision: 1,
		SnapshotID: "snap1", ConfirmationID: "c", Status: StatusQueued,
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}, []SubmissionItem{{
		ID: "item1", Recipient: MailAddress{Address: "a@example.com"}, Status: StatusQueued,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDueItem(ctx, now); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	d := NewDispatcher(s2, SystemClock{}, func(context.Context, SubmissionItem, PreparedSnapshot, Submission) (string, string, error) {
		t.Fatal("unknown must not redeliver")
		return StatusAccepted, "", nil
	}, nil)
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	waitStatus(t, s2, "item1", StatusUnknown)
}

func waitStatus(t *testing.T, s *Store, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		it, err := s.LoadItem(context.Background(), id, []string{"owner-a"})
		if err == nil && it.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	it, _ := s.LoadItem(context.Background(), id, []string{"owner-a"})
	t.Fatalf("status=%s want %s", it.Status, want)
}
