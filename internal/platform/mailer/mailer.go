// Package mailer sends plain-text email. No provider is chosen yet
// (ADR-0005), so mail goes over SMTP, which every provider (and Mailpit in
// local development) speaks; with no SMTP_ADDR it only logs a masked
// notice. It lives in platform because identity and messaging both send
// mail (ADR-0038).
//
// Every send runs in a River job, never in a request (CLAUDE.md
// performance rules), and has its own timeout.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/config"
)

// Message is a plain-text email.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Mailer sends email.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// SMTPMailer sends through an SMTP server, upgrading to TLS with STARTTLS
// when offered (required when credentials are set).
type SMTPMailer struct {
	Addr     string
	Username string
	Password string
	From     string
	Timeout  time.Duration
}

// Send implements Mailer.
func (m SMTPMailer) Send(ctx context.Context, msg Message) error {
	from, err := mail.ParseAddress(m.From)
	if err != nil {
		return fmt.Errorf("mailer: from address: %w", err)
	}
	to, err := mail.ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("mailer: to address: %w", err)
	}
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return errors.New("mailer: subject contains a line break")
	}
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", m.Addr)
	if err != nil {
		return fmt.Errorf("mailer: dial: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		if err = conn.SetDeadline(dl); err != nil {
			_ = conn.Close()
			return fmt.Errorf("mailer: deadline: %w", err)
		}
	}
	host, _, err := net.SplitHostPort(m.Addr)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mailer: smtp address: %w", err)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mailer: smtp: %w", err)
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err = c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mailer: starttls: %w", err)
		}
	} else if m.Username != "" {
		return errors.New("mailer: refusing to send credentials without TLS")
	}
	if m.Username != "" {
		if err = c.Auth(smtp.PlainAuth("", m.Username, m.Password, host)); err != nil {
			return fmt.Errorf("mailer: auth: %w", err)
		}
	}
	if err = c.Mail(from.Address); err != nil {
		return fmt.Errorf("mailer: mail from: %w", err)
	}
	if err = c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("mailer: rcpt: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mailer: data: %w", err)
	}
	if _, err := w.Write(compose(from, to, msg)); err != nil {
		return fmt.Errorf("mailer: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: send: %w", err)
	}
	return c.Quit()
}

func compose(from, to *mail.Address, msg Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + from.String() + "\r\n")
	b.WriteString("To: " + to.String() + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", msg.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(msg.Text, "\r\n", "\n"), "\n", "\r\n"))
	b.WriteString("\r\n")
	return []byte(b.String())
}

// LogMailer stands in when no SMTP server is configured. It logs who would
// have been mailed (masked) and the subject, never the body, which may
// carry links with secrets.
type LogMailer struct{ Log *slog.Logger }

// Send implements Mailer.
func (l LogMailer) Send(ctx context.Context, m Message) error {
	l.Log.InfoContext(ctx, "mailer: mail not sent (no SMTP configured)", "to", MaskEmail(m.To), "subject", m.Subject)
	return nil
}

// MaskEmail keeps the first character of the local part and the domain.
func MaskEmail(addr string) string {
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" {
		return "***"
	}
	return local[:1] + "***@" + domain
}

// FromConfig returns an SMTPMailer when SMTP_ADDR is set, else a LogMailer.
func FromConfig(c config.Mail, log *slog.Logger) Mailer {
	if c.SMTPAddr == "" {
		return LogMailer{Log: log}
	}
	return SMTPMailer{Addr: c.SMTPAddr, Username: c.SMTPUsername, Password: c.SMTPPassword, From: c.From}
}
