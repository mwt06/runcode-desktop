package mail

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStoreContactCRUDAndDuplicateEmail(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	c, err := s.SaveContact(ctx, Contact{OwnerScope: "owner-a", Name: "王老师", Emails: []string{"Wang@Example.com"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Emails[0] != "wang@example.com" || c.Revision != 1 {
		t.Fatalf("%+v", c)
	}
	_, err = s.SaveContact(ctx, Contact{OwnerScope: "owner-a", Name: "另一王", Emails: []string{"wang@example.com"}}, 0)
	if err == nil {
		t.Fatal("want duplicate email rejected")
	}
	// 同名不同邮箱允许
	c2, err := s.SaveContact(ctx, Contact{OwnerScope: "owner-a", Name: "王老师", Emails: []string{"wang2@example.com"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListContacts(ctx, []string{"owner-a"}, "王", 50, 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	c.Name = "王老师改"
	next, err := s.SaveContact(ctx, c, c.Revision)
	if err != nil || next.Revision != 2 {
		t.Fatalf("%+v %v", next, err)
	}
	if _, err := s.SaveContact(ctx, c, c.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict got %v", err)
	}
	_ = c2
}

func TestStoreContactImportSkipsDuplicates(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	_, err := s.SaveContact(ctx, Contact{OwnerScope: "o", Name: "已有", Emails: []string{"a@x.com"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	prev, err := s.PreviewContactImport(ctx, "o", []ContactImportRow{
		{Name: "新", Emails: []string{"b@x.com"}},
		{Name: "重", Emails: []string{"a@x.com"}},
		{Name: "", Emails: []string{"c@x.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prev.Accept) != 1 || len(prev.Skip) != 2 {
		t.Fatalf("%+v", prev)
	}
	created, err := s.CommitContactImport(ctx, "o", []ContactImportRow{
		{Name: "新", Emails: []string{"b@x.com"}},
		{Name: "重", Emails: []string{"a@x.com"}},
	})
	if err != nil || len(created) != 1 {
		t.Fatalf("%+v %v", created, err)
	}
}

func TestStoreGroupAndDraftUserCreate(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	c, err := s.SaveContact(ctx, Contact{OwnerScope: "o", Name: "A", Emails: []string{"a@x.com"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.SaveGroup(ctx, Group{OwnerScope: "o", Name: "教研组", MemberIDs: []string{c.ID}}, 0)
	if err != nil || g.Revision != 1 {
		t.Fatalf("%+v %v", g, err)
	}
	d, err := s.CreateUserDraft(ctx, "o", "acc1")
	if err != nil || d.Revision != 1 || d.Status != StatusDraft {
		t.Fatalf("%+v %v", d, err)
	}
	edit := DraftEdit{
		AccountID: "acc1", Subject: "主题", BodyMarkdown: "正文",
		To: []MailAddress{{Address: "a@x.com"}}, SendMode: ModeTogether, DispatchMode: ModeImmediate,
	}
	next, err := ApplyDraftEdit(d, edit, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveDraft(ctx, next, d.Revision)
	if err != nil || saved.Revision != 2 {
		t.Fatalf("%+v %v", saved, err)
	}
	// 冲突不覆盖
	next.Subject = "冲突"
	if _, err := s.SaveDraft(ctx, next, d.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict %v", err)
	}
	list, err := s.ListDrafts(ctx, []string{"o"}, 50, 0)
	if err != nil || len(list) != 1 || list[0].Subject != "主题" {
		t.Fatalf("%+v %v", list, err)
	}
	if err := s.DeleteContact(ctx, c.ID, []string{"o"}, c.Revision); err != nil {
		t.Fatal(err)
	}
	groups, _ := s.ListGroups(ctx, []string{"o"}, 50, 0)
	if len(groups) != 1 || len(groups[0].MemberIDs) != 0 {
		t.Fatalf("member not removed: %+v", groups)
	}
}

func TestStoreForeignOwnerHiddenContacts(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	c, err := s.SaveContact(ctx, Contact{OwnerScope: "a", Name: "A", Emails: []string{"a@x.com"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetContact(ctx, c.ID, []string{"b"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}
