package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ferrule-io/handbill/internal/auth"
	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/token"
)

// authorize is GET /oauth/authorize (plan §4.3). Every error goes to the
// consent page's error card, never to the redirect URI: the user has not
// seen the consent screen yet, and a client can register any https
// redirect URI (RFC 9700 §4.11.1). The codes are OAuth error codes.
func (m *module) authorize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	q := r.URL.Query()
	fail := func(code string) {
		http.Redirect(w, r, "/consent?error="+url.QueryEscape(code), http.StatusSeeOther)
	}

	// Phase A: the client and its redirect URI.
	clientID, ok := single(q, "client_id")
	if !ok || clientID == "" || len(clientID) > maxClientIDLen {
		fail(errInvalidClient)
		return
	}
	client, code := m.resolveClient(r.Context(), clientID)
	if code != "" {
		fail(code)
		return
	}
	redirectURI, ok := single(q, "redirect_uri")
	if !ok || len(redirectURI) > maxRedirectURILen || !matchRedirect(client.RedirectUris, redirectURI) {
		fail(errInvalidRedirectURI)
		return
	}

	// Phase B: the rest of the request.
	for _, k := range []string{"response_type", "code_challenge", "code_challenge_method", "resource", "scope", "state", "response_mode"} {
		if len(q[k]) > 1 {
			fail("invalid_request")
			return
		}
	}
	var state *string // returned to the client after the consent decision
	if s, ok := single(q, "state"); ok {
		if len(s) > maxStateLen {
			fail("invalid_request")
			return
		}
		state = &s
	}
	if q.Has("request") || q.Has("request_uri") {
		fail("request_not_supported")
		return
	}
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type")
		return
	}
	if rm := q.Get("response_mode"); q.Has("response_mode") && rm != "query" {
		fail("invalid_request")
		return
	}
	challenge := q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || !validChallenge(challenge) {
		fail("invalid_request")
		return
	}
	if q.Has("resource") && !sameResource(q.Get("resource"), Resource(m.d.Config)) {
		fail("invalid_target")
		return
	}
	if client.Registration == registrationDCR && !m.d.Config.OAuthDCREnabled {
		fail("unauthorized_client")
		return
	}
	if !slices.Contains(effectiveGrantTypes(client.Metadata), grantAuthorizationCode) {
		fail("unauthorized_client")
		return
	}
	scopes := keptScopes(q.Get("scope"))

	// Phase C: the user.
	user, ok := identity.FromContext(r.Context())
	if !ok {
		auth.RedirectToLogin(w, r)
		return
	}

	// Phase D: store the request for the consent page.
	plaintext, hash := token.New()
	err := db.InTx(r.Context(), m.d.DB, func(tq *db.Queries) error {
		if err := tq.DeleteExpiredAuthorizationRequests(r.Context(), user.ID); err != nil {
			return err
		}
		_, err := tq.CreateAuthorizationRequest(r.Context(), db.CreateAuthorizationRequestParams{
			ID:            uuid.Must(uuid.NewV7()),
			TokenHash:     hash,
			UserID:        user.ID,
			ClientID:      client.ID,
			RedirectUri:   redirectURI,
			State:         state,
			CodeChallenge: challenge,
			Resource:      Resource(m.d.Config),
			Scopes:        scopes,
			ExpiresAt:     m.now().Add(requestTTL),
		})
		return err
	})
	if err != nil {
		m.d.Logger.Error("oauth: store authorization request", "err", err)
		fail("server_error")
		return
	}
	http.Redirect(w, r, "/consent?request="+url.QueryEscape(plaintext), http.StatusSeeOther)
}

// resolveClient finds the client for a client_id: a CIMD document URL, or
// a dynamically registered id. Registered DCR clients resolve even when
// registration is disabled, so authorize can answer unauthorized_client.
func (m *module) resolveClient(ctx context.Context, clientID string) (db.OauthClient, string) {
	if isCIMDClientID(clientID) {
		return m.resolveCIMDClient(ctx, clientID)
	}
	c, err := db.New(m.d.DB).GetClientByClientID(ctx, clientID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && c.Registration != registrationDCR) {
		return db.OauthClient{}, errInvalidClient
	}
	if err != nil {
		m.d.Logger.Error("oauth: load client", "err", err)
		return db.OauthClient{}, errClientMetadataUnavailable
	}
	return c, ""
}

// single returns a parameter that must appear at most once; ok is false
// when it is absent or repeated.
func single(v url.Values, k string) (string, bool) {
	if len(v[k]) != 1 {
		return "", false
	}
	return v[k][0], true
}

// sameResource compares a requested resource with ours after lowercasing
// the scheme and host.
func sameResource(requested, ours string) bool {
	u, err := url.Parse(requested)
	if err != nil {
		return false
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String() == ours
}

// keptScopes keeps offline_access, the only scope recorded, and drops the
// rest: handbill has no scopes.
func keptScopes(scope string) []string {
	if slices.Contains(strings.Fields(scope), offlineAccess) {
		return []string{offlineAccess}
	}
	return []string{}
}
