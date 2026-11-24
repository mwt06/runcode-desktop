package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wt68/runcode/internal/mail"
	"github.com/wt68/runcode/internal/protocol"
)

type mockMailSMTP struct {
	loginErr   error
	sendStatus string
	sendDetail string
	sendErr    error
	logins     atomic.Int32
	sends      atomic.Int32
	lastUser   string
	lastToHint string // unused; ensure no external recipient API
}

func (m *mockMailSMTP) TestLogin(_ context.Context, _ string, _ int, username, _ string) error {
	m.logins.Add(1)
	m.lastUser = username
	return m.loginErr
}

func (m *mockMailSMTP) SendSelfTest(_ context.Context, _ string, _ int, username, _, _, _ string) (string, string, error) {
	m.sends.Add(1)
	m.lastUser = username
	m.lastToHint = username
	if m.sendStatus == "" {
		m.sendStatus = "accepted"
	}
	return m.sendStatus, m.sendDetail, m.sendErr
}

func (m *mockMailSMTP) SendMessage(_ context.Context, _ string, _ int, username, _ string, msg mail.OutboundMessage) (mailDeliveryResult, error) {
	m.sends.Add(1)
	m.lastUser = username
	if len(msg.To) > 0 {
		m.lastToHint = msg.To[0]
	}
	if m.sendStatus == "" {
		m.sendStatus = "accepted"
	}
	return mailDeliveryResult{Status: m.sendStatus, Detail: m.sendDetail, MessageID: msg.MessageID}, m.sendErr
}

func withMockMailSMTP(t *testing.T, m mailSMTPClient) {
	t.Helper()
	prev := mailSMTP
	mailSMTP = m
	t.Cleanup(func() { mailSMTP = prev; resetMailTestCachesForTest() })
	resetMailTestCachesForTest()
}

func TestMailSettingsProtectFailureDoesNotWrite(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	withMockMailSMTP(t, &mockMailSMTP{})

	prev := mailProtectSecret
	mailProtectSecret = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { mailProtectSecret = prev })

	fp := mustMailTestOK(t, app, "a@example.com", "secret")
	_, err := app.SaveMailAccount(protocol.SaveMailAccountRequest{
		Address: "a@example.com", DisplayName: "A", Version: protocol.MailVersionStandard,
		Password: "secret", ConfigFingerprint: fp, ExpectedRevision: 0, Enabled: true,
	})
	if err == nil {
		t.Fatal("want protect failure")
	}
	if got := loadRawConfig().MailAccount; got != nil {
		t.Fatalf("config written despite protect failure: %+v", got)
	}
}

