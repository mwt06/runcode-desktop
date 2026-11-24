package mail

import (
	"strings"
	"testing"
	"time"
)

func baseDraft() Draft {
	now := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	return Draft{
		ID:         "d1",
		OwnerScope: "owner-a",
		AccountID:  "acc1",
		Revision:   1,
		Status:     StatusDraft,
		Source: DraftSource{
			SessionID:    "s1",
			TurnID:       "t1",
			ToolUseID:    "tool1",
			ProposalMD:   "模型原文 **hello**",
			CreatedAtUTC: now,
		},
		Subject:      "主题",
		BodyMarkdown: "正文",
		To:           []MailAddress{{Address: "a@example.com"}},
		SendMode:     ModeTogether,
		DispatchMode: ModeImmediate,
		CreatedAtUTC: now,
		UpdatedAtUTC: now,
	}
}

func TestApplyDraftEditIncrementsRevisionAndKeepsProposal(t *testing.T) {
	d := baseDraft()
	edit := DraftEdit{
		AccountID:    "acc1",
		Subject:      "新主题",
		BodyMarkdown: "用户改过的正文",
		To:           []MailAddress{{Name: "A", Address: "a@example.com"}},
		SendMode:     ModeTogether,
		DispatchMode: ModeImmediate,
	}
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	out, err := ApplyDraftEdit(d, edit, now)
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision != 2 {
		t.Fatalf("revision=%d want 2", out.Revision)
	}
	if out.BodyMarkdown != "用户改过的正文" {
		t.Fatalf("body=%q", out.BodyMarkdown)
	}
	if out.Source.ProposalMD != "模型原文 **hello**" {
		t.Fatalf("proposal overwritten: %q", out.Source.ProposalMD)
	}
}

func TestConfirmationInvalidatedAfterEdit(t *testing.T) {
	d := baseDraft()
	c := Confirmation{
		ID:            "c1",
		OwnerScope:    d.OwnerScope,
		DraftID:       d.ID,
		DraftRevision: d.Revision,
		ExpiresAtUTC:  time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC),
	}
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	if err := ConfirmationStillValid(c, d, now); err != nil {
		t.Fatalf("expected valid: %v", err)
	}
	edit := DraftEdit{
		AccountID: "acc1", Subject: "x", BodyMarkdown: "y",
		To:       []MailAddress{{Address: "a@example.com"}},
		SendMode: ModeTogether, DispatchMode: ModeImmediate,
	}
	d2, err := ApplyDraftEdit(d, edit, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfirmationStillValid(c, d2, now); err == nil {
		t.Fatal("expected confirmation invalid after revision bump")
	}
}

func TestIndividualForbidsCcBcc(t *testing.T) {
	edit := DraftEdit{
		AccountID: "acc1", Subject: "s", BodyMarkdown: "b",
		To:       []MailAddress{{Address: "a@example.com"}},
		Cc:       []MailAddress{{Address: "c@example.com"}},
		SendMode: ModeIndividual, DispatchMode: ModeImmediate,
	}
	if err := ValidateDraftEdit(edit); err == nil {
		t.Fatal("expected error")
	}
}

func TestRejectNativeHTMLAndHeaderInjection(t *testing.T) {
	if err := RejectNativeHTML("hello <script>x</script>"); err == nil {
		t.Fatal("html")
	}
	if _, err := NormalizeAddress("Evil\nBcc: x", "a@example.com"); err == nil {
		t.Fatal("name inject")
	}
	if _, err := NormalizeAddress("", "a@example.com\r\nBcc: b@evil.com"); err == nil {
		t.Fatal("addr inject")
	}
	edit := DraftEdit{
		AccountID: "acc1", Subject: "ok", BodyMarkdown: "<b>hi</b>",
		To:       []MailAddress{{Address: "a@example.com"}},
		SendMode: ModeTogether, DispatchMode: ModeImmediate,
	}
	if err := ValidateDraftEdit(edit); err == nil {
		t.Fatal("html body")
	}
}

func TestDedupeAddressesKeepsFirst(t *testing.T) {
	in := []MailAddress{
		{Name: "A", Address: "a@Example.com"},
		{Name: "A2", Address: "a@example.com"},
		{Name: "B", Address: "b@example.com"},
	}
	out := DedupeAddresses(in)
	if len(out) != 2 || out[0].Name != "A" || out[0].Address != "a@example.com" {
		t.Fatalf("%+v", out)
	}
}

func TestAmbiguousContactNameNotGuessed(t *testing.T) {
	cands := []Contact{
		{ID: "1", Name: "张三", Emails: []string{"a@x.com"}},
		{ID: "2", Name: "张三", Emails: []string{"b@x.com"}},
	}
	if _, err := ResolveAmbiguousContacts("张三", cands); err == nil {
		t.Fatal("expected ambiguous")
	}
	one, err := ResolveAmbiguousContacts("张三", cands[:1])
	if err != nil || one.ID != "1" {
		t.Fatalf("%+v %v", one, err)
	}
}

func TestToolResultIsDraftReadyNotSent(t *testing.T) {
	m := ToolResultDraftReady("d1", 3, false)
	if m["status"] != "draft_ready" {
		t.Fatalf("%v", m)
	}
	if _, ok := m["needsMailSetup"]; ok {
		t.Fatalf("needsMailSetup must be omitted when false: %v", m)
	}
	if strings.Contains(strings.ToLower(m["status"].(string)), "accept") {
		t.Fatal("must not look like SMTP accepted")
	}
	setup := ToolResultDraftReady("d2", 1, true)
	if setup["needsMailSetup"] != true {
		t.Fatalf("want needsMailSetup: %v", setup)
	}
}
