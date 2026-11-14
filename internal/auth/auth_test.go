package auth

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/db"
)

func TestPasskeySignUpAndSignIn(t *testing.T) {
	e := newEnv(t, envOpts{})
	b := e.browser()
	if me := b.me(); me.User != nil || me.Recovery || !me.GoogleEnabled || me.MailEnabled {
		t.Fatalf("signed-out me = %+v", me)
	}
	a := b.signUp(t, "  Viewer@Example.com ")
	me := b.me()
	if me.User == nil || me.User.Email != "viewer@example.com" || me.User.EmailVerified {
		t.Fatalf("after sign-up me = %+v", me.User)
	}
	if got := b.whoami(); got != me.User.ID+" viewer@example.com" {
		t.Fatalf("identity = %q", got)
	}
	var (
		name      string
		signCount int64
		be, bs    bool
	)
	if err := e.pool.QueryRow(t.Context(), "SELECT name, sign_count, backup_eligible, backup_state FROM passkey_credentials").Scan(&name, &signCount, &be, &bs); err != nil {
		t.Fatal(err)
	}
	if name != "Passkey · Chrome on macOS" || signCount != 0 || be || bs {
		t.Fatalf("stored passkey: %q count=%d be=%v bs=%v", name, signCount, be, bs)
	}

	mustStatus(t, "sign out", b.api("POST", "/app-api/auth/signout", nil), 204)
	if b.me().User != nil || b.whoami() != "anonymous" {
		t.Fatal("still signed in after sign out")
	}
	var sessions int
	e.scalar(&sessions, "SELECT count(*) FROM sessions")
	if sessions != 0 {
		t.Fatalf("%d sessions after sign out", sessions)
	}

	// Usernameless: no email, the discoverable credential names the account.
	mustStatus(t, "usernameless sign-in", b.signIn(t, a, ""), 200)
	if got := b.me(); got.User == nil || got.User.ID != me.User.ID {
		t.Fatalf("signed in as %+v", got.User)
	}
	e.scalar(&signCount, "SELECT sign_count FROM passkey_credentials")
	if signCount != 1 {
		t.Fatalf("sign_count = %d after one sign-in", signCount)
	}

	// Email-first fallback, in another browser.
	b2 := e.browser()
	mustStatus(t, "email-first sign-in", b2.signIn(t, a, "VIEWER@example.com"), 200)
	if b2.me().User.ID != me.User.ID {
		t.Fatal("email-first signed in as another account")
	}
	// An unknown address gets a ceremony too (TestEmailFirstDecoy).
	mustStatus(t, "unknown email begin", b2.api("POST", "/app-api/auth/passkey/login/begin", map[string]string{"email": "nobody@example.com"}), 200)
	if res := b2.api("POST", "/app-api/auth/passkey/signup/begin", map[string]string{"email": "not an email"}); res.errorCode(t) != "invalid_email" {
		t.Fatalf("bad email: %s", res.body)
	}

	// A counter that goes backwards flags a cloned key and is refused.
	a.counter = 0
	b3 := e.browser()
	if res := b3.signIn(t, a, ""); res.status != 403 || res.errorCode(t) != "passkey_cloned" {
		t.Fatalf("cloned counter: %d %s", res.status, res.body)
	}
	var clone bool
	e.scalar(&clone, "SELECT clone_warning FROM passkey_credentials")
	if !clone || b3.me().User != nil {
		t.Fatalf("clone_warning=%v, signed in=%v", clone, b3.me().User != nil)
	}
}

