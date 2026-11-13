// Package mail sends notification email through the optional SMTP server
// (SMTP_* configuration). Everything in handbill works without it: when
// SMTP_HOST is unset, Enabled reports false and Send returns ErrDisabled.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/ferrule-io/handbill/internal/config"
)

// ErrDisabled is returned by Send when SMTP is not configured.
var ErrDisabled = errors.New("mail: SMTP is not configured")

// Message is one email. HTML is optional; Text is always sent.
type Message struct {
	To, Subject, Text, HTML string
}

// Sender delivers messages.
type Sender interface {
	Enabled() bool // false when SMTP_HOST is unset
	Send(ctx context.Context, m Message) error
}

// New returns a Sender for cfg; a nil cfg yields a disabled Sender.
func New(cfg *config.SMTP) Sender {
	if cfg == nil {
		return disabled{}
	}
	return &smtpSender{cfg: *cfg}
}

type disabled struct{}

func (disabled) Enabled() bool                       { return false }
func (disabled) Send(context.Context, Message) error { return ErrDisabled }

type smtpSender struct{ cfg config.SMTP }

func (s *smtpSender) Enabled() bool { return true }

// sendTimeout bounds one delivery when ctx has no earlier deadline.
const sendTimeout = 30 * time.Second

func (s *smtpSender) Send(ctx context.Context, m Message) error {
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("mail: recipient %q: %w", m.To, err)
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("mail: subject contains a line break")
	}
	msg, err := build(s.cfg.From, to, m, time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	return s.deliver(ctx, to.Address, msg)
}

func (s *smtpSender) deliver(ctx context.Context, rcpt string, msg []byte) error {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	tlsCfg := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mail: dial %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if s.cfg.TLS == config.SMTPTLSOnly {
		conn = tls.Client(conn, tlsCfg)
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail: %s: %w", addr, err)
	}
	defer c.Close()
	if s.cfg.TLS == config.SMTPStartTLS {
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("mail: STARTTLS: %w", err)
		}
	}
	if s.cfg.Username != "" {
		// PlainAuth refuses to send credentials without TLS unless the
		// server is on localhost.
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("mail: auth: %w", err)
		}
	}
	if err := c.Mail(s.cfg.From.Address); err != nil {
		return fmt.Errorf("mail: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(rcpt); err != nil {
		return fmt.Errorf("mail: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("mail: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: send: %w", err)
	}
	return c.Quit()
}

// build renders an RFC 5322 message: text only, or multipart/alternative
// when HTML is set. Bodies are quoted-printable.
func build(from, to *mail.Address, m Message, now time.Time) ([]byte, error) {
	var buf bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	h("From", from.String())
	h("To", to.String())
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", messageID(from.Address))
	h("MIME-Version", "1.0")
	h("Auto-Submitted", "auto-generated")
	if m.HTML == "" {
		h("Content-Type", "text/plain; charset=utf-8")
		h("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQP(&buf, m.Text); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	mw := multipart.NewWriter(&buf)
	h("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
	buf.WriteString("\r\n")
	for _, part := range []struct{ ct, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.ct + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		if err := writeQP(pw, part.body); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeQP(w interface{ Write([]byte) (int, error) }, s string) error {
	qp := quotedprintable.NewWriter(w)
	crlf := strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
	if _, err := qp.Write([]byte(crlf)); err != nil {
		return err
	}
	return qp.Close()
}

func messageID(from string) string {
	domain := "handbill.invalid"
	if _, d, ok := strings.Cut(from, "@"); ok && d != "" {
		domain = d
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "<" + hex.EncodeToString(b[:]) + "@" + domain + ">"
}
