package sharing_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/mail"
	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sharing"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
)

type fakeMail struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (f *fakeMail) Enabled() bool { return true }
func (f *fakeMail) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

type env struct {
	t     *testing.T
	d     *server.Deps
	svc   *sharing.Service
	sites *sites.Service
	mail  *fakeMail
	owner uuid.UUID
	n     int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := sitestest.Deps(t)
	e := &env{t: t, d: d, svc: sharing.NewService(d), sites: sites.NewService(d), mail: &fakeMail{}}
	e.svc.SetMailer(e.mail)
	e.owner = sitestest.User(t, d, "owner@example.com")
	return e
}

func (e *env) site() string {
	e.t.Helper()
	s, err := e.sites.Create(e.t.Context(), e.owner, "Kyoto in April", "")
	if err != nil {
		e.t.Fatal(err)
	}
	return s.ID
}

// user creates an account with a verified primary address.
func (e *env) user(label string) uuid.UUID {
	e.t.Helper()
	e.n++
	return sitestest.User(e.t, e.d, label+"-"+string(rune('a'+e.n%26))+uuid.NewString()[:6]+"@example.com")
}

// unverifiedUser creates an account whose primary address is unverified.
func (e *env) unverifiedUser(email string) uuid.UUID {
	e.t.Helper()
	q := db.New(e.d.DB)
	u, err := q.CreateUser(e.t.Context(), uuid.Must(uuid.NewV7()))
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := q.CreateEmail(e.t.Context(), db.CreateEmailParams{ID: uuid.Must(uuid.NewV7()), UserID: u.ID, Email: email, IsPrimary: true}); err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

func (e *env) member(siteID string, role rbac.Role) uuid.UUID {
	e.t.Helper()
	u := e.user(string(role))
	sitestest.Member(e.t, e.d, siteID, u, string(role))
	return u
}

func (e *env) role(siteID string, u uuid.UUID) rbac.Role {
	e.t.Helper()
	r, err := e.sites.Role(e.t.Context(), siteID, u)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.d.DB.Exec(e.t.Context(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func tokenOf(t *testing.T, link string) string {
	t.Helper()
	for _, sep := range []string{"#token=", "/_auth/share/"} {
		if i := strings.Index(link, sep); i >= 0 {
			return link[i+len(sep):]
		}
	}
	t.Fatalf("no token in %q", link)
	return ""
}

func who(id uuid.UUID) identity.User { return identity.User{ID: id} }

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		return
	}
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func wantInputError(t *testing.T, err error) {
	t.Helper()
	var ie *sites.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v, want *sites.InputError", err)
	}
}

// TestRolesTable checks every sharing row of the design's roles table
// (docs/design.md#roles) for each role, on a fresh site per case.
func TestRolesTable(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	type fixture struct {
		site                    string
		viewer, manager         uuid.UUID // targets
		viewerInvite, mgrInvite uuid.UUID
	}
	setup := func() fixture {
		f := fixture{site: e.site()}
		f.viewer = e.member(f.site, rbac.Viewer)
		f.manager = e.member(f.site, rbac.Manager)
		v, err := e.svc.Invite(ctx, f.site, e.owner, "pending-viewer@example.com", rbac.Viewer, false)
		wantErr(t, err, nil)
		m, err := e.svc.Invite(ctx, f.site, e.owner, "pending-manager@example.com", rbac.Manager, false)
		wantErr(t, err, nil)
		f.viewerInvite, f.mgrInvite = v.ID, m.ID
		return f
	}
	actions := []struct {
		name   string
		action rbac.Action // the roles-table row it exercises
		do     func(f fixture, by uuid.UUID) error
	}{
		{"list members", rbac.ManageViewers, func(f fixture, by uuid.UUID) error {
			_, _, err := e.svc.Members(ctx, f.site, by)
			return err
		}},
		{"invite viewer", rbac.ManageViewers, func(f fixture, by uuid.UUID) error {
			_, err := e.svc.Invite(ctx, f.site, by, "new-viewer@example.com", rbac.Viewer, false)
			return err
		}},
		{"remove viewer", rbac.ManageViewers, func(f fixture, by uuid.UUID) error {
			return e.svc.Revoke(ctx, f.site, by, f.viewer)
		}},
		{"revoke viewer invite", rbac.ManageViewers, func(f fixture, by uuid.UUID) error {
			return e.svc.Revoke(ctx, f.site, by, f.viewerInvite)
		}},
		{"public link state", rbac.ManageViewers, func(f fixture, by uuid.UUID) error {
			_, err := e.svc.PublicLinkState(ctx, f.site, by)
			return err
		}},
		{"invite manager", rbac.ManageManagers, func(f fixture, by uuid.UUID) error {
			_, err := e.svc.Invite(ctx, f.site, by, "new-manager@example.com", rbac.Manager, false)
			return err
		}},
		{"remove manager", rbac.ManageManagers, func(f fixture, by uuid.UUID) error {
			return e.svc.Revoke(ctx, f.site, by, f.manager)
		}},
		{"revoke manager invite", rbac.ManageManagers, func(f fixture, by uuid.UUID) error {
			return e.svc.Revoke(ctx, f.site, by, f.mgrInvite)
		}},
		{"promote viewer", rbac.ManageManagers, func(f fixture, by uuid.UUID) error {
			return e.svc.SetRole(ctx, f.site, by, f.viewer, rbac.Manager)
		}},
		{"demote manager", rbac.ManageManagers, func(f fixture, by uuid.UUID) error {
			return e.svc.SetRole(ctx, f.site, by, f.manager, rbac.Viewer)
		}},
		{"enable public link", rbac.ManagePublicLink, func(f fixture, by uuid.UUID) error {
			_, err := e.svc.SetPublicLink(ctx, f.site, by, sharing.LinkEnable)
			return err
		}},
		{"disable public link", rbac.ManagePublicLink, func(f fixture, by uuid.UUID) error {
			_, err := e.svc.SetPublicLink(ctx, f.site, by, sharing.LinkDisable)
			return err
		}},
		{"start transfer", rbac.Transfer, func(f fixture, by uuid.UUID) error {
			_, err := e.svc.StartTransfer(ctx, f.site, by, "heir@example.com", false)
			return err
		}},
		{"view transfer", rbac.Transfer, func(f fixture, by uuid.UUID) error {
			_, err := e.svc.PendingTransfer(ctx, f.site, by)
			return err
		}},
		{"cancel transfer", rbac.Transfer, func(f fixture, by uuid.UUID) error {
			return e.svc.CancelTransfer(ctx, f.site, by)
		}},
	}
	for _, role := range []rbac.Role{rbac.Owner, rbac.Manager, rbac.Viewer, ""} {
		for _, a := range actions {
			name := string(role)
			if name == "" {
				name = "stranger"
			}
			t.Run(name+"/"+a.name, func(t *testing.T) {
				f := setup()
				var by uuid.UUID
				switch role {
				case rbac.Owner:
					by = e.owner
				case "":
					by = e.user("stranger")
				default:
					by = e.member(f.site, role)
				}
				err := a.do(f, by)
				switch {
				case role == "":
					wantErr(t, err, sharing.ErrNotFound)
				case rbac.Can(role, a.action):
					wantErr(t, err, nil)
				default:
					wantErr(t, err, sharing.ErrForbidden)
				}
			})
		}
	}
	// The table itself, as the design states it.
	for _, row := range []struct {
		a                      rbac.Action
		owner, manager, viewer bool
	}{
		{rbac.View, true, true, true},
		{rbac.Publish, true, true, false},
		{rbac.EditMeta, true, true, false},
		{rbac.ManageViewers, true, true, false},
		{rbac.ManageManagers, true, false, false},
		{rbac.ManagePublicLink, true, false, false},
		{rbac.Transfer, true, false, false},
		{rbac.Delete, true, false, false},
	} {
		if rbac.Can(rbac.Owner, row.a) != row.owner || rbac.Can(rbac.Manager, row.a) != row.manager || rbac.Can(rbac.Viewer, row.a) != row.viewer {
			t.Errorf("rbac.Can disagrees with the roles table on %s", row.a)
		}
	}
}

func TestOwnerIsImmutableOutsideTransfer(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	wantErr(t, e.svc.Revoke(ctx, site, e.owner, e.owner), sharing.ErrOwner)
	wantErr(t, e.svc.SetRole(ctx, site, e.owner, e.owner, rbac.Viewer), sharing.ErrOwner)
	wantInputError(t, e.svc.SetRole(ctx, site, e.owner, e.owner, rbac.Owner))
	mgr := e.member(site, rbac.Manager)
	wantErr(t, e.svc.Revoke(ctx, site, mgr, e.owner), sharing.ErrOwner)
	wantErr(t, e.svc.SetRole(ctx, site, e.owner, uuid.New(), rbac.Viewer), sharing.ErrNoSuchMember)
	wantErr(t, e.svc.Revoke(ctx, site, e.owner, uuid.New()), sharing.ErrNoSuchMember)
}

func TestInviteSingleUse(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	inv, err := e.svc.Invite(ctx, site, e.owner, "  Friend@Example.com ", rbac.Viewer, false)
	wantErr(t, err, nil)
	if inv.Granted || inv.Email != "friend@example.com" {
		t.Fatalf("invite = %+v", inv)
	}
	if !strings.HasPrefix(inv.Link, sitestest.AppOrigin+"/invite#token=") {
		t.Fatalf("link = %q", inv.Link)
	}
	tok := tokenOf(t, inv.Link)

	p, err := e.svc.Lookup(ctx, tok, uuid.Nil)
	wantErr(t, err, nil)
	if p.Kind != sharing.KindInvite || p.Status != sharing.StatusPending || p.SiteID != site || p.SiteTitle != "Kyoto in April" ||
		p.From != "owner@example.com" || p.Role != rbac.Viewer || p.Email != "friend@example.com" {
		t.Fatalf("lookup = %+v", p)
	}

	// The account that accepts gets the grant, whatever its address.
	b := e.user("someone-else")
	got, err := e.svc.Accept(ctx, tok, who(b))
	wantErr(t, err, nil)
	if got != site || e.role(site, b) != rbac.Viewer {
		t.Fatalf("accept = %q, role %q", got, e.role(site, b))
	}
	// Used: another account cannot; the same one re-accepting is a no-op.
	c := e.user("third")
	_, err = e.svc.Accept(ctx, tok, who(c))
	wantErr(t, err, sharing.ErrLinkUnusable)
	if e.role(site, c) != "" {
		t.Fatal("second account got a role")
	}
	got, err = e.svc.Accept(ctx, tok, who(b))
	wantErr(t, err, nil)
	if got != site {
		t.Fatalf("re-accept = %q", got)
	}
	// Lookup hides details from anyone but the accepting account.
	p, _ = e.svc.Lookup(ctx, tok, c)
	if p.Status != sharing.StatusAccepted || p.SiteID != "" || p.AcceptedByYou {
		t.Fatalf("lookup by other = %+v", p)
	}
	p, _ = e.svc.Lookup(ctx, tok, b)
	if !p.AcceptedByYou || p.SiteID != site {
		t.Fatalf("lookup by acceptor = %+v", p)
	}
	_, err = e.svc.Lookup(ctx, "not-a-token", uuid.Nil)
	wantErr(t, err, sharing.ErrLinkUnusable)
	_, err = e.svc.Accept(ctx, "not-a-token", who(b))
	wantErr(t, err, sharing.ErrLinkUnusable)
}

func TestInviteExpiryRevokeAndReinvite(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	u := e.user("late")

	expired, err := e.svc.Invite(ctx, site, e.owner, "late@example.com", rbac.Viewer, false)
	wantErr(t, err, nil)
	if left := time.Until(expired.ExpiresAt); left < e.d.Config.InviteTTL-time.Minute || left > e.d.Config.InviteTTL {
		t.Fatalf("invite expires in %v, want INVITE_TTL %v", left, e.d.Config.InviteTTL)
	}
	e.exec(`UPDATE invites SET expires_at = now() - interval '1 second' WHERE id = $1`, expired.ID)
	_, err = e.svc.Accept(ctx, tokenOf(t, expired.Link), who(u))
	wantErr(t, err, sharing.ErrLinkUnusable)
	p, _ := e.svc.Lookup(ctx, tokenOf(t, expired.Link), u)
	if p.Status != sharing.StatusExpired || p.SiteID != "" {
		t.Fatalf("expired lookup = %+v", p)
	}

	revoked, err := e.svc.Invite(ctx, site, e.owner, "late@example.com", rbac.Viewer, false)
	wantErr(t, err, nil)
	_, pending, _ := e.svc.Members(ctx, site, e.owner)
	if len(pending) != 1 || pending[0].ID != revoked.ID {
		t.Fatalf("pending = %+v", pending)
	}
	wantErr(t, e.svc.Revoke(ctx, site, e.owner, revoked.ID), nil)
	_, err = e.svc.Accept(ctx, tokenOf(t, revoked.Link), who(u))
	wantErr(t, err, sharing.ErrLinkUnusable)
	p, _ = e.svc.Lookup(ctx, tokenOf(t, revoked.Link), u)
	if p.Status != sharing.StatusRevoked {
		t.Fatalf("revoked lookup = %+v", p)
	}
	wantErr(t, e.svc.Revoke(ctx, site, e.owner, revoked.ID), sharing.ErrNoSuchMember)

	// Re-inviting a pending address replaces its link.
	first, _ := e.svc.Invite(ctx, site, e.owner, "late@example.com", rbac.Viewer, false)
	second, err := e.svc.Invite(ctx, site, e.owner, "late@example.com", rbac.Manager, false)
	wantErr(t, err, nil)
	_, err = e.svc.Accept(ctx, tokenOf(t, first.Link), who(u))
	wantErr(t, err, sharing.ErrLinkUnusable)
	_, err = e.svc.Accept(ctx, tokenOf(t, second.Link), who(u))
	wantErr(t, err, nil)
	if e.role(site, u) != rbac.Manager {
		t.Fatalf("role = %q", e.role(site, u))
	}
	_, pending, _ = e.svc.Members(ctx, site, e.owner)
	if len(pending) != 0 {
		t.Fatalf("pending after accept = %+v", pending)
	}
}

func TestInviteRules(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	_, err := e.svc.Invite(ctx, site, e.owner, "OWNER@example.com", rbac.Viewer, false)
	wantInputError(t, err)
	_, err = e.svc.Invite(ctx, site, e.owner, "not an email", rbac.Viewer, false)
	wantInputError(t, err)
	_, err = e.svc.Invite(ctx, site, e.owner, "Name <x@example.com>", rbac.Viewer, false)
	wantInputError(t, err)
	_, err = e.svc.Invite(ctx, site, e.owner, "x@example.com", rbac.Owner, false)
	wantInputError(t, err)
	_, err = e.svc.Invite(ctx, "nosuchsite22", e.owner, "x@example.com", rbac.Viewer, false)
	wantErr(t, err, sharing.ErrNotFound)

	// An existing member (by verified address) changes roles with SetRole.
	q := db.New(e.d.DB)
	v := sitestest.User(t, e.d, "member@example.com")
	sitestest.Member(t, e.d, site, v, "viewer")
	_, err = e.svc.Invite(ctx, site, e.owner, "member@example.com", rbac.Manager, false)
	wantErr(t, err, sharing.ErrAlreadyMember)
	if n, _ := q.ListPendingInvites(ctx, site); len(n) != 0 {
		t.Fatal("rejected invite left a row")
	}
}

func TestAutoBindingOnlyForVerifiedEmails(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	verified := sitestest.User(t, e.d, "verified@example.com")
	// An unverified claim to a different address: the invite must not bind.
	claimant := e.unverifiedUser("claimed@example.com")
	// An unverified claim to an address another account holds verified:
	// the verified holder is the one bound.
	squatter := e.unverifiedUser("shared@example.com")
	holder := sitestest.User(t, e.d, "shared@example.com")

	inv, err := e.svc.Invite(ctx, site, e.owner, "verified@example.com", rbac.Manager, false)
	wantErr(t, err, nil)
	if !inv.Granted || e.role(site, verified) != rbac.Manager {
		t.Fatalf("verified holder not granted: %+v role %q", inv, e.role(site, verified))
	}
	// The link is spent on the holder: they land on the site, nobody else can use it.
	got, err := e.svc.Accept(ctx, tokenOf(t, inv.Link), who(verified))
	wantErr(t, err, nil)
	if got != site {
		t.Fatal(got)
	}
	_, err = e.svc.Accept(ctx, tokenOf(t, inv.Link), who(e.user("forwarded")))
	wantErr(t, err, sharing.ErrLinkUnusable)
	if _, pending, _ := e.svc.Members(ctx, site, e.owner); len(pending) != 0 {
		t.Fatalf("auto-bound invite still pending: %+v", pending)
	}

	inv, err = e.svc.Invite(ctx, site, e.owner, "claimed@example.com", rbac.Viewer, false)
	wantErr(t, err, nil)
	if inv.Granted || e.role(site, claimant) != "" {
		t.Fatalf("unverified claim was granted: %+v", inv)
	}
	inv2, err := e.svc.Invite(ctx, site, e.owner, "shared@example.com", rbac.Viewer, false)
	wantErr(t, err, nil)
	if !inv2.Granted || e.role(site, holder) != rbac.Viewer || e.role(site, squatter) != "" {
		t.Fatalf("shared address: %+v holder %q squatter %q", inv2, e.role(site, holder), e.role(site, squatter))
	}
	// The unverified claimant still gets in by accepting the link.
	_, err = e.svc.Accept(ctx, tokenOf(t, inv.Link), who(claimant))
	wantErr(t, err, nil)
	if e.role(site, claimant) != rbac.Viewer {
		t.Fatal("link acceptance did not grant")
	}
}

func TestAcceptNeverDowngrades(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	mgr := e.member(site, rbac.Manager)
	viewer := e.member(site, rbac.Viewer)
	accept := func(u uuid.UUID, role rbac.Role) {
		t.Helper()
		inv, err := e.svc.Invite(ctx, site, e.owner, uuid.NewString()[:8]+"@example.net", role, false)
		wantErr(t, err, nil)
		_, err = e.svc.Accept(ctx, tokenOf(t, inv.Link), who(u))
		wantErr(t, err, nil)
	}
	accept(mgr, rbac.Viewer)
	accept(e.owner, rbac.Viewer)
	accept(viewer, rbac.Manager)
	if e.role(site, mgr) != rbac.Manager || e.role(site, e.owner) != rbac.Owner || e.role(site, viewer) != rbac.Manager {
		t.Fatalf("roles: mgr %q owner %q viewer %q", e.role(site, mgr), e.role(site, e.owner), e.role(site, viewer))
	}
}

func TestConcurrentAcceptsOneWins(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	inv, err := e.svc.Invite(ctx, site, e.owner, "race@example.com", rbac.Viewer, false)
	wantErr(t, err, nil)
	tok := tokenOf(t, inv.Link)
	const n = 8
	users := make([]uuid.UUID, n)
	for i := range users {
		users[i] = e.user("racer")
	}
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() { _, errs[i] = e.svc.Accept(ctx, tok, who(users[i])) })
	}
	wg.Wait()
	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
			if e.role(site, users[i]) != rbac.Viewer {
				t.Error("winner has no role")
			}
		case errors.Is(err, sharing.ErrLinkUnusable):
			if e.role(site, users[i]) != "" {
				t.Error("loser has a role")
			}
		default:
			t.Errorf("accept: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d accepts won, want 1", wins)
	}
}

