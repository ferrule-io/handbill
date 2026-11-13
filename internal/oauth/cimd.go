package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mjquinlan2000/agent-dash/internal/db"
	"github.com/mjquinlan2000/agent-dash/internal/safehttp"
)

// Client resolution failures shown on the consent error card (never
// redirected: the redirect URI is not trusted yet).
const (
	errInvalidClient             = "invalid_client"
	errClientMetadataInvalid     = "client_metadata_invalid"
	errClientMetadataUnavailable = "client_metadata_unavailable"
	errInvalidRedirectURI        = "invalid_redirect_uri"
)

const (
	grantAuthorizationCode = "authorization_code"
	grantRefreshToken      = "refresh_token"
	registrationCIMD       = "cimd"
	registrationDCR        = "dcr"
	authMethodNone         = "none"
)

// defaultGrantTypes is a client's effective grant_types when it declares
// none. RFC 7591's authorization_code-only default is deliberately not
// applied: MCP clients rarely declare grant_types and need refresh tokens.
var defaultGrantTypes = []string{grantAuthorizationCode, grantRefreshToken}

// isCIMDClientID reports whether a client_id is a client ID metadata
// document URL rather than a DCR-issued id.
func isCIMDClientID(id string) bool { return strings.HasPrefix(id, "https://") }

// validCIMDURL applies the client identifier URL rules (CIMD-03 §3; plan
// §5.1). The client must send the canonical form: it is never normalized.
func (m *module) validCIMDURL(s string) bool {
	if len(s) > maxClientIDLen || strings.ContainsAny(s, "#?") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.String() != s || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Host != strings.ToLower(u.Host) {
		return false
	}
	if u.Path == "" || u.Path == "/" {
		return false
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	if u.Scheme+"://"+u.Host == Issuer(m.d.Config) {
		return false
	}
	host := strings.ToLower(u.Hostname())
	sites := m.d.Config.SitesDomain
	return host != sites && !strings.HasSuffix(host, "."+sites)
}

// clientMetadata is the subset of a client's metadata agent-dash uses.
type clientMetadata struct {
	name         string
	redirectURIs []string
}

// validateCIMD validates a fetched client ID metadata document (plan
// §5.3). It returns the error code for the consent card on failure.
func validateCIMD(clientID string, body []byte) (clientMetadata, string) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil || doc == nil {
		return clientMetadata{}, errClientMetadataInvalid
	}
	var id string
	if json.Unmarshal(doc["client_id"], &id) != nil || id != clientID {
		return clientMetadata{}, errClientMetadataInvalid
	}
	var name string
	if json.Unmarshal(doc["client_name"], &name) != nil {
		return clientMetadata{}, errClientMetadataInvalid
	}
	name = cleanClientName(name)
	if name == "" {
		return clientMetadata{}, errClientMetadataInvalid
	}
	var uris []string
	if json.Unmarshal(doc["redirect_uris"], &uris) != nil || len(uris) == 0 || len(uris) > maxRedirectURIs {
		return clientMetadata{}, errClientMetadataInvalid
	}
	uris = slices.DeleteFunc(uris, func(u string) bool { return !validRedirectURI(u) })
	if len(uris) == 0 {
		return clientMetadata{}, errClientMetadataInvalid
	}
	if has(doc, "client_secret") || has(doc, "client_secret_expires_at") {
		return clientMetadata{}, errClientMetadataInvalid
	}
	if has(doc, "jwks") && has(doc, "jwks_uri") {
		return clientMetadata{}, errClientMetadataInvalid
	}
	methods := []string{authMethodNone}
	var single string
	hasSingle := has(doc, "token_endpoint_auth_method")
	if hasSingle && json.Unmarshal(doc["token_endpoint_auth_method"], &single) != nil {
		return clientMetadata{}, errClientMetadataInvalid
	}
	switch {
	case has(doc, "token_endpoint_auth_methods_supported"):
		if json.Unmarshal(doc["token_endpoint_auth_methods_supported"], &methods) != nil {
			return clientMetadata{}, errClientMetadataInvalid
		}
		if hasSingle && !slices.Contains(methods, single) {
			return clientMetadata{}, errClientMetadataInvalid
		}
	case hasSingle:
		methods = []string{single}
	}
	for _, m := range methods {
		if m == "client_secret_basic" || m == "client_secret_post" || m == "client_secret_jwt" {
			return clientMetadata{}, errClientMetadataInvalid
		}
	}
	if !slices.Contains(methods, authMethodNone) {
		return clientMetadata{}, errInvalidClient
	}
	if has(doc, "grant_types") {
		var gt []string
		if json.Unmarshal(doc["grant_types"], &gt) != nil || !slices.Contains(gt, grantAuthorizationCode) {
			return clientMetadata{}, errClientMetadataInvalid
		}
	}
	if has(doc, "response_types") {
		var rt []string
		if json.Unmarshal(doc["response_types"], &rt) != nil || !slices.Contains(rt, "code") {
			return clientMetadata{}, errClientMetadataInvalid
		}
	}
	return clientMetadata{name: name, redirectURIs: uris}, ""
}

