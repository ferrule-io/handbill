package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/db"
)

// googleFlowCookie binds a Google round trip to the browser that started
// it: the callback's state must equal it, so a callback URL lured from
// someone else's attempt cannot sign this browser in (login CSRF).
const googleFlowCookie = "__Host-google-flow"

// googleClient discovers the issuer lazily, so startup never depends on
// reaching Google.
type googleClient struct {
	issuer, clientID, clientSecret, redirectURL string

	mu       sync.Mutex
	provider *oidc.Provider
}

func newGoogleClient(issuer, clientID, clientSecret, redirectURL string) *googleClient {
	return &googleClient{issuer: issuer, clientID: clientID, clientSecret: clientSecret, redirectURL: redirectURL}
}

func (g *googleClient) config(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider == nil {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		p, err := oidc.NewProvider(ctx, g.issuer)
		if err != nil {
			return nil, nil, err
		}
		g.provider = p
	}
	cfg := &oauth2.Config{
		ClientID:     g.clientID,
		ClientSecret: g.clientSecret,
		Endpoint:     g.provider.Endpoint(),
		RedirectURL:  g.redirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "email"},
	}
	return cfg, g.provider.Verifier(&oidc.Config{ClientID: g.clientID}), nil
}

const (
	googleSignIn = "signin" // sign in, or sign up when the Google account is new
	googleLink   = "link"   // link to the signed-in account
)

type googleFlow struct {
	Mode     string `json:"mode"`
	Return   string `json:"return,omitempty"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
}

var errGoogleDisabled = appapi.NewError(http.StatusNotFound, "google_disabled", "Google sign-in is not enabled on this server.")

// googleStart begins a round trip and returns the URL to send the browser
// to. It is a CSRF-protected POST so other sites cannot start one.
func (m *module) googleStart(w http.ResponseWriter, r *http.Request) error {
	if m.google == nil {
		return errGoogleDisabled
	}
	var in struct {
		Mode   string `json:"mode"`
		Return string `json:"return"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	f := googleFlow{Mode: in.Mode, Nonce: oauth2.GenerateVerifier(), Verifier: oauth2.GenerateVerifier()}
	var user uuid.NullUUID
	switch in.Mode {
	case googleSignIn:
		if in.Return != "" && SafeReturn(in.Return) {
			f.Return = in.Return
		}
	case googleLink:
		s, err := fullSession(r)
		if err != nil {
			return err
		}
		user = uuid.NullUUID{UUID: s.UserID, Valid: true}
	default:
		return errBadRequest("invalid_mode", `mode must be "signin" or "link".`)
	}
	cfg, _, err := m.google.config(r.Context())
	if err != nil {
		m.d.Logger.Error("google discovery failed", "err", err)
		return appapi.NewError(http.StatusBadGateway, "google_unavailable", "Google sign-in is unavailable right now. Try again shortly.")
	}
	state, err := m.startFlow(r.Context(), flowGoogle, user, f)
	if err != nil {
		return err
	}
	setCookie(w, googleFlowCookie, state, time.Now().Add(flowTTL))
	u := cfg.AuthCodeURL(state,
		oauth2.S256ChallengeOption(f.Verifier),
		oidc.Nonce(f.Nonce),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
	appapi.WriteJSON(w, http.StatusOK, map[string]string{"url": u})
	return nil
}

// googleClaims are the ID token claims handbill uses.
type googleClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

// googleCallback finishes a round trip and redirects to a UI page; errors
// travel as ?error=<code> for the page to explain.
func (m *module) googleCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	fail := func(dest, code string) {
		http.Redirect(w, r, dest+"?error="+url.QueryEscape(code), http.StatusSeeOther)
	}
	if m.google == nil {
		fail("/login", "google_disabled")
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	c, err := r.Cookie(googleFlowCookie)
	clearCookie(w, googleFlowCookie)
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		fail("/login", "google_expired")
		return
	}
	var f googleFlow
	flow, err := consumeFlow(r.Context(), m.q, flowGoogle, state, &f)
	if err != nil {
		fail("/login", "google_expired")
		return
	}
	errDest := "/login"
	if f.Mode == googleLink {
		errDest = "/settings/security"
	}
	if q.Get("error") != "" {
		fail(errDest, "google_cancelled")
		return
	}
	sub, claims, err := m.exchange(r.Context(), q.Get("code"), f)
	if err != nil {
		m.d.Logger.Warn("google sign-in failed", "err", err)
		fail(errDest, "google_failed")
		return
	}

	if f.Mode == googleLink {
		s, ok := sessionFrom(r.Context())
		if !ok || s.Restricted || !flow.UserID.Valid || flow.UserID.UUID != s.UserID {
			fail("/login", "google_expired")
			return
		}
		if code, err := m.linkGoogle(r.Context(), s.UserID, sub, claims); err != nil {
			m.d.Logger.Error("google link failed", "err", err)
			fail(errDest, "google_failed")
		} else if code != "" {
			fail(errDest, code)
		} else {
			http.Redirect(w, r, "/settings/security?linked=google", http.StatusSeeOther)
		}
		return
	}

	dest, code, err := m.googleSignIn(r.Context(), w, r, sub, claims, f.Return)
	switch {
	case err != nil:
		m.d.Logger.Error("google sign-in failed", "err", err)
		fail(errDest, "google_failed")
	case code != "":
		fail(errDest, code)
	default:
		http.Redirect(w, r, dest, http.StatusSeeOther)
	}
}

