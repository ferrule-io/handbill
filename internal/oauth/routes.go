package oauth

import (
	"time"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/safehttp"
	"github.com/ferrule-io/handbill/internal/server"
)

// Options adjust the module; the zero value is production. Exported for
// tests, which point CIMD at a fake TLS host and move the clock.
type Options struct {
	// CIMD fetches client ID metadata documents. nil: a safehttp client
	// with production limits (no loopback or private addresses).
	CIMD *safehttp.Client
	// Now is the clock for lifetimes and CIMD caching; nil: time.Now.
	Now func() time.Time
}

type module struct {
	d    *server.Deps
	cimd *safehttp.Client
	now  func() time.Time
}

// Register installs the authorization server with production options.
var Register = Module(Options{})

// Module returns the registration for o.
func Module(o Options) server.Module {
	return func(r *server.Routes, d *server.Deps) error {
		m := &module{d: d, cimd: o.CIMD, now: o.Now}
		if m.cimd == nil {
			m.cimd = safehttp.New(safehttp.Options{
				Timeout:      5 * time.Second,
				MaxBodyBytes: maxCIMDBytes,
				UserAgent:    "handbill (+" + Issuer(d.Config) + ")",
			})
		}
		if m.now == nil {
			m.now = time.Now
		}

		r.App.HandleFunc("GET /.well-known/oauth-protected-resource", m.protectedResource)
		r.App.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", m.protectedResource)
		r.App.HandleFunc("GET /.well-known/oauth-authorization-server", m.authorizationServer)
		r.App.HandleFunc("GET /oauth/authorize", m.authorize)
		r.App.HandleFunc("POST /oauth/token", m.token)
		r.App.HandleFunc("POST /oauth/revoke", m.revoke)
		r.App.HandleFunc("POST /oauth/register", m.register)

		appapi.Handle(r, d, "GET /app-api/oauth/consent", m.getConsent)
		appapi.Handle(r, d, "POST /app-api/oauth/consent", m.postConsent)
		appapi.Handle(r, d, "GET /app-api/oauth/grants", m.listGrants)
		appapi.Handle(r, d, "DELETE /app-api/oauth/grants/{id}", m.deleteGrant)
		return nil
	}
}
