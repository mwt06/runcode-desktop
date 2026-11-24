package mail

import (
	"bytes"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
)

// OutboundMessage 是已组好的 SMTP DATA 载荷（不含凭据）。
type OutboundMessage struct {
	From       string
	To         []string // RCPT 列表
	Data       []byte
	MessageID  string
	Subject    string
}

// BuildOutboundMessage 从定稿构造 MIME；individual 只发 item 收件人，together 发全部 To（Bcc 进 RCPT 不进可见头）。
func BuildOutboundMessage(from DisplayFrom, snap PreparedSnapshot, item SubmissionItem, now time.Time) (OutboundMessage, error) {
	fromAddr := strings.TrimSpace(from.Address)
	if fromAddr == "" {
		return OutboundMessage{}, fmt.Errorf("%w: empty from", ErrInvalid)
	}
	var rcpt []string
	var toHeader []string
	var ccHeader []string
	if snap.SendMode == ModeIndividual {
		addr := strings.TrimSpace(item.Recipient.Address)
		if addr == "" {
			return OutboundMessage{}, fmt.Errorf("%w: empty recipient", ErrInvalid)
		}
		rcpt = []string{addr}
		toHeader = []string{formatAddress(item.Recipient)}
	} else {
		for _, a := range snap.ResolvedTo {
			rcpt = append(rcpt, a.Address)
			toHeader = append(toHeader, formatAddress(a))
		}
		for _, a := range snap.ResolvedCc {
			rcpt = append(rcpt, a.Address)
			ccHeader = append(ccHeader, formatAddress(a))
		}
		for _, a := range snap.ResolvedBcc {
			rcpt = append(rcpt, a.Address)
		}
	}
	if len(rcpt) == 0 {
		return OutboundMessage{}, fmt.Errorf("%w: no recipients", ErrInvalid)
	}
	msgID := fmt.Sprintf("<%s.%d@runcode.local>", shortID(12), now.UnixNano())
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", formatMailbox(from.Name, fromAddr))
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(toHeader, ", "))
	if len(ccHeader) > 0 {
		fmt.Fprintf(&buf, "Cc: %s\r\n", strings.Join(ccHeader, ", "))
	}
	fmt.Fprintf(&buf, "Subject: %s\r\n", encodeHeader(snap.Subject))
	fmt.Fprintf(&buf, "Message-ID: %s\r\n", msgID)
	fmt.Fprintf(&buf, "Date: %s\r\n", now.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=UTF-8\r\n")
	fmt.Fprintf(&buf, "Content-Transfer-Encoding: 8bit\r\n\r\n")
	body := snap.BodyMarkdown
	if snap.SendMode == ModeIndividual {
		body = expandPersonalization(body, item.Recipient)
	}
	buf.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	if !strings.HasSuffix(body, "\n") {
		buf.WriteString("\r\n")
	}
	return OutboundMessage{
		From: fromAddr, To: rcpt, Data: buf.Bytes(), MessageID: msgID, Subject: snap.Subject,
	}, nil
}

// DisplayFrom 是发件人展示信息。
type DisplayFrom struct {
	Name    string
	Address string
}

func formatAddress(a MailAddress) string {
	return formatMailbox(a.Name, a.Address)
}

func formatMailbox(name, address string) string {
	address = strings.TrimSpace(address)
	name = strings.TrimSpace(name)
	if name == "" {
		return address
	}
	return (&mail.Address{Name: name, Address: address}).String()
}

func encodeHeader(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return mime.QEncoding.Encode("UTF-8", s)
}

func expandPersonalization(body string, r MailAddress) string {
	out := body
	out = strings.ReplaceAll(out, "{name}", r.Name)
	out = strings.ReplaceAll(out, "{email}", r.Address)
	return out
}
