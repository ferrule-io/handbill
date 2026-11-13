package sharing

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/mjquinlan2000/agent-dash/internal/mail"
	"github.com/mjquinlan2000/agent-dash/internal/rbac"
)

// send delivers m and reports whether the mail server accepted it. A
// failure is logged, not returned: the caller already has the link to share
// another way.
func (s *Service) send(ctx context.Context, m mail.Message) bool {
	if err := s.mail.Send(ctx, m); err != nil {
		s.log.Warn("sharing notification not sent", "err", err)
		return false
	}
	return true
}

func sender(from string) string {
	if from == "" {
		return "Someone"
	}
	return from
}

func expiry(t time.Time) string { return t.UTC().Format("January 2, 2006") }

// Subjects and the text body quote the site title; it has no line breaks
// or control characters (sites.cleanTitle), so it cannot inject headers.

func inviteMessage(to, from, title string, role rbac.Role, link string, expires time.Time, granted bool) mail.Message {
	verb := "view"
	if role == rbac.Manager {
		verb = "help manage"
	}
	lead := fmt.Sprintf("%s invited you to %s “%s” on agent-dash.", sender(from), verb, title)
	action := "Open the invitation to create an account or sign in, and the site is yours to see:"
	note := fmt.Sprintf("The link works once and expires on %s. If you weren’t expecting this, you can ignore it.", expiry(expires))
	if granted {
		action = "Your account already has access. Open the site from agent-dash:"
		note = "If you weren’t expecting this, you can ignore it."
	}
	return message(to, fmt.Sprintf("%s invited you to “%s”", sender(from), title), lead, action, "Open invitation", link, note)
}

func transferMessage(to, from, title, link string, expires time.Time) mail.Message {
	lead := fmt.Sprintf("%s wants to make you the owner of “%s” on agent-dash.", sender(from), title)
	action := "Open the link and accept while signed in to the account that should own it. They stay on as a manager."
	note := fmt.Sprintf("The link works once and expires on %s. If you weren’t expecting this, you can ignore it.", expiry(expires))
	return message(to, fmt.Sprintf("Ownership of “%s” is waiting for you", title), lead, action, "Review transfer", link, note)
}

func message(to, subject, lead, action, button, link, note string) mail.Message {
	text := strings.Join([]string{lead, "", action, "", link, "", note, ""}, "\n")
	e := html.EscapeString
	body := `<div style="font-family:system-ui,-apple-system,'Segoe UI',sans-serif;font-size:15px;line-height:1.55;color:#1d1b18;max-width:32rem">` +
		`<p style="margin:0 0 16px">` + e(lead) + `</p>` +
		`<p style="margin:0 0 20px">` + e(action) + `</p>` +
		`<p style="margin:0 0 24px"><a href="` + e(link) + `" style="display:inline-block;background:#c2410c;color:#fff;text-decoration:none;font-weight:600;padding:10px 18px;border-radius:8px">` + e(button) + `</a></p>` +
		`<p style="margin:0 0 8px;color:#6b665d;font-size:13px">Or paste this address into your browser:<br><span style="word-break:break-all">` + e(link) + `</span></p>` +
		`<p style="margin:0;color:#6b665d;font-size:13px">` + e(note) + `</p>` +
		`</div>`
	return mail.Message{To: to, Subject: subject, Text: text, HTML: body}
}