func has(doc map[string]json.RawMessage, k string) bool {
	_, ok := doc[k]
	return ok
}

// cleanClientName is a client name for display: trimmed, without control
// characters, at most maxClientNameLen runes.
func cleanClientName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxClientNameLen {
		s = strings.TrimSpace(string([]rune(s)[:maxClientNameLen]))
	}
	return s
}

// effectiveGrantTypes reads a stored client's grant_types, defaulting to
// defaultGrantTypes.
func effectiveGrantTypes(metadata []byte) []string {
	var doc struct {
		GrantTypes *[]string `json:"grant_types"`
	}
	if json.Unmarshal(metadata, &doc) != nil || doc.GrantTypes == nil {
		return defaultGrantTypes
	}
	return *doc.GrantTypes
}

// resolveCIMDClient returns the stored client for a CIMD client_id,
// fetching or revalidating its document when the cache has expired (plan
// §5.2). On failure it returns a consent error code.
func (m *module) resolveCIMDClient(ctx context.Context, clientID string) (db.OauthClient, string) {
	if !m.validCIMDURL(clientID) {
		return db.OauthClient{}, errInvalidClient
	}
	q := db.New(m.d.DB)
	row, err := q.GetClientByClientID(ctx, clientID)
	stored := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		m.d.Logger.Error("oauth: load client", "err", err)
		return db.OauthClient{}, errClientMetadataUnavailable
	}
	if stored && row.Registration != registrationCIMD {
		return db.OauthClient{}, errInvalidClient
	}
	if stored && row.CacheExpiresAt != nil && row.CacheExpiresAt.After(m.now()) {
		return row, ""
	}

	hdr := http.Header{"Accept": {"application/json"}}
	if stored && row.Etag != nil {
		hdr.Set("If-None-Match", *row.Etag)
	}
	if stored && row.LastModified != nil {
		hdr.Set("If-Modified-Since", *row.LastModified)
	}
	resp, err := m.cimd.Get(ctx, clientID, hdr)
	if err != nil {
		if errors.Is(err, safehttp.ErrRedirect) || errors.Is(err, safehttp.ErrBlockedAddress) ||
			errors.Is(err, safehttp.ErrScheme) || errors.Is(err, safehttp.ErrTooLarge) {
			return db.OauthClient{}, errClientMetadataInvalid
		}
		return m.transientFailure(row, stored, clientID, err)
	}

	switch {
	case resp.StatusCode == http.StatusNotModified && stored:
		etag, lastMod := cacheValidators(resp.Header, row.Etag, row.LastModified)
		expires := m.cacheExpiry(resp.Header)
		if err := q.RefreshCIMDClientCache(ctx, db.RefreshCIMDClientCacheParams{
			ID: row.ID, CacheExpiresAt: &expires, Etag: etag, LastModified: lastMod,
		}); err != nil {
			m.d.Logger.Error("oauth: refresh client cache", "err", err)
		}
		return row, ""
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return m.transientFailure(row, stored, clientID, fmt.Errorf("status %d", resp.StatusCode))
	case resp.StatusCode != http.StatusOK || !jsonContentType(resp.Header.Get("Content-Type")):
		return db.OauthClient{}, errClientMetadataInvalid
	}

	meta, code := validateCIMD(clientID, resp.Body)
	if code != "" {
		if stored && clientIDMismatch(resp.Body, clientID) {
			if _, err := q.DeleteClientGrants(ctx, row.ID); err != nil {
				m.d.Logger.Error("oauth: delete grants of mismatched client", "err", err)
			}
		}
		return db.OauthClient{}, code
	}
	expires := m.cacheExpiry(resp.Header)
	etag, lastMod := cacheValidators(resp.Header, nil, nil)
	out, err := q.UpsertCIMDClient(ctx, db.UpsertCIMDClientParams{
		ID:                      uuid.Must(uuid.NewV7()),
		ClientID:                clientID,
		Metadata:                resp.Body,
		ClientName:              meta.name,
		RedirectUris:            meta.redirectURIs,
		TokenEndpointAuthMethod: authMethodNone,
		CacheExpiresAt:          &expires,
		Etag:                    etag,
		LastModified:            lastMod,
	})
	if err != nil {
		m.d.Logger.Error("oauth: store client", "err", err)
		return db.OauthClient{}, errClientMetadataUnavailable
	}
	return out, ""
}