func TestPasskeyCeremonyChecks(t *testing.T) {
	e := newEnv(t, envOpts{})
	b := e.browser()

	// A ceremony is single use.
	a := newAuthenticator()
	a.backup = true
	res := b.api("POST", "/app-api/auth/passkey/signup/begin", map[string]string{"email": "synced@example.com"})
	var bg begin
	res.json(t, &bg)
	cred := a.create(t, bg.Options)
	mustStatus(t, "finish", b.api("POST", "/app-api/auth/passkey/signup/finish", map[string]any{"flow": bg.Flow, "credential": cred}), 200)
	if res := b.api("POST", "/app-api/auth/passkey/signup/finish", map[string]any{"flow": bg.Flow, "credential": cred}); res.errorCode(t) != "flow_expired" {
		t.Fatalf("replayed finish: %s", res.body)
	}
	var be, bs bool
	if err := e.pool.QueryRow(t.Context(), "SELECT backup_eligible, backup_state FROM passkey_credentials").Scan(&be, &bs); err != nil || !be || !bs {
		t.Fatalf("backup flags be=%v bs=%v err=%v", be, bs, err)
	}

	// An assertion made for another origin (a site host) is refused.
	evil := e.browser()
	a.origin = "http://abcdefghijkl.sites.localhost:8083"
	if res := evil.signIn(t, a, ""); res.errorCode(t) != "passkey_failed" {
		t.Fatalf("cross-origin assertion: %d %s", res.status, res.body)
	}
	a.origin = testOrigin

	// Expired ceremonies are refused.
	res = evil.api("POST", "/app-api/auth/passkey/login/begin", map[string]string{})
	res.json(t, &bg)
	e.exec("UPDATE auth_flows SET expires_at = now() - interval '1 second'")
	if res := evil.api("POST", "/app-api/auth/passkey/login/finish", map[string]any{"flow": bg.Flow, "credential": a.get(t, bg.Options)}); res.errorCode(t) != "flow_expired" {
		t.Fatalf("expired flow: %s", res.body)
	}

	// An unregistered credential is refused.
	if res := evil.signIn(t, newAuthenticator(), ""); res.errorCode(t) != "unknown_passkey" {
		t.Fatalf("unknown credential: %s", res.body)
	}
}

func TestManagePasskeys(t *testing.T) {
	e := newEnv(t, envOpts{})
	b := e.browser()
	first := b.signUp(t, "owner@example.com")

	res := b.api("POST", "/app-api/auth/passkey/register/begin", nil)
	mustStatus(t, "register begin", res, 200)
	var bg begin
	res.json(t, &bg)
	var opts struct{ ExcludeCredentials []struct{ ID string } }
	json.Unmarshal(bg.Options, &opts)
	if len(opts.ExcludeCredentials) != 1 || opts.ExcludeCredentials[0].ID != b64.EncodeToString(first.credID) {
		t.Fatalf("excludeCredentials = %+v", opts.ExcludeCredentials)
	}
	second := newAuthenticator()
	res = b.api("POST", "/app-api/auth/passkey/register/finish", map[string]any{"flow": bg.Flow, "credential": second.create(t, bg.Options), "name": "Work laptop"})
	mustStatus(t, "register finish", res, 200)
	var added struct {
		Passkey  passkeyView
		Upgraded bool
	}
	res.json(t, &added)
	if added.Passkey.Name != "Work laptop" || added.Upgraded {
		t.Fatalf("added = %+v", added)
	}

	var sec struct{ Passkeys []passkeyView }
	res = b.api("GET", "/app-api/auth/security", nil)
	mustStatus(t, "security", res, 200)
	res.json(t, &sec)
	if len(sec.Passkeys) != 2 {
		t.Fatalf("passkeys = %+v", sec.Passkeys)
	}
	res = b.api("PATCH", "/app-api/auth/passkeys/"+sec.Passkeys[0].ID.String(), map[string]string{"name": " Phone "})
	mustStatus(t, "rename", res, 200)
	if res := b.api("PATCH", "/app-api/auth/passkeys/"+sec.Passkeys[0].ID.String(), map[string]string{"name": strings.Repeat("x", 65)}); res.errorCode(t) != "invalid_name" {
		t.Fatalf("long name: %s", res.body)
	}

	// Another account cannot touch these passkeys.
	other := e.browser()
	other.signUp(t, "other@example.com")
	if res := other.api("DELETE", "/app-api/auth/passkeys/"+sec.Passkeys[0].ID.String(), nil); res.status != 404 {
		t.Fatalf("foreign delete: %d", res.status)
	}

	mustStatus(t, "delete", b.api("DELETE", "/app-api/auth/passkeys/"+sec.Passkeys[0].ID.String(), nil), 204)
	if res := b.api("DELETE", "/app-api/auth/passkeys/"+sec.Passkeys[1].ID.String(), nil); res.status != 409 || res.errorCode(t) != "last_method" {
		t.Fatalf("delete last: %d %s", res.status, res.body)
	}
	// The removed passkey no longer signs in; the remaining one does.
	if res := e.browser().signIn(t, first, ""); res.errorCode(t) != "unknown_passkey" {
		t.Fatalf("removed passkey: %s", res.body)
	}
	mustStatus(t, "remaining passkey", e.browser().signIn(t, second, ""), 200)
}

var recoveryCodePattern = regexp.MustCompile(`^[A-Z2-7]{4}-[A-Z2-7]{4}-[A-Z2-7]{4}-[A-Z2-7]{4}$`)

