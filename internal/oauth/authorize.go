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

	"github.com/mjquinlan2000/agent-dash/internal/auth"
	"github.com/mjquinlan2000/agent-dash/internal/db"
	"github.com/mjquinlan2000/agent-dash/internal/identity"
	"github.com/mjquinlan2000/agent-dash/internal/token"
)

// authorize is GET /oauth/authorize (plan §4.3). Errors that cannot be
// trusted to the redirect URI go to the consent page's error card; the
// rest are redirected to the client with error, state, and iss.
func (m *module) authorize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	q := r.URL.Query()

	// Phase A: errors that must not redirect.
	consentError := func(code string) {
		http.Redirect(w, r, "/consent?error="+url.QueryEscape(code), http.StatusSeeOther)
	}
	clientID, ok := single(q, "client_id")
	if !ok || clientID == "" || len(clientID) > maxClientIDLen {
		consentError(errInvalidClient)
		return
	}
	client, code := m.resolveClient(r.Context(), clientID)
	if code != "" {
		consentError(code)
		return
	}
	redirectURI, ok := single(q, "redirect_uri")
	if !ok || len(redirectURI) > maxRedirectURILen || !matchRedirect(client.RedirectUris, redirectURI) {
		consentError(errInvalidRedirectURI)
		return
	}

	// Phase B: errors redirected to the client.
	iss := Issuer(m.d.Config)
	// state is echoed only when it appears once and within the limit.
	var state *string
	if s, ok := single(q, "state"); ok && len(s) <= maxStateLen {
		state = &s
	}
	fail := func(code, description string) {
		http.Redirect(w, r, errorRedirectURL(redirectURI, iss, code, description, state), http.StatusFound)
	}
	for _, k := range []string{"response_type", "code_challenge", "code_challenge_method", "resource", "scope", "state", "response_mode"} {
		if len(q[k]) > 1 {
			fail("invalid_request", "The "+k+" parameter must not be repeated.")
			return
		}
	}
	if q.Has("state") && state == nil {
		fail("invalid_request", "The state parameter is too long.")
		return
	}
	if q.Has("request") || q.Has("request_uri") {
		fail("request_not_supported", "Request objects are not supported.")
		return
	}
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "The response_type must be code.")
		return
	}
	if rm := q.Get("response_mode"); q.Has("response_mode") && rm != "query" {
		fail("invalid_request", "The response_mode must be query.")
		return
	}
	if q.Get("code_challenge_method") != "S256" {
		fail("invalid_request", "code_challenge_method must be S256")
		return
	}
	challenge := q.Get("code_challenge")
	if !validChallenge(challenge) {
		fail("invalid_request", "code_challenge must be a base64url S256 challenge.")
		return
	}
	if q.Has("resource") && !sameResource(q.Get("resource"), Resource(m.d.Config)) {
		fail("invalid_target", "The only resource is "+Resource(m.d.Config)+".")
		return
	}
	if client.Registration == registrationDCR && !m.d.Config.OAuthDCREnabled {
		fail("unauthorized_client", "Dynamically registered clients are disabled.")
		return
	}
	if !slices.Contains(effectiveGrantTypes(client.Metadata), grantAuthorizationCode) {
		fail("unauthorized_client", "The client is not registered for the authorization_code grant.")
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
		fail("server_error", "The authorization request could not be stored. Try again.")
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
// rest: agent-dash has no scopes.
func keptScopes(scope string) []string {
	if slices.Contains(strings.Fields(scope), offlineAccess) {
		return []string{offlineAccess}
	}
	return []string{}
}
