package mcp

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ferrule-io/handbill/internal/mail"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
)

type fakeMail struct {
	mu       sync.Mutex
	disabled bool
	sent     []mail.Message
}

func (f *fakeMail) Enabled() bool { return !f.disabled }
func (f *fakeMail) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeMail) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func TestShareSiteRolesAndLinks(t *testing.T) {
	e := newToolEnv(t)
	r := e.roles()
	args := func(email, role string, notify bool) map[string]any {
		return map[string]any{"site": r.site, "email": email, "role": role, "notify": notify}
	}

	// A link every time, emailed when asked.
	out := ok[shareOut](t, r.manager, "share_site", args("new@example.org", "viewer", true))
	if !strings.Contains(out.InviteLink, "/invite#token=") || out.Granted || !out.Emailed || e.mail.count() != 1 {
		t.Fatalf("share = %+v, sent %d", out, e.mail.count())
	}
	again := ok[shareOut](t, r.manager, "share_site", args("new@example.org", "viewer", false))
	if again.InviteLink == "" || again.InviteLink == out.InviteLink || again.Emailed || e.mail.count() != 1 {
		t.Fatalf("re-share = %+v", again)
	}
	// A verified account gets access now, and still a link.
	sitestest.User(t, e.d, "known@example.org")
	g := ok[shareOut](t, r.owner, "share_site", args("known@example.org", "manager", false))
	if !g.Granted || g.InviteLink == "" || !strings.Contains(g.Note, "already has access") {
		t.Fatalf("granted share = %+v", g)
	}
	fails(t, r.owner, "share_site", args("known@example.org", "viewer", false), "use set_member_role")
	fails(t, r.manager, "share_site", args("m@example.org", "manager", false), "your role on site "+r.site+" is manager: share_site needs owner")
	fails(t, r.viewer, "share_site", args("v@example.org", "viewer", false), "share_site needs manager")
	fails(t, r.stranger, "share_site", args("v@example.org", "viewer", false), "not shared with you")
	fails(t, r.owner, "share_site", args("not-an-email", "viewer", false), "valid email")
	if _, isErr := call(t, r.owner, "share_site", args("x@example.org", "owner", false)); !isErr {
		t.Fatal("role owner accepted")
	}

	// Without SMTP the link is still returned and the note says so.
	e.mail.disabled = true
	n := ok[shareOut](t, r.owner, "share_site", args("nosmtp@example.org", "viewer", true))
	if n.Emailed || n.InviteLink == "" || !strings.Contains(n.Note, "cannot send email") {
		t.Fatalf("no smtp share = %+v", n)
	}
}

func TestMembersRoleAndRevoke(t *testing.T) {
	e := newToolEnv(t)
	r := e.roles()
	ok[shareOut](t, r.owner, "share_site", map[string]any{"site": r.site, "email": "p@example.org", "role": "viewer"})
	list := ok[listMembersOut](t, r.manager, "list_members", map[string]any{"site": r.site})
	if len(list.Members) != 3 || len(list.PendingInvites) != 1 || list.Members[0].Role != "owner" {
		t.Fatalf("members = %+v", list)
	}
	fails(t, r.viewer, "list_members", map[string]any{"site": r.site}, "list_members needs manager")
	fails(t, r.stranger, "list_members", map[string]any{"site": r.site}, "not shared with you")

	var viewerID, managerID, ownerID string
	for _, m := range list.Members {
		switch m.Role {
		case "viewer":
			viewerID = m.UserID
		case "manager":
			managerID = m.UserID
		case "owner":
			ownerID = m.UserID
		}
	}
	set := func(id, role string) map[string]any {
		return map[string]any{"site": r.site, "user_id": id, "role": role}
	}
	fails(t, r.manager, "set_member_role", set(viewerID, "manager"), "set_member_role needs owner")
	fails(t, r.owner, "set_member_role", set(ownerID, "viewer"), "transfer_ownership")
	ok[setRoleOut](t, r.owner, "set_member_role", set(viewerID, "manager"))
	ok[setRoleOut](t, r.owner, "set_member_role", set(viewerID, "viewer"))

	rev := func(id string) map[string]any { return map[string]any{"site": r.site, "id": id} }
	fails(t, r.manager, "revoke_access", rev(managerID), "revoke_access needs owner")
	fails(t, r.viewer, "revoke_access", rev(viewerID), "revoke_access needs manager")
	fails(t, r.owner, "revoke_access", rev(ownerID), "transfer_ownership")
	ok[revokeOut](t, r.manager, "revoke_access", rev(list.PendingInvites[0].InviteID))
	ok[revokeOut](t, r.manager, "revoke_access", rev(viewerID))
	fails(t, r.manager, "revoke_access", rev(viewerID), "see list_members")
	// The removed viewer has no access on their next call.
	fails(t, r.viewer, "list_members", map[string]any{"site": r.site}, "not shared with you")
}

