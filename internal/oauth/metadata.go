package oauth

import (
	"encoding/json"
	"net/http"
)

// protectedResourceMetadata is RFC 9728 metadata for /mcp. No
// scopes_supported: agent-dash has no scopes.
type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceName           string   `json:"resource_name"`
}

// authServerMetadata is RFC 8414 metadata. The SDK's oauthex.AuthServerMeta
// always emits jwks_uri, so agent-dash uses its own struct.
type authServerMetadata struct {
	Issuer                                     string   `json:"issuer"`
	AuthorizationEndpoint                      string   `json:"authorization_endpoint"`
	TokenEndpoint                              string   `json:"token_endpoint"`
	RevocationEndpoint                         string   `json:"revocation_endpoint"`
	RegistrationEndpoint                       string   `json:"registration_endpoint,omitempty"`
	ResponseTypesSupported                     []string `json:"response_types_supported"`
	ResponseModesSupported                     []string `json:"response_modes_supported"`
	GrantTypesSupported                        []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported          []string `json:"token_endpoint_auth_methods_supported"`
	RevocationEndpointAuthMethodsSupported     []string `json:"revocation_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported              []string `json:"code_challenge_methods_supported"`
	ClientIDMetadataDocumentSupported          bool     `json:"client_id_metadata_document_supported"`
	AuthorizationResponseIssParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
}

func (m *module) protectedResource(w http.ResponseWriter, r *http.Request) {
	writePublicJSON(w, protectedResourceMetadata{
		Resource:               Resource(m.d.Config),
		AuthorizationServers:   []string{Issuer(m.d.Config)},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "agent-dash",
	})
}

func (m *module) authorizationServer(w http.ResponseWriter, r *http.Request) {
	iss := Issuer(m.d.Config)
	md := authServerMetadata{
		Issuer:                                     iss,
		AuthorizationEndpoint:                      iss + "/oauth/authorize",
		TokenEndpoint:                              iss + "/oauth/token",
		RevocationEndpoint:                         iss + "/oauth/revoke",
		ResponseTypesSupported:                     []string{"code"},
		ResponseModesSupported:                     []string{"query"},
		GrantTypesSupported:                        []string{grantAuthorizationCode, grantRefreshToken},
		TokenEndpointAuthMethodsSupported:          []string{authMethodNone},
		RevocationEndpointAuthMethodsSupported:     []string{authMethodNone},
		CodeChallengeMethodsSupported:              []string{"S256"},
		ClientIDMetadataDocumentSupported:          true,
		AuthorizationResponseIssParameterSupported: true,
	}
	if m.d.Config.OAuthDCREnabled {
		md.RegistrationEndpoint = iss + "/oauth/register"
	}
	writePublicJSON(w, md)
}

// writePublicJSON writes cacheable metadata.
func writePublicJSON(w http.ResponseWriter, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(v)
}