// transientFailure keeps using a stored document when its host is
// temporarily unreachable (CIMD-03 §6.1 SHOULD NOT discard).
func (m *module) transientFailure(row db.OauthClient, stored bool, clientID string, err error) (db.OauthClient, string) {
	if stored {
		m.d.Logger.Warn("oauth: client metadata fetch failed; using cached document", "client_id", clientID, "err", err)
		return row, ""
	}
	m.d.Logger.Warn("oauth: client metadata fetch failed", "client_id", clientID, "err", err)
	return db.OauthClient{}, errClientMetadataUnavailable
}

func clientIDMismatch(body []byte, clientID string) bool {
	var doc struct {
		ClientID *string `json:"client_id"`
	}
	return json.Unmarshal(body, &doc) == nil && doc.ClientID != nil && *doc.ClientID != clientID
}

func jsonContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mt == "application/json" || (strings.HasPrefix(mt, "application/") && strings.HasSuffix(mt, "+json"))
}

// cacheValidators returns the response's ETag and Last-Modified verbatim,
// falling back to the stored ones (a 304 may omit them).
func cacheValidators(h http.Header, etag, lastMod *string) (*string, *string) {
	if v := h.Get("ETag"); v != "" {
		etag = &v
	}
	if v := h.Get("Last-Modified"); v != "" {
		lastMod = &v
	}
	return etag, lastMod
}

// cacheExpiry applies the response's Cache-Control (plan §5.4): no-store,
// no-cache, or max-age=0 revalidate on the next authorize; max-age=N is
// clamped to [cimdMinTTL, cimdMaxTTL]; otherwise cimdDefaultTTL. Expires is
// ignored.
func (m *module) cacheExpiry(h http.Header) time.Time {
	now := m.now()
	maxAge := -1
	for _, part := range strings.Split(strings.Join(h.Values("Cache-Control"), ","), ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch strings.ToLower(k) {
		case "no-store", "no-cache":
			return now
		case "max-age":
			n, err := strconv.Atoi(strings.Trim(v, `"`))
			if err == nil && n >= 0 {
				maxAge = min(n, int(cimdMaxTTL/time.Second))
			}
		}
	}
	switch {
	case maxAge == 0:
		return now
	case maxAge > 0:
		return now.Add(min(max(time.Duration(maxAge)*time.Second, cimdMinTTL), cimdMaxTTL))
	}
	return now.Add(cimdDefaultTTL)
}