func (b *browser) recoveryCodes(t *testing.T) []string {
	t.Helper()
	res := b.api("POST", "/app-api/auth/recovery-codes", nil)
	mustStatus(t, "recovery codes", res, 200)
	var out struct{ Codes []string }
	res.json(t, &out)
	if len(out.Codes) != 10 {
		t.Fatalf("%d codes", len(out.Codes))
	}
	for _, c := range out.Codes {
		if !recoveryCodePattern.MatchString(c) {
			t.Fatalf("code %q", c)
		}
	}
	return out.Codes
}

func TestRecovery(t *testing.T) {
	e := newEnv(t, envOpts{})
	b := e.browser()
	b.signUp(t, "lost@example.com")
	userID := b.me().User.ID
	old := b.recoveryCodes(t)
	codes := b.recoveryCodes(t) // regenerating invalidates the first set
	var sec struct{ RecoveryCodes recoveryView }
	b.api("GET", "/app-api/auth/security", nil).json(t, &sec)
	if sec.RecoveryCodes.Remaining != 10 || sec.RecoveryCodes.IssuedAt == nil {
		t.Fatalf("recovery status %+v", sec.RecoveryCodes)
	}

	// The passkey is gone (new device): sign in with a code.
	r := e.browser()
	recover := func(email, code string) response {
		return r.api("POST", "/app-api/auth/recovery/signin", map[string]string{"email": email, "code": code})
	}
	for _, bad := range []struct{ email, code string }{
		{"lost@example.com", old[0]},
		{"lost@example.com", "AAAA-AAAA-AAAA-AAAA"},
		{"nobody@example.com", codes[0]},
		{"lost@example.com", "garbage"},
	} {
		if res := recover(bad.email, bad.code); res.status != 401 || res.errorCode(t) != "invalid_recovery" {
			t.Fatalf("recovery with %v: %d %s", bad, res.status, res.body)
		}
	}
	// Case, dashes, and spaces don't matter.
	mustStatus(t, "recovery sign-in", recover("lost@example.com", strings.ToLower(strings.ReplaceAll(codes[3], "-", " "))), 200)

	// The restricted session is not a signed-in user anywhere else.
	if me := r.me(); me.User != nil || !me.Recovery {
		t.Fatalf("recovery me = %+v", me)
	}
	if r.whoami() != "anonymous" {
		t.Fatal("restricted session exposed as identity")
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/app-api/auth/security"},
		{"POST", "/app-api/auth/recovery-codes"},
		{"POST", "/app-api/auth/sessions/revoke-others"},
		{"DELETE", "/app-api/auth/google"},
	} {
		if res := r.api(c.method, c.path, nil); res.status != 401 {
			t.Fatalf("restricted %s %s: %d", c.method, c.path, res.status)
		}
	}
	// A used code is spent.
	if res := e.browser().api("POST", "/app-api/auth/recovery/signin", map[string]string{"email": "lost@example.com", "code": codes[3]}); res.status != 401 {
		t.Fatalf("reused code: %d", res.status)
	}

	// Registering a passkey upgrades it to a normal session (a new token).
	restrictedCookie := r.cookies[SessionCookie]
	fresh, res := r.addPasskey(t)
	mustStatus(t, "register from recovery", res, 200)
	var out struct{ Upgraded bool }
	res.json(t, &out)
	if !out.Upgraded || r.cookies[SessionCookie] == restrictedCookie {
		t.Fatalf("upgraded=%v, cookie rotated=%v", out.Upgraded, r.cookies[SessionCookie] != restrictedCookie)
	}
	if me := r.me(); me.User == nil || me.User.ID != userID || me.Recovery {
		t.Fatalf("after upgrade me = %+v", me)
	}
	var restricted int
	e.scalar(&restricted, "SELECT count(*) FROM sessions WHERE restricted")
	if restricted != 0 {
		t.Fatal("restricted session survived the upgrade")
	}
	mustStatus(t, "new passkey signs in", e.browser().signIn(t, fresh, ""), 200)
	b.api("GET", "/app-api/auth/security", nil).json(t, &sec)
	if sec.RecoveryCodes.Remaining != 9 {
		t.Fatalf("remaining = %d", sec.RecoveryCodes.Remaining)
	}

	// A restricted session expires quickly and is never renewed.
	r2 := e.browser()
	mustStatus(t, "second recovery", r2.api("POST", "/app-api/auth/recovery/signin", map[string]string{"email": "lost@example.com", "code": codes[4]}), 200)
	var exp time.Time
	e.scalar(&exp, "SELECT expires_at FROM sessions WHERE restricted")
	if d := time.Until(exp); d > recoveryTTL || d < recoveryTTL-time.Minute {
		t.Fatalf("restricted session lifetime %v", d)
	}
	e.exec("UPDATE sessions SET expires_at = now() - interval '1 second' WHERE restricted")
	if me := r2.me(); me.Recovery {
		t.Fatal("expired restricted session still active")
	}
}

