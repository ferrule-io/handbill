// Package oauth is handbill's OAuth 2.1 authorization server for MCP
// clients (docs/design.md#mcp-authorization): protected-resource and
// authorization-server metadata, /oauth/authorize with the consent screen's
// /app-api/oauth/consent endpoints, /oauth/token, /oauth/revoke, optional
// dynamic client registration, client ID metadata document (CIMD)
// fetching, the grants list in settings, and RequireBearer, which turns an
// access token into the Principal the MCP tools act as.
//
// Every client is public (token_endpoint_auth_method "none") and proves
// possession with PKCE. Codes and tokens are opaque token.New values stored
// as SHA-256 hashes and checked against the database on every request.
package oauth

import "github.com/ferrule-io/handbill/internal/config"

// Issuer is the authorization server's issuer identifier, APP_ORIGIN.
func Issuer(c *config.Config) string { return c.AppOrigin.String() }

// Resource is the one protected resource tokens are issued for, the MCP
// endpoint.
func Resource(c *config.Config) string { return Issuer(c) + "/mcp" }

// ResourceMetadataURL is the path-suffixed protected resource metadata
// document WWW-Authenticate points to (RFC 9728 §3.1).
func ResourceMetadataURL(c *config.Config) string {
	return Issuer(c) + "/.well-known/oauth-protected-resource/mcp"
}
