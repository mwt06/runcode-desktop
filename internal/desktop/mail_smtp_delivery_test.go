package desktop

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/mail"
)

type fakeSMTPBehavior struct {
	authFail   bool
	rcptReject string
	dropData   bool // close without final 250
	quitFail   bool
	password   string
}

type fakeSMTPServer struct {
	ln       net.Listener
	host     string
	port     int
	certPEM  []byte
	behavior fakeSMTPBehavior
	mu       sync.Mutex
	received []string
	log      []string
}

func startFakeSMTPTLS(t *testing.T, b fakeSMTPBehavior) *fakeSMTPServer {
	t.Helper()
	certPEM, keyPEM := mustTestCert(t, "127.0.0.1")
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	s := &fakeSMTPServer{ln: ln, host: "127.0.0.1", port: port, certPEM: certPEM, behavior: b}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeSMTPServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTPServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(line string) {
		s.mu.Lock()
		s.log = append(s.log, "S:"+line)
		s.mu.Unlock()
		_, _ = w.WriteString(line + "\r\n")
		_ = w.Flush()
	}
	write("220 fake.example ESMTP")
	authed := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.mu.Lock()
		s.log = append(s.log, "C:"+line)
		s.mu.Unlock()
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			write("250-fake.example")
			write("250-AUTH PLAIN LOGIN")
			write("250 OK")
		case strings.HasPrefix(upper, "AUTH"):
			if s.behavior.authFail {
				write("535 5.7.8 Authentication credentials invalid")
				continue
			}
			authed = true
			write("235 2.7.0 Authentication successful")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			if !authed {
				write("530 auth required")
				continue
			}
			write("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			addr := line
			if s.behavior.rcptReject != "" && strings.Contains(strings.ToLower(addr), strings.ToLower(s.behavior.rcptReject)) {
				write("550 5.1.1 User unknown")
				continue
			}
			write("250 OK")
		case upper == "RSET":
			write("250 OK")
		case upper == "DATA":
			write("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				body.WriteString(l)
			}
			s.mu.Lock()
			s.received = append(s.received, body.String())
			s.mu.Unlock()
			if s.behavior.dropData {
				_ = conn.Close()
				return
			}
			write("250 2.0.0 Ok: queued")
		case upper == "QUIT":
			if s.behavior.quitFail {
				_ = conn.Close()
				return
			}
			write("221 Bye")
			return
		default:
			write("250 OK")
		}
	}
}

func mustTestCert(t *testing.T, host string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	return certPEM, keyPEM
}

func withTestTLSRoots(t *testing.T, certPEM []byte) {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("bad cert")
	}
	prev := mailTLSConfig
	mailTLSConfig = func(_ string) *tls.Config {
		return &tls.Config{ServerName: "127.0.0.1", RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	t.Cleanup(func() { mailTLSConfig = prev })
}

func TestSMTPSendMessageAuthFailure(t *testing.T) {
	srv := startFakeSMTPTLS(t, fakeSMTPBehavior{authFail: true, password: "secret-pass"})
	withTestTLSRoots(t, srv.certPEM)
	msg := mustOutbound(t, "a@example.com")
	res, err := realMailSMTP{}.SendMessage(context.Background(), srv.host, srv.port, "user@example.com", "secret-pass", msg)
	if err == nil || res.Status != "failed" {
		t.Fatalf("status=%s err=%v", res.Status, err)
	}
	for _, p := range res.Phases {
		if strings.Contains(p.Detail, "secret-pass") {
			t.Fatalf("secret leaked in phase: %+v", p)
		}
	}
}

func TestSMTPSendMessageRcptRejectAbandonsData(t *testing.T) {
	srv := startFakeSMTPTLS(t, fakeSMTPBehavior{rcptReject: "bad@example.com"})
	withTestTLSRoots(t, srv.certPEM)
	msg, err := mail.BuildOutboundMessage(
		mail.DisplayFrom{Address: "me@example.com"},
		mail.PreparedSnapshot{
			Subject: "s", BodyMarkdown: "b", SendMode: mail.ModeTogether,
			ResolvedTo: []mail.MailAddress{{Address: "ok@example.com"}, {Address: "bad@example.com"}},
		},
		mail.SubmissionItem{Recipient: mail.MailAddress{Address: "ok@example.com"}},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	res, err := realMailSMTP{}.SendMessage(context.Background(), srv.host, srv.port, "me@example.com", "pw", msg)
	if err == nil || res.Status != "failed" {
		t.Fatalf("status=%s err=%v phases=%v", res.Status, err, res.Phases)
	}
	srv.mu.Lock()
	n := len(srv.received)
	srv.mu.Unlock()
	if n != 0 {
		t.Fatalf("DATA should be abandoned, got %d", n)
	}
	hasRSET := false
	for _, p := range res.Phases {
		if p.Phase == "rset" {
			hasRSET = true
		}
	}
	if !hasRSET {
		t.Fatalf("expected rset phase: %+v", res.Phases)
	}
}

func TestSMTPSendMessageDataReplyLostIsUnknown(t *testing.T) {
	srv := startFakeSMTPTLS(t, fakeSMTPBehavior{dropData: true})
	withTestTLSRoots(t, srv.certPEM)
	res, err := realMailSMTP{}.SendMessage(context.Background(), srv.host, srv.port, "me@example.com", "pw", mustOutbound(t, "a@example.com"))
	if res.Status != "unknown" {
		t.Fatalf("status=%s err=%v", res.Status, err)
	}
}

func TestSMTPSendMessageQuitFailStillAccepted(t *testing.T) {
	srv := startFakeSMTPTLS(t, fakeSMTPBehavior{quitFail: true})
	withTestTLSRoots(t, srv.certPEM)
	res, err := realMailSMTP{}.SendMessage(context.Background(), srv.host, srv.port, "me@example.com", "pw", mustOutbound(t, "a@example.com"))
	if err != nil || res.Status != "accepted" {
		t.Fatalf("status=%s err=%v", res.Status, err)
	}
	srv.mu.Lock()
	n := len(srv.received)
	srv.mu.Unlock()
	if n != 1 {
		t.Fatalf("received=%d", n)
	}
}

func mustOutbound(t *testing.T, to string) mail.OutboundMessage {
	t.Helper()
	msg, err := mail.BuildOutboundMessage(
		mail.DisplayFrom{Address: "me@example.com"},
		mail.PreparedSnapshot{
			Subject: "s", BodyMarkdown: "body", SendMode: mail.ModeTogether,
			ResolvedTo: []mail.MailAddress{{Address: to}},
		},
		mail.SubmissionItem{Recipient: mail.MailAddress{Address: to}},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}