func TestOwnershipTransfer(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	viewer := e.member(site, rbac.Viewer)
	q := db.New(e.d.DB)

	_, err := e.svc.StartTransfer(ctx, site, e.owner, "owner@example.com", false)
	wantInputError(t, err)
	tr, err := e.svc.StartTransfer(ctx, site, e.owner, "heir@example.com", false)
	wantErr(t, err, nil)
	if !strings.HasPrefix(tr.Link, sitestest.AppOrigin+"/invite#token=") || tr.Email != "heir@example.com" {
		t.Fatalf("transfer = %+v", tr)
	}
	pending, err := e.svc.PendingTransfer(ctx, site, e.owner)
	wantErr(t, err, nil)
	if pending == nil || pending.Email != "heir@example.com" || pending.Link != "" {
		t.Fatalf("pending = %+v", pending)
	}
	p, err := e.svc.Lookup(ctx, tokenOf(t, tr.Link), uuid.Nil)
	wantErr(t, err, nil)
	if p.Kind != sharing.KindTransfer || p.Role != rbac.Owner || p.SiteTitle != "Kyoto in April" || p.From != "owner@example.com" {
		t.Fatalf("lookup = %+v", p)
	}

	// The owner opening their own transfer link changes nothing.
	_, err = e.svc.Accept(ctx, tokenOf(t, tr.Link), who(e.owner))
	wantErr(t, err, sharing.ErrAlreadyOwner)

	// A viewer, signed in under any address, accepts: roles swap at once.
	got, err := e.svc.Accept(ctx, tokenOf(t, tr.Link), who(viewer))
	wantErr(t, err, nil)
	if got != site || e.role(site, viewer) != rbac.Owner || e.role(site, e.owner) != rbac.Manager {
		t.Fatalf("after transfer: new %q old %q", e.role(site, viewer), e.role(site, e.owner))
	}
	s, _ := e.sites.Get(ctx, site)
	if s.OwnerID != viewer {
		t.Fatalf("sites.owner_id = %v", s.OwnerID)
	}
	ms, _ := q.ListMembers(ctx, site)
	owners := 0
	for _, m := range ms {
		if m.Role == "owner" {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("%d owners", owners)
	}
	// The old owner is now a manager: no more owner actions.
	_, err = e.svc.SetPublicLink(ctx, site, e.owner, sharing.LinkEnable)
	wantErr(t, err, sharing.ErrForbidden)
	_, err = e.svc.StartTransfer(ctx, site, e.owner, "back@example.com", false)
	wantErr(t, err, sharing.ErrForbidden)
	// Single use: a re-accept by the new owner is a no-op, others fail.
	_, err = e.svc.Accept(ctx, tokenOf(t, tr.Link), who(viewer))
	wantErr(t, err, nil)
	_, err = e.svc.Accept(ctx, tokenOf(t, tr.Link), who(e.user("late")))
	wantErr(t, err, sharing.ErrLinkUnusable)

	// A transfer to an account with no role gives it ownership too.
	stranger := e.user("stranger")
	tr, err = e.svc.StartTransfer(ctx, site, viewer, "stranger@example.com", false)
	wantErr(t, err, nil)
	_, err = e.svc.Accept(ctx, tokenOf(t, tr.Link), who(stranger))
	wantErr(t, err, nil)
	if e.role(site, stranger) != rbac.Owner || e.role(site, viewer) != rbac.Manager {
		t.Fatal("second transfer did not swap")
	}
}

func TestTransferCancelReplaceAndExpiry(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	u := e.user("heir")

	tr, _ := e.svc.StartTransfer(ctx, site, e.owner, "heir@example.com", false)
	wantErr(t, e.svc.CancelTransfer(ctx, site, e.owner), nil)
	if p, _ := e.svc.PendingTransfer(ctx, site, e.owner); p != nil {
		t.Fatal("transfer still pending after cancel")
	}
	_, err := e.svc.Accept(ctx, tokenOf(t, tr.Link), who(u))
	wantErr(t, err, sharing.ErrLinkUnusable)
	wantErr(t, e.svc.CancelTransfer(ctx, site, e.owner), nil) // nothing pending: no-op

	old, _ := e.svc.StartTransfer(ctx, site, e.owner, "heir@example.com", false)
	replaced, _ := e.svc.StartTransfer(ctx, site, e.owner, "other@example.com", false)
	_, err = e.svc.Accept(ctx, tokenOf(t, old.Link), who(u))
	wantErr(t, err, sharing.ErrLinkUnusable)

	e.exec(`UPDATE ownership_transfers SET expires_at = now() - interval '1 second' WHERE site_id = $1 AND revoked_at IS NULL`, site)
	_, err = e.svc.Accept(ctx, tokenOf(t, replaced.Link), who(u))
	wantErr(t, err, sharing.ErrLinkUnusable)
	if p, _ := e.svc.Lookup(ctx, tokenOf(t, replaced.Link), u); p.Status != sharing.StatusExpired {
		t.Fatalf("lookup = %+v", p)
	}
	if e.role(site, e.owner) != rbac.Owner || e.role(site, u) != "" {
		t.Fatal("a failed transfer changed roles")
	}
}

func TestPublicLinkLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	state := func() sharing.PublicLink {
		t.Helper()
		pl, err := e.svc.PublicLinkState(ctx, site, e.owner)
		wantErr(t, err, nil)
		return pl
	}
	access := func() sites.Access {
		t.Helper()
		a, err := e.sites.Access(ctx, site, uuid.Nil)
		wantErr(t, err, nil)
		return a
	}
	if pl := state(); pl.Enabled || pl.UpdatedAt != nil {
		t.Fatalf("initial = %+v", pl)
	}
	_, err := e.svc.SetPublicLink(ctx, site, e.owner, sharing.LinkRotate)
	wantErr(t, err, sharing.ErrPublicLinkDisabled)
	_, err = e.svc.SetPublicLink(ctx, site, e.owner, "share")
	wantInputError(t, err)

	link, err := e.svc.SetPublicLink(ctx, site, e.owner, sharing.LinkEnable)
	wantErr(t, err, nil)
	if want := "http://" + site + "." + sitestest.SitesDomain + ":8084/_auth/share/"; !strings.HasPrefix(link, want) {
		t.Fatalf("link = %q, want prefix %q", link, want)
	}
	if a := access(); !a.PublicLinkEnabled || a.PublicLinkGen != 1 || !state().Enabled {
		t.Fatalf("after enable: %+v", a)
	}
	_, err = e.svc.SetPublicLink(ctx, site, e.owner, sharing.LinkEnable)
	wantErr(t, err, sharing.ErrPublicLinkEnabled)

	rotated, err := e.svc.SetPublicLink(ctx, site, e.owner, sharing.LinkRotate)
	wantErr(t, err, nil)
	if rotated == link || access().PublicLinkGen != 2 {
		t.Fatalf("rotate: %q gen %d", rotated, access().PublicLinkGen)
	}
	off, err := e.svc.SetPublicLink(ctx, site, e.owner, sharing.LinkDisable)
	wantErr(t, err, nil)
	if off != "" || access().PublicLinkEnabled || state().Enabled {
		t.Fatal("disable did not turn the link off")
	}
	_, err = e.svc.SetPublicLink(ctx, site, e.owner, sharing.LinkDisable)
	wantErr(t, err, nil)
	again, err := e.svc.SetPublicLink(ctx, site, e.owner, sharing.LinkEnable)
	wantErr(t, err, nil)
	if again == rotated || access().PublicLinkGen != 3 {
		t.Fatal("re-enable must issue a new token and generation")
	}
}