func TestPublicLinkAndTransfer(t *testing.T) {
	e := newToolEnv(t)
	r := e.roles()
	op := func(o string) map[string]any { return map[string]any{"site": r.site, "op": o} }
	fails(t, r.manager, "set_public_link", op("enable"), "set_public_link needs owner")
	on := ok[publicLinkOut](t, r.owner, "set_public_link", op("enable"))
	if on.Link == nil || !strings.Contains(*on.Link, "/_auth/share/") || !strings.Contains(on.Note, "shown only now") {
		t.Fatalf("enable = %+v", on)
	}
	fails(t, r.owner, "set_public_link", op("enable"), "use op rotate")
	rot := ok[publicLinkOut](t, r.owner, "set_public_link", op("rotate"))
	if rot.Link == nil || *rot.Link == *on.Link {
		t.Fatalf("rotate = %+v", rot)
	}
	off := ok[publicLinkOut](t, r.owner, "set_public_link", op("disable"))
	if off.Link != nil || off.Enabled {
		t.Fatalf("disable = %+v", off)
	}
	fails(t, r.owner, "set_public_link", op("rotate"), "use op enable")

	tr := map[string]any{"site": r.site, "email": "heir@example.org", "notify": true}
	fails(t, r.manager, "transfer_ownership", tr, "transfer_ownership needs owner")
	fails(t, r.owner, "transfer_ownership", map[string]any{"site": r.site, "email": "owner@example.com"}, "already own")
	out := ok[transferOut](t, r.owner, "transfer_ownership", tr)
	if !strings.Contains(out.AcceptanceLink, "/invite#token=") || !out.Emailed || e.mail.count() != 1 {
		t.Fatalf("transfer = %+v", out)
	}
}

func TestPublishNotify(t *testing.T) {
	e := newToolEnv(t)
	r := e.roles()
	files := []map[string]any{{"path": "index.html", "content": "<h1>hi</h1>"}}
	pub := ok[publishOut](t, r.manager, "publish_site", map[string]any{"site": r.site, "mode": "replace", "files": files, "notify": true})
	// Owner and viewer are emailed; the publishing manager is not.
	if pub.NotifyResult != "emailed 2 other member(s)" || e.mail.count() != 2 {
		t.Fatalf("notify = %q, sent %d", pub.NotifyResult, e.mail.count())
	}
	for _, m := range e.mail.sent {
		if m.To == "manager@example.com" || !strings.Contains(m.Text, "Team") {
			t.Errorf("message %+v", m)
		}
	}
	quiet := ok[publishOut](t, r.manager, "publish_site", map[string]any{"site": r.site, "mode": "replace", "files": files})
	if quiet.NotifyResult != "" || e.mail.count() != 2 {
		t.Fatal("notify without asking")
	}
	e.mail.disabled = true
	off := ok[publishOut](t, r.manager, "publish_site", map[string]any{"site": r.site, "mode": "replace", "files": files, "notify": true})
	if !strings.Contains(off.NotifyResult, "no email configured") || e.mail.count() != 2 {
		t.Fatalf("no smtp notify = %q", off.NotifyResult)
	}
}
