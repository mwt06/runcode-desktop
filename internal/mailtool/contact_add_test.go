package mailtool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubContactSaver struct {
	calls []struct{ name, email string }
	err   error
	hit   ContactHit
}

func (s *stubContactSaver) SaveContact(_ context.Context, name, email string) (ContactHit, error) {
	s.calls = append(s.calls, struct{ name, email string }{name, email})
	if s.err != nil {
		return ContactHit{}, s.err
	}
	if s.hit.ID == "" {
		return ContactHit{ID: "c1", Name: name, Emails: []string{email}}, nil
	}
	return s.hit, nil
}

func TestAddMailContactRejectsBadEmailWithoutSave(t *testing.T) {
	saver := &stubContactSaver{}
	tl := AddMailContactTool{Saver: saver}
	raw, _ := json.Marshal(map[string]string{"name": "马文涛", "email": "not-an-email"})
	_, err := tl.Run(context.Background(), raw, nil, nil)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "邮箱格式不正确") {
		t.Fatalf("want Chinese format error, got %v", err)
	}
	if len(saver.calls) != 0 {
		t.Fatalf("must not save on bad email, calls=%v", saver.calls)
	}
}

func TestAddMailContactSavesNormalized(t *testing.T) {
	saver := &stubContactSaver{}
	tl := AddMailContactTool{Saver: saver}
	raw, _ := json.Marshal(map[string]string{"name": "马文涛", "email": "MaWenTao@ouc-online.com.cn"})
	res, err := tl.Run(context.Background(), raw, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(saver.calls) != 1 || saver.calls[0].email != "mawentao@ouc-online.com.cn" {
		t.Fatalf("calls=%v", saver.calls)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, `"status":"saved"`) {
		t.Fatalf("result=%+v", res)
	}
}

func TestAddMailContactRequiresName(t *testing.T) {
	saver := &stubContactSaver{}
	tl := AddMailContactTool{Saver: saver}
	raw, _ := json.Marshal(map[string]string{"name": "  ", "email": "a@example.com"})
	_, err := tl.Run(context.Background(), raw, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "姓名") {
		t.Fatalf("err=%v", err)
	}
	if len(saver.calls) != 0 {
		t.Fatal("must not save")
	}
}