func TestSessionLifetime(t *testing.T) {
	e := newEnv(t, envOpts{})
	b := e.browser()
	b.signUp(t, "s@example.com")
	var exp time.Time
	e.scalar(&exp, "SELECT expires_at FROM sessions")
	if d := time.Until(exp); d < sessionIdle-time.Minute || d > sessionIdle {
		t.Fatalf("new session lifetime %v", d)
	}

	// Use renews the idle deadline, at most once per renewEvery.
	e.exec("UPDATE sessions SET last_seen_at = now() - interval '2 hours', expires_at = now() + interval '1 day'")
	res := b.raw("GET", "/whoami", nil, nil)
	if !strings.Contains(res.header.Get("Set-Cookie"), SessionCookie+"=") {
		t.Fatal("renewal did not refresh the cookie")
	}
	e.scalar(&exp, "SELECT expires_at FROM sessions")
	if time.Until(exp) < sessionIdle-time.Minute {
		t.Fatalf("not renewed: expires in %v", time.Until(exp))
	}
	if res := b.raw("GET", "/whoami", nil, nil); res.header.Get("Set-Cookie") != "" {
		t.Fatal("renewed again within renewEvery")
	}

	// Renewal never passes the absolute lifetime.
	e.exec("UPDATE sessions SET created_at = now() - interval '89 days', last_seen_at = now() - interval '2 hours'")
	b.raw("GET", "/whoami", nil, nil)
	e.scalar(&exp, "SELECT expires_at FROM sessions")
	if d := time.Until(exp); d > 25*time.Hour {
		t.Fatalf("absolute cap exceeded: expires in %v", d)
	}

	// Expired sessions are gone, and the stale cookie is cleared.
	e.exec("UPDATE sessions SET expires_at = now() - interval '1 second'")
	res = b.raw("GET", "/whoami", nil, nil)
	if string(res.body) != "anonymous" {
		t.Fatal("expired session accepted")
	}
	if _, ok := b.cookies[SessionCookie]; ok {
		t.Fatal("stale session cookie not cleared")
	}
}

func TestRevokeSessions(t *testing.T) {
	e := newEnv(t, envOpts{})
	b1 := e.browser()
	a := b1.signUp(t, "many@example.com")
	b2, b3 := e.browser(), e.browser()
	b2.ua = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
	mustStatus(t, "b2", b2.signIn(t, a, ""), 200)
	mustStatus(t, "b3", b3.signIn(t, a, ""), 200)

	var sec struct{ Sessions []sessionView }
	b1.api("GET", "/app-api/auth/security", nil).json(t, &sec)
	if len(sec.Sessions) != 3 {
		t.Fatalf("sessions = %+v", sec.Sessions)
	}
	devices := map[string]bool{}
	current := 0
	for _, s := range sec.Sessions {
		devices[s.Device] = true
		if s.Current {
			current++
		}
	}
	if !devices["Safari on iPhone"] || !devices["Chrome on macOS"] || current != 1 {
		t.Fatalf("session list = %+v", sec.Sessions)
	}

	res := b1.api("POST", "/app-api/auth/sessions/revoke-others", nil)
	mustStatus(t, "revoke others", res, 200)
	var out struct{ Revoked int }
	res.json(t, &out)
	if out.Revoked != 2 || b2.whoami() != "anonymous" || b3.whoami() != "anonymous" || b1.whoami() == "anonymous" {
		t.Fatalf("revoked=%d b1=%q b2=%q", out.Revoked, b1.whoami(), b2.whoami())
	}

	uid, _ := uuid.Parse(b1.me().User.ID)
	if err := RevokeSessions(context.Background(), db.New(e.pool), uid); err != nil {
		t.Fatal(err)
	}
	if b1.whoami() != "anonymous" {
		t.Fatal("RevokeSessions left a session")
	}
}

