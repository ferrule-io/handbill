package sharing

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/mail"
	"github.com/ferrule-io/handbill/internal/rbac"
)

// NotSentReason says why a requested notification was not emailed. The
// link is returned either way, to share another way.
type NotSentReason string

const (
	// NotSentMailDisabled: SMTP is not configured.
	NotSentMailDisabled NotSentReason = "mail_disabled"
	// NotSentSenderUnverified: the sender's primary email is missing or
	// unverified, so handbill does not mail anyone in their name.
	NotSentSenderUnverified NotSentReason = "sender_unverified"
	// NotSentFailed: the mail server did not accept the message.
	NotSentFailed NotSentReason = "send_failed"
)

// notifyFrom sends m when SMTP is configured and the sender (whose
// address m quotes) is verified, and reports whether the mail server
// accepted it, or why not.
func (s *Service) notifyFrom(ctx context.Context, senderVerified bool, m mail.Message) (bool, NotSentReason) {
	switch {
	case !s.mail.Enabled():
		return false, NotSentMailDisabled
	case !senderVerified:
		return false, NotSentSenderUnverified
	case !s.send(ctx, m):
		return false, NotSentFailed
	}
	return true, ""
}

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

func expiry(t time.Time) string { return t.UTC().Format("January 2, 2006") }

// Subjects and the text body quote the site title; it has no line breaks
// or control characters (sites.cleanTitle), so it cannot inject headers.
// from is the sender's verified primary email.

func inviteMessage(to, from, title string, role rbac.Role, link string, expires time.Time, granted bool) mail.Message {
	verb := "view"
	if role == rbac.Manager {
		verb = "help manage"
	}
	lead := fmt.Sprintf("%s invited you to %s “%s” on Handbill.", from, verb, title)
	action := "Open the invitation to create an account or sign in, and the site is yours to see:"
	note := fmt.Sprintf("The link works once and expires on %s. If you weren’t expecting this, you can ignore it.", expiry(expires))
	if granted {
		action = "Your account already has access. Open the site from Handbill:"
		note = "If you weren’t expecting this, you can ignore it."
	}
	return message(to, fmt.Sprintf("%s invited you to “%s”", from, title), lead, action, "Open invitation", link, note)
}

func transferMessage(to, from, title, link string, expires time.Time) mail.Message {
	lead := fmt.Sprintf("%s wants to make you the owner of “%s” on Handbill.", from, title)
	action := "Open the link and accept while signed in to the account that should own it. They stay on as a manager."
	note := fmt.Sprintf("The link works once and expires on %s. If you weren’t expecting this, you can ignore it.", expiry(expires))
	return message(to, fmt.Sprintf("Ownership of “%s” is waiting for you", title), lead, action, "Review transfer", link, note)
}

func message(to, subject, lead, action, button, link, note string) mail.Message {
	text := strings.Join([]string{lead, "", action, "", link, "", note, ""}, "\n")
	e := html.EscapeString
	body := `<div style="font-family:system-ui,-apple-system,'Segoe UI',sans-serif;font-size:15px;line-height:1.55;color:#131927;max-width:32rem">` +
		`<p style="margin:0 0 16px">` + e(lead) + `</p>` +
		`<p style="margin:0 0 20px">` + e(action) + `</p>` +
		`<p style="margin:0 0 24px"><a href="` + e(link) + `" style="display:inline-block;background:#c12b09;color:#fffbf4;text-decoration:none;font-weight:600;padding:10px 18px;border-radius:8px">` + e(button) + `</a></p>` +
		`<p style="margin:0 0 8px;color:#5d6370;font-size:13px">Or paste this address into your browser:<br><span style="word-break:break-all">` + e(link) + `</span></p>` +
		`<p style="margin:0;color:#5d6370;font-size:13px">` + e(note) + `</p>` +
		`</div>`
	return mail.Message{To: to, Subject: subject, Text: text, HTML: body}
}

// NotifyUpdate emails every member of a site except the publisher that a
// new version is live, with the site's address. by must be able to
// publish. It returns how many messages the mail server accepted, and when
// none were, why: SMTP is not configured or by's primary email is not
// verified (nothing attempted), or every attempt failed. Members without a
// primary email are skipped.
func (s *Service) NotifyUpdate(ctx context.Context, siteID string, by uuid.UUID) (sent int, reason NotSentReason, err error) {
	if err := s.authorizeRead(ctx, siteID, by, rbac.Publish); err != nil {
		return 0, "", err
	}
	if !s.mail.Enabled() {
		return 0, NotSentMailDisabled, nil
	}
	q := db.New(s.db)
	from, verified, err := primaryEmail(ctx, q, by)
	if err != nil {
		return 0, "", err
	}
	if !verified {
		return 0, NotSentSenderUnverified, nil
	}
	site, err := q.GetSite(ctx, siteID)
	if err != nil {
		return 0, "", notFound(err, ErrNotFound)
	}
	rows, err := q.ListMembers(ctx, siteID)
	if err != nil {
		return 0, "", err
	}
	link := s.cfg.SiteOrigin(siteID) + "/"
	failed := 0
	for _, r := range rows {
		if r.UserID == by || r.Email == nil {
			continue
		}
		lead := fmt.Sprintf("%s published a new version of “%s” on Handbill.", from, site.Title)
		m := message(*r.Email, fmt.Sprintf("“%s” was updated", site.Title), lead,
			"Open the site to see what changed:", "Open site", link,
			"You get this because the site is shared with you.")
		if s.send(ctx, m) {
			sent++
		} else {
			failed++
		}
	}
	if sent == 0 && failed > 0 {
		return 0, NotSentFailed, nil
	}
	return sent, "", nil
}
