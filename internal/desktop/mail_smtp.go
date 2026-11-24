package desktop

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/mail"
)

// mailSMTPClient 是可替换的 SMTP 探测/发信接口。
type mailSMTPClient interface {
	// TestLogin 连接 → TLS → AUTH → QUIT，不发送邮件。
	TestLogin(ctx context.Context, host string, port int, username, password string) error
	// SendSelfTest 向同一地址发固定测试信；结果 accepted/failed/unknown。
	SendSelfTest(ctx context.Context, host string, port int, username, password, subject, body string) (status, detail string, err error)
	// SendMessage 按 MIME 投递；phases 不含密码。status=accepted|failed|unknown。
	SendMessage(ctx context.Context, host string, port int, username, password string, msg mail.OutboundMessage) (result mailDeliveryResult, err error)
}

// mailDeliveryResult 是结构化 SMTP 结果。
type mailDeliveryResult struct {
	Status   string
	Detail   string
	Phases   []mailDeliveryPhase
	MessageID string
}

type mailDeliveryPhase struct {
	Phase   string
	Outcome string
	Detail  string
}

// 测试可替换；生产默认走真实隐式 TLS 465。
var mailSMTP mailSMTPClient = realMailSMTP{}

// mailTLSConfig 生产用系统根证书；测试可换成专用 CA，禁止 InsecureSkipVerify 生产开关。
var mailTLSConfig = func(host string) *tls.Config {
	return &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}
}

type realMailSMTP struct{}

func (realMailSMTP) TestLogin(ctx context.Context, host string, port int, username, password string) error {
	client, err := dialSMTPTLS(ctx, host, port)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return client.Quit()
}

func (realMailSMTP) SendSelfTest(ctx context.Context, host string, port int, username, password, subject, body string) (string, string, error) {
	msg, err := mail.BuildOutboundMessage(
		mail.DisplayFrom{Address: username},
		mail.PreparedSnapshot{
			Subject: subject, BodyMarkdown: body, SendMode: mail.ModeTogether,
			ResolvedTo: []mail.MailAddress{{Address: username}},
		},
		mail.SubmissionItem{Recipient: mail.MailAddress{Address: username}},
		time.Now().UTC(),
	)
	if err != nil {
		return "failed", err.Error(), err
	}
	res, err := realMailSMTP{}.SendMessage(ctx, host, port, username, password, msg)
	return res.Status, res.Detail, err
}

func (realMailSMTP) SendMessage(ctx context.Context, host string, port int, username, password string, msg mail.OutboundMessage) (mailDeliveryResult, error) {
	res := mailDeliveryResult{MessageID: msg.MessageID, Status: "failed"}
	record := func(phase, outcome, detail string) {
		res.Phases = append(res.Phases, mailDeliveryPhase{Phase: phase, Outcome: outcome, Detail: detail})
	}

	client, err := dialSMTPTLS(ctx, host, port)
	if err != nil {
		record("connect", "failed", redactSecret(err.Error(), password))
		res.Detail = err.Error()
		return res, err
	}
	defer func() { _ = client.Close() }()
	record("connect", "ok", "")

	if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
		record("auth", "failed", redactSecret(err.Error(), password))
		res.Detail = "auth failed"
		return res, fmt.Errorf("auth: %w", err)
	}
	record("auth", "ok", "")

	from := strings.TrimSpace(msg.From)
	if from == "" {
		from = username
	}
	if err := client.Mail(from); err != nil {
		record("mail_from", "failed", err.Error())
		res.Detail = err.Error()
		return res, err
	}
	record("mail_from", "ok", "")

	for _, rcpt := range msg.To {
		if err := client.Rcpt(rcpt); err != nil {
			record("rcpt", "failed", rcpt+": "+err.Error())
			_ = client.Reset()
			record("rset", "ok", "abandon_data_after_rcpt_reject")
			res.Detail = "rcpt rejected"
			return res, err
		}
	}
	record("rcpt", "ok", fmt.Sprintf("n=%d", len(msg.To)))

	w, err := client.Data()
	if err != nil {
		record("data", "failed", err.Error())
		res.Detail = err.Error()
		return res, err
	}
	if _, err := w.Write(msg.Data); err != nil {
		_ = w.Close()
		record("data", "unknown", redactSecret(err.Error(), password))
		res.Status = "unknown"
		res.Detail = "data write interrupted"
		return res, err
	}
	if err := w.Close(); err != nil {
		// DATA 最终应答丢失：无法证明未提交，归 unknown，禁止自动重发。
		record("data", "unknown", err.Error())
		res.Status = "unknown"
		res.Detail = "data final reply lost"
		return res, err
	}
	record("data", "ok", "")

	if err := client.Quit(); err != nil {
		record("quit", "failed", err.Error())
		// 已拿到 DATA 250：QUIT 失败仍 accepted。
		res.Status = "accepted"
		res.Detail = "quit: " + err.Error()
		return res, nil
	}
	record("quit", "ok", "")
	res.Status = "accepted"
	res.Detail = ""
	return res, nil
}

func dialSMTPTLS(ctx context.Context, host string, port int) (*smtp.Client, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	d := net.Dialer{Timeout: 8 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	deadline, ok := ctx.Deadline()
	if ok {
		_ = raw.SetDeadline(deadline)
	} else {
		_ = raw.SetDeadline(time.Now().Add(30 * time.Second))
	}
	cfg := mailTLSConfig(host)
	if cfg == nil {
		cfg = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	tlsConn := tls.Client(raw, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("tls: %w", err)
	}
	client, err := smtp.NewClient(tlsConn, host)
	if err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	return client, nil
}

func classifyMailSMTPError(err error) (category, message string) {
	if err == nil {
		return "", ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "tls") || strings.Contains(lower, "certificate") || strings.Contains(lower, "x509"):
		return "tls", "安全连接失败：请检查系统时间与企业网络，不能忽略证书继续。"
	case strings.Contains(lower, "auth") || strings.Contains(lower, "535") || strings.Contains(lower, "534"):
		return "auth", "认证失败：请确认使用的是网页生成的专用安全密码，而不是网页登录密码。"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host"):
		return "network", "无法连接发信服务器：请核对邮箱版本、网络与出口 TCP 465。"
	default:
		return "unknown", "连接或发信失败，请按可能原因依次排查。"
	}
}

func redactSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}