func TestCSRFAndOrigin(t *testing.T) {
	e := newEnv(t, envOpts{})
	b := e.browser()
	b.signUp(t, "csrf@example.com")
	body := `{}`
	post := func(origin, tok string) response {
		hdr := map[string]string{"Content-Type": "application/json"}
		if origin != "" {
			hdr["Origin"] = origin
		}
		if tok != "" {
			hdr["X-CSRF-Token"] = tok
		}
		return b.raw("POST", "/app-api/auth/sessions/revoke-others", strings.NewReader(body), hdr)
	}
	for _, tc := range []struct {
		name, origin, token, code string
	}{
		{"missing origin", "", b.csrf, "bad_origin"},
		{"wrong origin", "https://evil.example", b.csrf, "bad_origin"},
		{"same-site site host", "http://abc.sites.localhost:8083", b.csrf, "bad_origin"},
		{"real site host", "http://abcdefghijkl.sites.localhost:8083", b.csrf, "bad_origin"},
		{"missing token", testOrigin, "", "bad_csrf"},
		{"wrong token", testOrigin, "x" + b.csrf[1:], "bad_csrf"},
	} {
		if res := post(tc.origin, tc.token); res.status != 403 || res.errorCode(t) != tc.code {
			t.Fatalf("%s: %d %s", tc.name, res.status, res.body)
		}
	}
	// Public endpoints are protected the same way: a site cannot sign the
	// browser out or start a ceremony.
	res := b.raw("POST", "/app-api/auth/signout", nil, map[string]string{"Origin": "http://abc.sites.localhost:8083", "X-CSRF-Token": b.csrf})
	if res.status != 403 || b.whoami() == "anonymous" {
		t.Fatalf("cross-site sign out: %d", res.status)
	}
	mustStatus(t, "valid", post(testOrigin, b.csrf), 200)
}

func TestGoogle(t *testing.T) {
	e := newEnv(t, envOpts{})

	// A new Google account creates an account and goes to its recovery codes.
	b := e.browser()
	loc := location(t, b.googleRoundTrip(t, googleSignIn, "/site?id=abcdefghijkl", fakeGrant{sub: "g-1", email: "Ann@Gmail.com", verified: true}))
	if loc != "/signup/codes?return="+url.QueryEscape("/site?id=abcdefghijkl") {
		t.Fatalf("new account redirect %q", loc)
	}
	me := b.me()
	if me.User == nil || me.User.Email != "ann@gmail.com" || !me.User.EmailVerified {
		t.Fatalf("google sign-up me = %+v", me.User)
	}
	// The same Google account signs into the same handbill account.
	b2 := e.browser()
	if loc := location(t, b2.googleRoundTrip(t, googleSignIn, "", fakeGrant{sub: "g-1", email: "ann@gmail.com", verified: true})); loc != "/" {
		t.Fatalf("returning sign-in redirect %q", loc)
	}
	if b2.me().User.ID != me.User.ID {
		t.Fatal("returning Google sign-in made another account")
	}
	// An unsafe return path is dropped.
	if loc := location(t, e.browser().googleRoundTrip(t, googleSignIn, "//evil.example", fakeGrant{sub: "g-1", email: "ann@gmail.com", verified: true})); loc != "/" {
		t.Fatalf("unsafe return kept: %q", loc)
	}

	// An unverified Google email is an unverified claim.
	b3 := e.browser()
	location(t, b3.googleRoundTrip(t, googleSignIn, "", fakeGrant{sub: "g-2", email: "bob@example.com", verified: false}))
	if u := b3.me().User; u == nil || u.Email != "bob@example.com" || u.EmailVerified {
		t.Fatalf("unverified google me = %+v", u)
	}

	// Displacement: a passkey account claims an address unverified; a new
	// Google account that has it verified takes it.
	squatter := e.browser()
	squatter.signUp(t, "carol@example.com")
	carol := e.browser()
	location(t, carol.googleRoundTrip(t, googleSignIn, "", fakeGrant{sub: "g-3", email: "carol@example.com", verified: true}))
	if u := carol.me().User; u == nil || !u.EmailVerified || u.Email != "carol@example.com" {
		t.Fatalf("carol = %+v", u)
	}
	if u := squatter.me().User; u.Email != "" {
		t.Fatalf("squatter kept the claim: %+v", u)
	}
	// Another new Google account with the same verified address does not
	// get a second account for it.
	if loc := location(t, e.browser().googleRoundTrip(t, googleSignIn, "", fakeGrant{sub: "g-4", email: "carol@example.com", verified: true})); loc != "/login?error=email_in_use" {
		t.Fatalf("duplicate verified email: %q", loc)
	}

	// Bad ID tokens are refused.
	for _, g := range []fakeGrant{
		{sub: "g-5", email: "x@example.com", verified: true, wrongNonce: true},
		{sub: "g-5", email: "x@example.com", verified: true, wrongAudience: true},
	} {
		if loc := location(t, e.browser().googleRoundTrip(t, googleSignIn, "", g)); loc != "/login?error=google_failed" {
			t.Fatalf("bad token %+v: %q", g, loc)
		}
	}

	// The callback only completes in the browser that started the flow.
	victim := e.browser()
	attacker := e.browser()
	res := attacker.api("POST", "/app-api/auth/google/start", map[string]string{"mode": "signin"})
	var start struct{ URL string }
	res.json(t, &start)
	u, _ := url.Parse(start.URL)
	if loc := location(t, victim.raw("GET", "/auth/google/callback?code=x&state="+url.QueryEscape(u.Query().Get("state")), nil, nil)); loc != "/login?error=google_expired" {
		t.Fatalf("foreign state: %q", loc)
	}
	if victim.me().User != nil {
		t.Fatal("login CSRF succeeded")
	}
	// The user declining at Google.
	res = attacker.api("POST", "/app-api/auth/google/start", map[string]string{"mode": "signin"})
	res.json(t, &start)
	u, _ = url.Parse(start.URL)
	if loc := location(t, attacker.raw("GET", "/auth/google/callback?error=access_denied&state="+url.QueryEscape(u.Query().Get("state")), nil, nil)); loc != "/login?error=google_cancelled" {
		t.Fatalf("declined: %q", loc)
	}
}

