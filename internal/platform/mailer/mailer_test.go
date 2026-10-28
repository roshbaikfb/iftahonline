package mailer_test

import (
	"bufio"
	"bytes"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
)

// fakeSMTP is a minimal SMTP server (no STARTTLS) that records one message.
type fakeSMTP struct {
	addr string
	mu   sync.Mutex
	from string
	rcpt string
	data string
}

func startSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &fakeSMTP{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	say := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-fake")
			say("250 8BITMIME")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			s.mu.Lock()
			s.from = strings.TrimSpace(line[10:])
			s.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			s.mu.Lock()
			s.rcpt = strings.TrimSpace(line[8:])
			s.mu.Unlock()
			say("250 ok")
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.data = b.String()
			s.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func TestSMTPMailer(t *testing.T) {
	t.Parallel()
	srv := startSMTP(t)
	m := mailer.SMTPMailer{Addr: srv.addr, From: "iftahonline <no-reply@iftahonline.com>", Timeout: 5 * time.Second}
	err := m.Send(t.Context(), mailer.Message{To: "ahmed@example.com", Subject: "دعوة للانضمام", Text: "line one\nline two"})
	if err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !strings.HasPrefix(srv.from, "<no-reply@iftahonline.com>") || srv.rcpt != "<ahmed@example.com>" {
		t.Fatalf("envelope = %q -> %q", srv.from, srv.rcpt)
	}
	for _, want := range []string{"To: <ahmed@example.com>", "Subject: =?utf-8?q?", "charset=utf-8", "line one\r\nline two"} {
		if !strings.Contains(srv.data, want) {
			t.Fatalf("message lacks %q:\n%s", want, srv.data)
		}
	}
}

func TestSMTPMailerRefusals(t *testing.T) {
	t.Parallel()
	srv := startSMTP(t)
	tests := map[string]struct {
		m   mailer.SMTPMailer
		msg mailer.Message
	}{
		"credentials without TLS": {mailer.SMTPMailer{Addr: srv.addr, From: "a@b.co", Username: "u", Password: "p"}, mailer.Message{To: "x@y.co"}},
		"bad from":                {mailer.SMTPMailer{Addr: srv.addr, From: "nope"}, mailer.Message{To: "x@y.co"}},
		"bad to":                  {mailer.SMTPMailer{Addr: srv.addr, From: "a@b.co"}, mailer.Message{To: "nope"}},
		"header injection":        {mailer.SMTPMailer{Addr: srv.addr, From: "a@b.co"}, mailer.Message{To: "x@y.co", Subject: "hi\r\nBcc: z@z.co"}},
		"unreachable":             {mailer.SMTPMailer{Addr: "127.0.0.1:1", From: "a@b.co", Timeout: time.Second}, mailer.Message{To: "x@y.co"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := tt.m.Send(t.Context(), tt.msg); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestLogMailerMasks(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := mailer.LogMailer{Log: slog.New(slog.NewJSONHandler(&buf, nil))}
	if err := l.Send(t.Context(), mailer.Message{To: "ahmed@example.com", Subject: "Invite", Text: "secret-link-token"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "ahmed@") || strings.Contains(out, "secret-link-token") || !strings.Contains(out, "a***@example.com") {
		t.Fatalf("log = %s", out)
	}
	if mailer.MaskEmail("nope") != "***" {
		t.Fatal("mask without @")
	}
}

func TestFromConfig(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	if _, ok := mailer.FromConfig(config.Mail{}, log).(mailer.LogMailer); !ok {
		t.Fatal("no SMTP_ADDR: want LogMailer")
	}
	m, ok := mailer.FromConfig(config.Mail{SMTPAddr: "smtp.example.com:587", SMTPUsername: "u", From: "a@b.co"}, log).(mailer.SMTPMailer)
	if !ok || m.Addr != "smtp.example.com:587" || m.Username != "u" || m.From != "a@b.co" {
		t.Fatalf("SMTP_ADDR set: got %#v", m)
	}
}
