package oauth

import (
	"encoding/json"
	"net/http"
	"net/url"
)

// oauthError is an OAuth error response (RFC 6749 §5.2): a JSON body from
// the token, revocation, and registration endpoints, or query parameters on
// an authorization redirect.
type oauthError struct {
	status      int
	code        string
	description string
}

func (e *oauthError) Error() string { return e.code + ": " + e.description }

func newError(status int, code, description string) *oauthError {
	return &oauthError{status: status, code: code, description: description}
}

func badRequest(code, description string) *oauthError {
	return newError(http.StatusBadRequest, code, description)
}

// writeJSON writes a JSON response that must never be cached.
func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, e *oauthError) {
	writeJSON(w, e.status, map[string]string{"error": e.code, "error_description": e.description})
}

// redirectURL appends params to a redirect URI, keeping its existing query
// as sent. The URI was validated at registration, so it parses.
func redirectURL(redirectURI string, params url.Values) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	if u.RawQuery != "" {
		u.RawQuery += "&" + params.Encode()
	} else {
		u.RawQuery = params.Encode()
	}
	return u.String()
}

// errorRedirectURL is the authorization error response (OAuth 2.1
// §4.1.2.1) with the RFC 9207 issuer. state is echoed only when valid.
func errorRedirectURL(redirectURI, issuer, code, description string, state *string) string {
	p := url.Values{"error": {code}, "error_description": {description}, "iss": {issuer}}
	if state != nil {
		p.Set("state", *state)
	}
	return redirectURL(redirectURI, p)
}