func TestGoogleLinking(t *testing.T) {
	e := newEnv(t, envOpts{})
	if res := e.browser().api("POST", "/app-api/auth/google/start", map[string]string{"mode": "link"}); res.status != 401 {
		t.Fatalf("link signed out: %d", res.status)
	}

	b := e.browser()
	b.signUp(t, "dana@example.com")
	other := e.browser()
	location(t, other.googleRoundTrip(t, googleSignIn, "", fakeGrant{sub: "g-taken", email: "erin@example.com", verified: true}))

	// A Google account linked elsewhere cannot be linked again.
	if loc := location(t, b.googleRoundTrip(t, googleLink, "", fakeGrant{sub: "g-taken", email: "erin@example.com", verified: true})); loc != "/settings/security?error=google_linked_elsewhere" {
		t.Fatalf("linked elsewhere: %q", loc)
	}
	if loc := location(t, b.googleRoundTrip(t, googleLink, "", fakeGrant{sub: "g-dana", email: "dana@example.com", verified: true})); loc != "/settings/security?linked=google" {
		t.Fatalf("link: %q", loc)
	}
	if u := b.me().User; !u.EmailVerified {
		t.Fatal("verified Google email did not verify the account's address")
	}
	var sec struct{ Google *googleView }
	b.api("GET", "/app-api/auth/security", nil).json(t, &sec)
	if sec.Google == nil || sec.Google.Email != "dana@example.com" {
		t.Fatalf("security google = %+v", sec.Google)
	}
	if loc := location(t, b.googleRoundTrip(t, googleLink, "", fakeGrant{sub: "g-dana-2", email: "dana2@example.com", verified: true})); loc != "/settings/security?error=google_already_linked" {
		t.Fatalf("second link: %q", loc)
	}
	// Signing in with the linked Google account reaches the passkey account.
	g := e.browser()
	location(t, g.googleRoundTrip(t, googleSignIn, "", fakeGrant{sub: "g-dana", email: "dana@example.com", verified: true}))
	if g.me().User.ID != b.me().User.ID {
		t.Fatal("linked Google signed into another account")
	}

	mustStatus(t, "unlink", b.api("DELETE", "/app-api/auth/google", nil), 204)
	if res := b.api("DELETE", "/app-api/auth/google", nil); res.status != 404 {
		t.Fatalf("unlink twice: %d", res.status)
	}
	// Google is the only way into a Google-created account.
	if res := other.api("DELETE", "/app-api/auth/google", nil); res.status != 409 || res.errorCode(t) != "last_method" {
		t.Fatalf("unlink only method: %d %s", res.status, res.body)
	}
}

func TestGoogleDisabled(t *testing.T) {
	e := newEnv(t, envOpts{noGoogle: true})
	b := e.browser()
	if b.me().GoogleEnabled {
		t.Fatal("googleEnabled without configuration")
	}
	if res := b.api("POST", "/app-api/auth/google/start", map[string]string{"mode": "signin"}); res.status != 404 {
		t.Fatalf("start: %d", res.status)
	}
	if loc := location(t, b.raw("GET", "/auth/google/callback?state=x&code=y", nil, nil)); loc != "/login?error=google_disabled" {
		t.Fatalf("callback: %q", loc)
	}
}

