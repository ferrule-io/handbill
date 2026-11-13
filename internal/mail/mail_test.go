package mail

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"testing"

	"github.com/mjquinlan2000/agent-dash/internal/config"
)

func TestDisabled(t *testing.T) {
	s := New(nil)
	if s.Enabled() {
		t.Fatal("nil config is enabled")
	}
	if err := s.Send(context.Background(), Message{To: "a@example.com"}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Send = %v, want ErrDisabled", err)
	}
}

// fakeSMTP accepts one plain-text SMTP session and returns what it received.
func fakeSMTP(t *testing.T) (addr string, got <-chan session) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan session, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		ch <- serve(conn)
	}()
	return ln.Addr().String(), ch
}

type session struct {
	from, rcpt string
	data       string
}

func serve(conn net.Conn) session {
	var s session
	r := bufio.NewReader(conn)
	say := func(line string) { io.WriteString(conn, line+"\r\n") }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return s
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			s.from = line[len("MAIL FROM:"):]
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			s.rcpt = line[len("RCPT TO:"):]
			say("250 ok")
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return s
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.data = b.String()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return s
		default:
			say("502 unknown")
		}
	}
}

func TestSend(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	s := New(&config.SMTP{
		Host: host, Port: p, TLS: config.SMTPNoTLS,
		From: &mail.Address{Name: "agent-dash", Address: "dash@example.com"},
	})
	if !s.Enabled() {
		t.Fatal("configured sender is disabled")
	}
	err := s.Send(context.Background(), Message{
		To:      "Viewer <viewer@example.com>",
		Subject: "Confirm your email — agent-dash",
		Text:    "Open this link:\nhttps://dash.example.com/verify-email#token=abc",
		HTML:    `<p><a href="https://dash.example.com/verify-email#token=abc">Confirm</a></p>`,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := <-got
	if sess.from != "<dash@example.com>" || sess.rcpt != "<viewer@example.com>" {
		t.Fatalf("envelope = %q -> %q", sess.from, sess.rcpt)
	}
	msg, err := mail.ReadMessage(strings.NewReader(sess.data))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "Confirm your email — agent-dash" {
		t.Fatalf("subject = %q, %v", subject, err)
	}
	if !strings.HasPrefix(msg.Header.Get("Content-Type"), "multipart/alternative;") {
		t.Fatalf("Content-Type = %q", msg.Header.Get("Content-Type"))
	}
	if !strings.Contains(sess.data, "verify-email#token=3Dabc") && !strings.Contains(sess.data, "verify-email#token=abc") {
		t.Fatalf("link missing from body:\n%s", sess.data)
	}
}

func TestRejectsHeaderInjection(t *testing.T) {
	s := New(&config.SMTP{Host: "127.0.0.1", Port: 1, From: &mail.Address{Address: "dash@example.com"}})
	if err := s.Send(context.Background(), Message{To: "a@example.com", Subject: "x\r\nBcc: b@example.com"}); err == nil {
		t.Fatal("subject with CRLF accepted")
	}
	if err := s.Send(context.Background(), Message{To: "not an address", Subject: "x"}); err == nil {
		t.Fatal("bad recipient accepted")
	}
}