func TestMailSettingsBlankPasswordKeepsCiphertext(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	withMockMailSMTP(t, &mockMailSMTP{})

	prevP, prevU := mailProtectSecret, mailUnprotectSecret
	mailProtectSecret = func(s string) (string, bool) { return "enc:" + s, true }
	mailUnprotectSecret = func(s string) (string, bool) {
		if strings.HasPrefix(s, "enc:") {
			return strings.TrimPrefix(s, "enc:"), true
		}
		return "", false
	}
	t.Cleanup(func() { mailProtectSecret = prevP; mailUnprotectSecret = prevU })

	fp := mustMailTestOK(t, app, "a@example.com", "secret")
	view, err := app.SaveMailAccount(protocol.SaveMailAccountRequest{
		Address: "a@example.com", DisplayName: "A", Version: protocol.MailVersionStandard,
		Password: "secret", ConfigFingerprint: fp, ExpectedRevision: 0, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Account == nil || !view.Account.HasPassword || view.Account.Revision != 1 {
		t.Fatalf("%+v", view.Account)
	}

	fp2 := mustMailTestOK(t, app, "a@example.com", "secret")
	view, err = app.SaveMailAccount(protocol.SaveMailAccountRequest{
		Address: "a@example.com", DisplayName: "A2", Version: protocol.MailVersionStandard,
		Password: "", ConfigFingerprint: fp2, ExpectedRevision: view.Account.Revision, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := loadRawConfig().MailAccount
	if raw == nil || raw.PasswordProtected != "enc:secret" || raw.DisplayName != "A2" {
		t.Fatalf("%+v", raw)
	}
	if view.Account.HasPassword != true {
		t.Fatal("hasPassword lost")
	}
}

func TestMailSettingsViewNeverReturnsPassword(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	withMockMailSMTP(t, &mockMailSMTP{})
	prevP := mailProtectSecret
	mailProtectSecret = func(s string) (string, bool) { return "enc:" + s, true }
	t.Cleanup(func() { mailProtectSecret = prevP })

	fp := mustMailTestOK(t, app, "a@example.com", "secret")
	_, err := app.SaveMailAccount(protocol.SaveMailAccountRequest{
		Address: "a@example.com", Version: protocol.MailVersionStandard,
		Password: "secret", ConfigFingerprint: fp, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := app.GetMailSettings()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(settings)
	if strings.Contains(string(b), "passwordProtected") || strings.Contains(string(b), `"password":`) || strings.Contains(string(b), "enc:secret") {
		t.Fatalf("leaked secret fields: %s", b)
	}
	path, err := desktopConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"password":`) || !strings.Contains(string(data), `"passwordProtected"`) {
		t.Fatalf("expected only protected password field: %s", data)
	}
}

func TestMailConnectionDoesNotSendDATA(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	m := &mockMailSMTP{}
	withMockMailSMTP(t, m)
	res, err := app.TestMailConnection(protocol.TestMailConnectionRequest{
		Address: "a@example.com", Password: "x", Version: protocol.MailVersionStandard,
	})
	if err != nil || !res.OK {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if m.logins.Load() != 1 || m.sends.Load() != 0 {
		t.Fatalf("logins=%d sends=%d", m.logins.Load(), m.sends.Load())
	}
}

func TestMailConfigChangeInvalidatesOldTest(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	withMockMailSMTP(t, &mockMailSMTP{})
	prevP := mailProtectSecret
	mailProtectSecret = func(s string) (string, bool) { return "enc:" + s, true }
	t.Cleanup(func() { mailProtectSecret = prevP })

	fp := mustMailTestOK(t, app, "a@example.com", "old")
	_, err := app.SaveMailAccount(protocol.SaveMailAccountRequest{
		Address: "a@example.com", Version: protocol.MailVersionStandard,
		Password: "new", ConfigFingerprint: fp, Enabled: true,
	})
	if err == nil {
		t.Fatal("want fingerprint mismatch for changed password")
	}
}

func TestMailSelfTestRejectsExternalRecipientInjection(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	m := &mockMailSMTP{}
	withMockMailSMTP(t, m)
	prevP, prevU := mailProtectSecret, mailUnprotectSecret
	mailProtectSecret = func(s string) (string, bool) { return "enc:" + s, true }
	mailUnprotectSecret = func(s string) (string, bool) { return strings.TrimPrefix(s, "enc:"), true }
	t.Cleanup(func() { mailProtectSecret = prevP; mailUnprotectSecret = prevU })

	fp := mustMailTestOK(t, app, "me@example.com", "secret")
	view, err := app.SaveMailAccount(protocol.SaveMailAccountRequest{
		Address: "me@example.com", Version: protocol.MailVersionStandard,
		Password: "secret", ConfigFingerprint: fp, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 协议无收件人字段；发送只打已保存地址。
	res, err := app.SendMailTestToSelf(protocol.SendMailTestToSelfRequest{ExpectedRevision: view.Account.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "accepted" || res.ShortID == "" {
		t.Fatalf("%+v", res)
	}
	if m.lastToHint != "me@example.com" {
		t.Fatalf("sent to %q", m.lastToHint)
	}
}

func TestMailSelfTestIdempotentClientRequest(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	m := &mockMailSMTP{}
	withMockMailSMTP(t, m)
	prevP, prevU := mailProtectSecret, mailUnprotectSecret
	mailProtectSecret = func(s string) (string, bool) { return "enc:" + s, true }
	mailUnprotectSecret = func(s string) (string, bool) { return strings.TrimPrefix(s, "enc:"), true }
	t.Cleanup(func() { mailProtectSecret = prevP; mailUnprotectSecret = prevU })

	fp := mustMailTestOK(t, app, "me@example.com", "secret")
	view, err := app.SaveMailAccount(protocol.SaveMailAccountRequest{
		Address: "me@example.com", Version: protocol.MailVersionStandard,
		Password: "secret", ConfigFingerprint: fp, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.SendMailTestToSelf(protocol.SendMailTestToSelfRequest{
		ExpectedRevision: view.Account.Revision, ClientRequestID: "req-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// revision bumped after send — second call with same client id returns cached without new SMTP
	b, err := app.SendMailTestToSelf(protocol.SendMailTestToSelfRequest{
		ExpectedRevision: view.Account.Revision, ClientRequestID: "req-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.SubmissionID != b.SubmissionID || m.sends.Load() != 1 {
		t.Fatalf("a=%+v b=%+v sends=%d", a, b, m.sends.Load())
	}
}

func TestMailUnknownDoesNotAutoRetry(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	m := &mockMailSMTP{sendStatus: "unknown", sendErr: errors.New("data close lost")}
	withMockMailSMTP(t, m)
	prevP, prevU := mailProtectSecret, mailUnprotectSecret
	mailProtectSecret = func(s string) (string, bool) { return "enc:" + s, true }
	mailUnprotectSecret = func(s string) (string, bool) { return strings.TrimPrefix(s, "enc:"), true }
	t.Cleanup(func() { mailProtectSecret = prevP; mailUnprotectSecret = prevU })

	fp := mustMailTestOK(t, app, "me@example.com", "secret")
	view, err := app.SaveMailAccount(protocol.SaveMailAccountRequest{
		Address: "me@example.com", Version: protocol.MailVersionStandard,
		Password: "secret", ConfigFingerprint: fp, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := app.SendMailTestToSelf(protocol.SendMailTestToSelfRequest{ExpectedRevision: view.Account.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "unknown" {
		t.Fatalf("%+v", res)
	}
	settings, _ := app.GetMailSettings()
	if settings.Account.OnboardingStatus != protocol.MailStatusNeedsAttention {
		t.Fatalf("%s", settings.Account.OnboardingStatus)
	}
}

func TestMailRefuseWhenExiting(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	app.exit.closing.Store(true)
	t.Cleanup(func() { app.exit.closing.Store(false) })
	_, err := app.TestMailConnection(protocol.TestMailConnectionRequest{Address: "a@example.com", Password: "x"})
	if err == nil {
		t.Fatal("want exit refusal")
	}
}

func TestOpenMailOfficialURLAllowlist(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	var opened string
	prev := mailOpenBrowser
	mailOpenBrowser = func(url string) error { opened = url; return nil }
	t.Cleanup(func() { mailOpenBrowser = prev })

	if err := app.OpenMailOfficialURL(protocol.OpenMailOfficialURLRequest{Page: "https://evil.example"}); err == nil {
		t.Fatal("want reject")
	}
	if err := app.OpenMailOfficialURL(protocol.OpenMailOfficialURLRequest{Page: "help_security_password"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(opened, "aliyun.com") {
		t.Fatalf("opened %q", opened)
	}
}

func TestLoadConfigDoesNotExposeMailPassword(t *testing.T) {
	isolateConfigDir(t)
	if err := updateRawConfig(func(cfg *desktopConfig) error {
		cfg.MailAccount = &mailAccountRecord{
			ID: "m1", Address: "a@example.com", PasswordProtected: "opaque", Revision: 1,
			OnboardingStatus: protocol.MailStatusSaved,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := New(&recordingSink{})
	view := app.LoadConfig()
	b, _ := json.Marshal(view)
	if strings.Contains(string(b), "opaque") || strings.Contains(strings.ToLower(string(b)), "mail") {
		t.Fatalf("LoadConfig leaked mail: %s", b)
	}
}

func mustMailTestOK(t *testing.T, app *App, address, password string) string {
	t.Helper()
	res, err := app.TestMailConnection(protocol.TestMailConnectionRequest{
		Address: address, Password: password, Version: protocol.MailVersionStandard,
	})
	if err != nil || !res.OK || res.ConfigFingerprint == "" {
		t.Fatalf("test: %+v err=%v", res, err)
	}
	return res.ConfigFingerprint
}