func TestEmailVerification(t *testing.T) {
	e := newEnv(t, envOpts{mail: true})
	squatter := e.browser()
	squatter.signUp(t, "fay@example.com")
	b := e.browser()
	if !b.me().MailEnabled {
		t.Fatal("mailEnabled = false")
	}
	b.signUp(t, "fay@example.com")
	if len(e.mail.sent) != 2 {
		t.Fatalf("%d emails sent", len(e.mail.sent))
	}
	m := e.mail.sent[1]
	tok := regexp.MustCompile(`/verify-email#token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(m.Text)
	if m.To != "fay@example.com" || tok == nil {
		t.Fatalf("message = %+v", m)
	}
	// Possession of the link is enough; no session needed.
	anon := e.browser()
	mustStatus(t, "verify", anon.api("POST", "/app-api/auth/email/verify", map[string]string{"token": tok[1]}), 200)
	if res := anon.api("POST", "/app-api/auth/email/verify", map[string]string{"token": tok[1]}); res.errorCode(t) != "invalid_link" {
		t.Fatalf("reused link: %s", res.body)
	}
	if u := b.me().User; !u.EmailVerified {
		t.Fatal("not verified")
	}
	if u := squatter.me().User; u.Email != "" {
		t.Fatal("unverified claim not displaced")
	}
	if res := b.api("POST", "/app-api/auth/email/send-verification", nil); res.errorCode(t) != "already_verified" {
		t.Fatalf("resend verified: %s", res.body)
	}
	// The squatter's earlier link no longer works for an address someone
	// else verified.
	tok0 := regexp.MustCompile(`token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(e.mail.sent[0].Text)
	if res := anon.api("POST", "/app-api/auth/email/verify", map[string]string{"token": tok0[1]}); res.status == 200 {
		t.Fatal("displaced claim verified")
	}

	// Without SMTP everything works and nothing is sent.
	e2 := newEnv(t, envOpts{})
	c := e2.browser()
	c.signUp(t, "gus@example.com")
	if res := c.api("POST", "/app-api/auth/email/send-verification", nil); res.errorCode(t) != "mail_disabled" {
		t.Fatalf("send without SMTP: %s", res.body)
	}
}

func TestSafeReturn(t *testing.T) {
	for s, want := range map[string]bool{
		"/":                    true,
		"/site?id=abc":         true,
		"/oauth/authorize?x=1": true,
		"":                     false,
		"//evil.example":       false,
		"/\\evil.example":      false,
		"https://evil.example": false,
		"site":                 false,
		"/a\nb":                false,
		"/%2F%2Fevil":          true, // a path, not a host
	} {
		if got := SafeReturn(s); got != want {
			t.Errorf("SafeReturn(%q) = %v, want %v", s, got, want)
		}
	}
	r := httptest.NewRequest("GET", "http://dash.localhost/oauth/authorize?client_id=x&state=y", nil)
	if got := LoginURL(r); got != "/login?return="+url.QueryEscape("/oauth/authorize?client_id=x&state=y") {
		t.Fatalf("LoginURL = %q", got)
	}
}

func TestDescribeUserAgent(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36 Edg/140.0": "Edge on Windows",
		"Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0":                                                "Firefox on Linux",
		"Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Mobile Safari/537.36":              "Chrome on Android",
		"curl/8.0": "Unknown device",
	} {
		if got := describeUserAgent(ua); got != want {
			t.Errorf("%q = %q, want %q", ua, got, want)
		}
	}
}

// A recovery sign-in runs argon2id once whatever the address and code: the
// lookup hash finds the one candidate, so many accounts claiming an address
// cannot multiply the work of a guess.
func TestRecoveryVerifiesOnce(t *testing.T) {
	e := newEnv(t, envOpts{})
	var codes [][]string
	for range 3 {
		b := e.browser()
		b.signUp(t, "shared@example.com")
		codes = append(codes, b.recoveryCodes(t))
	}
	other := e.browser()
	other.signUp(t, "other@example.com")
	otherCodes := other.recoveryCodes(t)

	r := e.browser()
	for _, c := range []struct {
		name, email, code string
		status            int
	}{
		{"wrong code", "shared@example.com", "AAAA-AAAA-AAAA-AAAA", 401},
		{"unknown email", "nobody@example.com", codes[0][0], 401},
		{"another account's code", "shared@example.com", otherCodes[0], 401},
		{"right code", "shared@example.com", codes[2][5], 200},
	} {
		e.verifications.Store(0)
		res := r.api("POST", "/app-api/auth/recovery/signin", map[string]string{"email": c.email, "code": c.code})
		if res.status != c.status {
			t.Fatalf("%s: %d %s", c.name, res.status, res.body)
		}
		if n := e.verifications.Load(); n != 1 {
			t.Fatalf("%s: %d argon2id verifications, want 1", c.name, n)
		}
	}
}

