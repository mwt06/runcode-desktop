package mail

import (
	"strings"
	"testing"
	"time"
)

func TestBuildOutboundTogetherIncludesBccInRcptNotHeader(t *testing.T) {
	msg, err := BuildOutboundMessage(
		DisplayFrom{Name: "Me", Address: "me@example.com"},
		PreparedSnapshot{
			Subject: "主题", BodyMarkdown: "hello", SendMode: ModeTogether,
			ResolvedTo:  []MailAddress{{Name: "A", Address: "a@example.com"}},
			ResolvedCc:  []MailAddress{{Address: "c@example.com"}},
			ResolvedBcc: []MailAddress{{Address: "b@example.com"}},
		},
		SubmissionItem{Recipient: MailAddress{Address: "a@example.com"}},
		time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.To) != 3 {
		t.Fatalf("rcpt=%v", msg.To)
	}
	raw := string(msg.Data)
	if strings.Contains(raw, "b@example.com") {
		t.Fatalf("bcc leaked into headers: %s", raw)
	}
	if !strings.Contains(raw, "To:") || !strings.Contains(raw, "Cc:") {
		t.Fatalf("missing to/cc: %s", raw)
	}
}

func TestBuildOutboundIndividualPersonalizes(t *testing.T) {
	msg, err := BuildOutboundMessage(
		DisplayFrom{Address: "me@example.com"},
		PreparedSnapshot{
			Subject: "Hi", BodyMarkdown: "Dear {name} <{email}>", SendMode: ModeIndividual,
			ResolvedTo: []MailAddress{{Name: "Wang", Address: "w@example.com"}},
		},
		SubmissionItem{Recipient: MailAddress{Name: "Wang", Address: "w@example.com"}},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.To) != 1 || msg.To[0] != "w@example.com" {
		t.Fatalf("%v", msg.To)
	}
	if !strings.Contains(string(msg.Data), "Dear Wang <w@example.com>") {
		t.Fatalf("%s", msg.Data)
	}
}