func TestNotifications(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	site := e.site()
	inv, err := e.svc.Invite(ctx, site, e.owner, "guest@example.com", rbac.Viewer, true)
	wantErr(t, err, nil)
	if !inv.Emailed || len(e.mail.sent) != 1 {
		t.Fatalf("emailed %v, sent %d", inv.Emailed, len(e.mail.sent))
	}
	m := e.mail.sent[0]
	if m.To != "guest@example.com" || !strings.Contains(m.Subject, "Kyoto in April") ||
		!strings.Contains(m.Text, inv.Link) || !strings.Contains(m.Text, "owner@example.com") || !strings.Contains(m.HTML, "Open invitation") {
		t.Fatalf("message = %+v", m)
	}
	if _, err := e.svc.Invite(ctx, site, e.owner, "quiet@example.com", rbac.Viewer, false); err != nil || len(e.mail.sent) != 1 {
		t.Fatal("notify=false sent mail")
	}
	tr, err := e.svc.StartTransfer(ctx, site, e.owner, "heir@example.com", true)
	wantErr(t, err, nil)
	if !tr.Emailed || len(e.mail.sent) != 2 || !strings.Contains(e.mail.sent[1].Text, tr.Link) {
		t.Fatal("transfer not emailed")
	}

	// Without SMTP the link is still returned; nothing is sent.
	e.svc.SetMailer(mail.New(nil))
	inv, err = e.svc.Invite(ctx, site, e.owner, "nosmtp@example.com", rbac.Viewer, true)
	wantErr(t, err, nil)
	if inv.Emailed || inv.Link == "" {
		t.Fatalf("without SMTP: %+v", inv)
	}
}
