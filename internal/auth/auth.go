// Package auth implements accounts and sessions (docs/design.md#accounts):
// passkey (WebAuthn) and Google OIDC sign-up and sign-in, recovery codes,
// optional email verification, and the __Host-session cookie that the
// session middleware turns into identity.User for every other package.
//
// Every endpoint lives under /app-api/auth/ (JSON, via appapi) except the
// Google redirect target /auth/google/callback.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/db"
	mailer "github.com/ferrule-io/handbill/internal/mail"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/token"
)

// googleIssuer is Google's OIDC issuer; tests substitute a fake one.
const googleIssuer = "https://accounts.google.com"

type options struct {
	googleIssuer string
	mail         mailer.Sender // nil: from the SMTP configuration
	// verifyRecovery replaces token.VerifyRecoveryCode, so tests can count
	// argon2id verifications.
	verifyRecovery func(encoded, code string) bool
}

type module struct {
	d              *server.Deps
	q              *db.Queries
	wa             *webauthn.WebAuthn
	google         *googleClient // nil when Google sign-in is not configured
	mail           mailer.Sender
	verifyRecovery func(encoded, code string) bool
	recoveryKey    []byte // keys recovery-code lookup hashes
	decoyKey       []byte // keys decoy credential IDs for unknown addresses
}

// Register installs routes and, via r.UseApp, the session middleware that
// loads the __Host-session cookie into identity.WithUser.
func Register(r *server.Routes, d *server.Deps) error {
	return register(r, d, options{googleIssuer: googleIssuer})
}

func register(r *server.Routes, d *server.Deps, o options) error {
	m, err := newModule(d, o)
	if err != nil {
		return err
	}
	r.UseApp(m.sessionMiddleware)
	m.routes(r)
	return nil
}

func newModule(d *server.Deps, o options) (*module, error) {
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          d.Config.AppHost,
		RPDisplayName: "Handbill",
		RPOrigins:     []string{d.Config.AppOrigin.String()},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			// Discoverable credentials let sign-in skip the email field.
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: flowTTL, TimeoutUVD: flowTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: flowTTL, TimeoutUVD: flowTTL},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("auth: webauthn: %w", err)
	}
	m := &module{
		d: d, q: db.New(d.DB), wa: wa, mail: o.mail, verifyRecovery: o.verifyRecovery,
		recoveryKey: token.DeriveKey(d.Config.SecretKey, token.PurposeRecoveryCode),
		decoyKey:    token.DeriveKey(d.Config.SecretKey, token.PurposePasskeyDecoy),
	}
	if m.mail == nil {
		m.mail = mailer.New(d.Config.SMTP)
	}
	if m.verifyRecovery == nil {
		m.verifyRecovery = token.VerifyRecoveryCode
	}
	if g := d.Config.Google; g != nil {
		m.google = newGoogleClient(o.googleIssuer, g.ClientID, g.ClientSecret, d.Config.AppOrigin.String()+"/auth/google/callback")
	}
	return m, nil
}

func (m *module) routes(r *server.Routes) {
	h := func(pattern string, fn appapi.Handler, opts ...appapi.Option) {
		appapi.Handle(r, m.d, pattern, fn, opts...)
	}
	pub := appapi.Public()

	h("GET /app-api/me", m.me, pub)
	h("POST /app-api/auth/signout", m.signOut, pub)

	h("POST /app-api/auth/passkey/signup/begin", m.signupBegin, pub)
	h("POST /app-api/auth/passkey/signup/finish", m.signupFinish, pub)
	h("POST /app-api/auth/passkey/login/begin", m.loginBegin, pub)
	h("POST /app-api/auth/passkey/login/finish", m.loginFinish, pub)
	// Public so a restricted (recovery) session gets through; the handlers
	// require a session of either kind themselves.
	h("POST /app-api/auth/passkey/register/begin", m.registerBegin, pub)
	h("POST /app-api/auth/passkey/register/finish", m.registerFinish, pub)
	h("PATCH /app-api/auth/passkeys/{id}", m.renamePasskey)
	h("DELETE /app-api/auth/passkeys/{id}", m.deletePasskey)

	h("POST /app-api/auth/google/start", m.googleStart, pub)
	h("DELETE /app-api/auth/google", m.googleUnlink)
	r.App.HandleFunc("GET /auth/google/callback", m.googleCallback)

	h("POST /app-api/auth/recovery/signin", m.recoverySignIn, pub)
	h("POST /app-api/auth/recovery-codes", m.regenerateRecoveryCodes)

	h("POST /app-api/auth/email/verify", m.verifyEmail, pub)
	h("POST /app-api/auth/email/send-verification", m.sendVerification)

	h("GET /app-api/auth/security", m.security)
	h("POST /app-api/auth/sessions/revoke-others", m.revokeOtherSessions)
}

// RevokeSessions signs a user out everywhere (e.g. on account deletion).
func RevokeSessions(ctx context.Context, q *db.Queries, userID uuid.UUID) error {
	_, err := q.DeleteUserSessions(ctx, userID)
	return err
}

// SafeReturn reports whether s is a same-origin path to return to after
// sign-in: it must start with a single "/" and contain no backslash or
// control characters, so "//evil.example" and "/\evil.example" are refused.
func SafeReturn(s string) bool {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.ContainsAny(s, "\\") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "" && u.Host == ""
}

// LoginURL returns /login?return=<path and query of r> for pages and
// endpoints that need a signed-in user.
func LoginURL(r *http.Request) string {
	ret := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		ret += "?" + r.URL.RawQuery
	}
	if !SafeReturn(ret) {
		return "/login"
	}
	return "/login?return=" + url.QueryEscape(ret)
}

// RedirectToLogin sends a browser that needs to sign in to LoginURL(r).
func RedirectToLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, LoginURL(r), http.StatusSeeOther)
}

// normalizeEmail lowercases and validates a bare address typed by a user.
func normalizeEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 254 {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" {
		return "", false
	}
	return s, true
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

var (
	errBadRequest = func(code, msg string) error { return appapi.NewError(http.StatusBadRequest, code, msg) }
	errUnauth     = appapi.NewError(http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
)
