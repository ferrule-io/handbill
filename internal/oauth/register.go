package oauth

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/mjquinlan2000/agent-dash/internal/db"
)

// register is POST /oauth/register (RFC 7591), served only with
// OAUTH_DCR_ENABLED=true. Registered clients are public and unverified.
func (m *module) register(w http.ResponseWriter, r *http.Request) {
	if !m.d.Config.OAuthDCREnabled {
		writeError(w, newError(http.StatusNotFound, "invalid_request", "dynamic client registration is disabled"))
		return
	}
	invalid := func(description string) {
		writeError(w, badRequest("invalid_client_metadata", description))
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		invalid("The body must be application/json.")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxFormBytes+1))
	if err != nil || len(body) > maxFormBytes {
		invalid("The body must be at most 16 KiB.")
		return
	}
	var doc map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&doc); err != nil || doc == nil || dec.More() {
		invalid("The body must be a JSON object.")
		return
	}

	var uris []string
	if json.Unmarshal(doc["redirect_uris"], &uris) != nil || len(uris) == 0 || len(uris) > maxRedirectURIs ||
		slices.ContainsFunc(uris, func(u string) bool { return !validRedirectURI(u) }) {
		writeError(w, badRequest("invalid_redirect_uri", "redirect_uris must be 1-20 https or loopback http URIs without fragments."))
		return
	}
	if raw, ok := doc["token_endpoint_auth_method"]; ok {
		var method string
		if json.Unmarshal(raw, &method) != nil || method != authMethodNone {
			invalid("Only public clients (token_endpoint_auth_method none) are supported.")
			return
		}
	}
	grantTypes := defaultGrantTypes
	if raw, ok := doc["grant_types"]; ok {
		grantTypes = nil // never decode into defaultGrantTypes' array
		if json.Unmarshal(raw, &grantTypes) != nil || !slices.Contains(grantTypes, grantAuthorizationCode) ||
			slices.ContainsFunc(grantTypes, func(g string) bool { return g != grantAuthorizationCode && g != grantRefreshToken }) {
			invalid("grant_types must include authorization_code and may add refresh_token.")
			return
		}
	}
	if raw, ok := doc["response_types"]; ok {
		var rt []string
		if json.Unmarshal(raw, &rt) != nil || !slices.Equal(rt, []string{"code"}) {
			invalid(`response_types must be ["code"].`)
			return
		}
	}
	var name string
	if raw, ok := doc["client_name"]; ok {
		if json.Unmarshal(raw, &name) != nil {
			invalid("client_name must be a string.")
			return
		}
		name = cleanClientName(name)
	}

	id := uuid.Must(uuid.NewV7())
	c, err := db.New(m.d.DB).CreateDCRClient(r.Context(), db.CreateDCRClientParams{
		ID: id, Metadata: body, ClientName: name, RedirectUris: uris,
	})
	if err != nil {
		m.d.Logger.Error("oauth: register client", "err", err)
		writeError(w, errServer)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  c.ClientID,
		"client_id_issued_at":        c.CreatedAt.Unix(),
		"client_name":                c.ClientName,
		"redirect_uris":              c.RedirectUris,
		"grant_types":                grantTypes,
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": authMethodNone,
	})
}
