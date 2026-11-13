// Package mcp serves agent-dash's MCP tools at /mcp (docs/design.md#mcp-tools)
// with the official Go SDK's stateless Streamable HTTP handler, behind
// oauth.RequireBearer and an Origin check.
//
// Tools live in tools_<area>.go files. Each file's init appends one
// function to toolsets that adds its tools to a server:
//
//	func init() { toolsets = append(toolsets, addSharingTools) }
//
//	func addSharingTools(s *mcpsdk.Server, env *Env) {
//		addTool(s, env, &mcpsdk.Tool{Name: "share_site", ...}, patch, handler)
//	}
//
// Handlers find the caller with oauth.PrincipalFrom (or env.site, which
// also checks the caller's role) and return errors from toolErrorf, which
// the model sees; any other error is logged and reported generically.
package mcp

import (
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjquinlan2000/agent-dash/internal/server"
	"github.com/mjquinlan2000/agent-dash/internal/sites"
	"github.com/mjquinlan2000/agent-dash/internal/token"
)

// Env is what tool handlers receive.
type Env struct {
	Deps   *server.Deps
	Sites  *sites.Service
	Signer *token.Signer
}

// toolsets is appended from tools_<area>.go init functions; NewServer
// applies every entry.
var toolsets []func(s *mcpsdk.Server, env *Env)

// instructions are the server instructions clients show the model.
const instructions = `agent-dash hosts static websites (HTML, CSS, JavaScript, images) that you publish for a person to view at a stable, private URL; no server-side code runs. Viewers sign in to see a site, and only people the owner shares it with can open it. Typical flow: list_sites (reuse an existing site to keep its URL) or create_site -> publish_site for small sites (under ~10 MB per call), or create_deployment -> upload_files / get_upload_url -> finalize_deployment for large ones -> give the user the site's URL. Every publish is kept as a version; rollback_site undoes a bad one.`

// NewServer returns an MCP server with every toolset. Server logging stays
// off: it could include file contents.
func NewServer(env *Env) *mcpsdk.Server {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "agent-dash", Title: "agent-dash", Version: "0.1"},
		&mcpsdk.ServerOptions{Instructions: instructions})
	for _, add := range toolsets {
		add(s, env)
	}
	return s
}
