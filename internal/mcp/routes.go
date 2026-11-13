package mcp

import (
	"net/http"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ferrule-io/handbill/internal/oauth"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/token"
)

// Register serves /mcp on the app host. The handler is stateless (MCP
// 2026-07-28 has no sessions; the SDK also serves 2025-11-25 clients by
// synthesizing the initialized session per request), so any replica can
// answer any request, and each request acts as its own bearer token's
// principal. Responses are JSON, not SSE: the server sends no requests or
// notifications of its own.
func Register(r *server.Routes, d *server.Deps) error {
	srv := NewServer(&Env{Deps: d, Sites: sites.NewService(d), Signer: token.NewSigner(d.Config.SecretKey)})
	h := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return srv }, &mcpsdk.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		DisableLocalhostProtection: true, // see checkOrigin
		MaxRequestBodyBytes:        maxRequestBodyBytes,
	})
	handler := checkOrigin(d.Config, oauth.RequireBearer(d, h))
	// Each method separately: a bare "/mcp" would conflict with webui's
	// "GET /" (neither is more specific). The SDK answers GET and DELETE
	// with 405 in stateless mode.
	r.App.Handle("POST /mcp", handler)
	r.App.Handle("GET /mcp", handler)
	r.App.Handle("DELETE /mcp", handler)
	return nil
}