func (m *module) exchange(ctx context.Context, code string, f googleFlow) (string, googleClaims, error) {
	var claims googleClaims
	cfg, verifier, err := m.google.config(ctx)
	if err != nil {
		return "", claims, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return "", claims, err
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return "", claims, errors.New("token response has no id_token")
	}
	idt, err := verifier.Verify(ctx, raw)
	if err != nil {
		return "", claims, err
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(f.Nonce)) != 1 {
		return "", claims, errors.New("id_token nonce mismatch")
	}
	if err := idt.Claims(&claims); err != nil {
		return "", claims, err
	}
	if idt.Subject == "" {
		return "", claims, errors.New("id_token has no subject")
	}
	if e, ok := normalizeEmail(claims.Email); ok {
		claims.Email = e
	} else {
		claims.Email, claims.EmailVerified = "", false
	}
	return idt.Subject, claims, nil
}

// googleSignIn signs in the account linked to sub, or creates one. It
// returns where to send the browser, or an error code for the UI.
func (m *module) googleSignIn(ctx context.Context, w http.ResponseWriter, r *http.Request, sub string, c googleClaims, ret string) (dest, code string, err error) {
	dest = "/"
	if ret != "" {
		dest = ret
	}
	err = db.InTx(ctx, m.d.DB, func(q *db.Queries) error {
		gi, err := q.GetGoogleIdentityBySub(ctx, sub)
		switch {
		case err == nil:
			if err := q.UpdateGoogleIdentityUse(ctx, db.UpdateGoogleIdentityUseParams{ID: gi.ID, Email: c.Email, EmailVerified: c.EmailVerified}); err != nil {
				return err
			}
			if c.EmailVerified {
				if err := claimVerifiedEmail(ctx, q, gi.UserID, c.Email); err != nil {
					return err
				}
			}
			return m.replaceSession(ctx, q, w, r, gi.UserID, false)
		case !isNoRows(err):
			return err
		}

		// A new Google account creates a new handbill account; linking
		// to an existing one only happens from settings. An address that
		// another account has verified stays with it. An unverified address
		// already claimed by maxUnverifiedEmailClaims accounts is left off:
		// the account starts without an email, and gets it once Google
		// reports it verified (the returning sign-in above).
		claim := c.Email != ""
		if claim {
			if _, err := q.GetVerifiedEmailHolder(ctx, c.Email); err == nil {
				code = "email_in_use"
				return nil
			} else if !isNoRows(err) {
				return err
			}
		}
		if claim && !c.EmailVerified {
			full, err := claimCapReached(ctx, q, c.Email)
			if err != nil {
				return err
			}
			claim = !full
		}
		userID := newID()
		if _, err := q.CreateUser(ctx, userID); err != nil {
			return err
		}
		if claim {
			e, err := q.CreateEmail(ctx, db.CreateEmailParams{ID: newID(), UserID: userID, Email: c.Email, IsPrimary: true})
			if err != nil {
				return err
			}
			if c.EmailVerified {
				if _, err := markVerified(ctx, q, e); err != nil {
					return err
				}
			}
		}
		if _, err := q.CreateGoogleIdentity(ctx, db.CreateGoogleIdentityParams{
			ID: newID(), UserID: userID, Sub: sub, Email: c.Email, EmailVerified: c.EmailVerified,
		}); err != nil {
			return err
		}
		// New accounts see their first recovery codes before anything else.
		dest = "/signup/codes"
		if ret != "" {
			dest += "?return=" + url.QueryEscape(ret)
		}
		return m.replaceSession(ctx, q, w, r, userID, false)
	})
	if code != "" {
		dest = ""
	}
	return dest, code, err
}

// linkGoogle links sub to userID; it returns an error code for the UI when
// the Google account belongs to another account or one is already linked.
func (m *module) linkGoogle(ctx context.Context, userID uuid.UUID, sub string, c googleClaims) (string, error) {
	var code string
	err := db.InTx(ctx, m.d.DB, func(q *db.Queries) error {
		if err := q.LockUser(ctx, userID); err != nil {
			return err
		}
		if gi, err := q.GetGoogleIdentityBySub(ctx, sub); err == nil {
			if gi.UserID != userID {
				code = "google_linked_elsewhere"
			}
			return nil
		} else if !isNoRows(err) {
			return err
		}
		if _, err := q.GetGoogleIdentityForUser(ctx, userID); err == nil {
			code = "google_already_linked"
			return nil
		} else if !isNoRows(err) {
			return err
		}
		if _, err := q.CreateGoogleIdentity(ctx, db.CreateGoogleIdentityParams{
			ID: newID(), UserID: userID, Sub: sub, Email: c.Email, EmailVerified: c.EmailVerified,
		}); err != nil {
			return err
		}
		if c.EmailVerified {
			return claimVerifiedEmail(ctx, q, userID, c.Email)
		}
		return nil
	})
	return code, err
}

func (m *module) googleUnlink(w http.ResponseWriter, r *http.Request) error {
	s, err := fullSession(r)
	if err != nil {
		return err
	}
	err = db.InTx(r.Context(), m.d.DB, func(q *db.Queries) error {
		if err := q.LockUser(r.Context(), s.UserID); err != nil {
			return err
		}
		if _, err := q.GetGoogleIdentityForUser(r.Context(), s.UserID); isNoRows(err) {
			return appapi.NewError(http.StatusNotFound, "not_linked", "No Google account is linked.")
		} else if err != nil {
			return err
		}
		n, err := q.CountSignInMethods(r.Context(), s.UserID)
		if err != nil {
			return err
		}
		if n <= 1 {
			return errLastMethod
		}
		_, err = q.DeleteGoogleIdentity(r.Context(), s.UserID)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