// Only a few accounts may claim one address unverified: further passkey
// sign-ups are refused, and a new Google account gets no claim to it.
func TestUnverifiedClaimCap(t *testing.T) {
	e := newEnv(t, envOpts{})
	for range maxUnverifiedEmailClaims {
		e.browser().signUp(t, "popular@example.com")
	}
	res := e.browser().api("POST", "/app-api/auth/passkey/signup/begin", map[string]string{"email": "Popular@example.com"})
	if res.status != 400 || res.errorCode(t) != "email_unavailable" {
		t.Fatalf("sign-up over the cap: %d %s", res.status, res.body)
	}
	g := e.browser()
	location(t, g.googleRoundTrip(t, googleSignIn, "", fakeGrant{sub: "g-cap", email: "popular@example.com", verified: false}))
	if u := g.me().User; u == nil || u.Email != "" {
		t.Fatalf("google account over the cap = %+v", u)
	}
	var claims int
	e.scalar(&claims, "SELECT count(*) FROM emails WHERE email = 'popular@example.com'")
	if claims != maxUnverifiedEmailClaims {
		t.Fatalf("%d claims", claims)
	}
	// A verified holder is not capped: it displaces the claims.
	v := e.browser()
	location(t, v.googleRoundTrip(t, googleSignIn, "", fakeGrant{sub: "g-owner", email: "popular@example.com", verified: true}))
	if u := v.me().User; u == nil || u.Email != "popular@example.com" || !u.EmailVerified {
		t.Fatalf("verified holder = %+v", u)
	}
}

// Email-first sign-in for an address without passkeys looks like one with
// them: a stable decoy credential, and a finish that fails like a wrong one.
func TestEmailFirstDecoy(t *testing.T) {
	e := newEnv(t, envOpts{})
	a := e.browser().signUp(t, "real@example.com")
	location(t, e.browser().googleRoundTrip(t, googleSignIn, "", fakeGrant{sub: "g-only", email: "gonly@example.com", verified: true}))

	type allowed struct {
		Type       string
		ID         string
		Transports []string
	}
	begin1 := func(email string) (begin, allowed) {
		t.Helper()
		res := e.browser().api("POST", "/app-api/auth/passkey/login/begin", map[string]string{"email": email})
		mustStatus(t, "begin "+email, res, 200)
		var bg begin
		res.json(t, &bg)
		var o struct{ AllowCredentials []allowed }
		if err := json.Unmarshal(bg.Options, &o); err != nil {
			t.Fatal(err)
		}
		if len(o.AllowCredentials) != 1 {
			t.Fatalf("%s: allowCredentials %+v", email, o.AllowCredentials)
		}
		return bg, o.AllowCredentials[0]
	}
	_, real := begin1("real@example.com")
	for _, email := range []string{"nobody@example.com", "gonly@example.com"} {
		_, d := begin1(email)
		_, again := begin1(email)
		raw, err := b64.DecodeString(d.ID)
		if err != nil || len(raw) != 16 || d.Type != real.Type || again.ID != d.ID || d.ID == real.ID ||
			strings.Join(d.Transports, ",") != "internal,hybrid" {
			t.Fatalf("%s decoy %+v (again %+v), real %+v", email, d, again, real)
		}
	}
	// A real credential presented to a decoy flow fails like an unknown one,
	// rather than signing in usernameless.
	b := e.browser()
	bg, _ := begin1("nobody@example.com")
	var opts map[string]any
	if err := json.Unmarshal(bg.Options, &opts); err != nil {
		t.Fatal(err)
	}
	delete(opts, "allowCredentials")
	stripped, _ := json.Marshal(opts)
	res := b.api("POST", "/app-api/auth/passkey/login/finish", map[string]any{"flow": bg.Flow, "credential": a.get(t, stripped)})
	if res.status != 400 || res.errorCode(t) != "unknown_passkey" || b.me().User != nil {
		t.Fatalf("decoy finish: %d %s", res.status, res.body)
	}
}
