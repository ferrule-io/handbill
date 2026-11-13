// Package server builds the root HTTP handler: it routes by Host to the app
// mux (APP_ORIGIN's host) or the sites mux (<id>.SITES_DOMAIN), answers
// /healthz and /readyz on every non-site host, and 404s unknown hosts.
//
// Feature packages never edit this file. Each exposes a Module that registers
// its own routes on Routes.App and/or Routes.Sites; cmd/agent-dash lists the
// modules, one file per feature.
package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mjquinlan2000/agent-dash/internal/config"
	"github.com/mjquinlan2000/agent-dash/internal/siteid"
	"github.com/mjquinlan2000/agent-dash/internal/storage"
)

// Deps are the shared dependencies handed to every module. Modules construct
// their own services from these; there is no other dependency graph.
type Deps struct {
	Config *config.Config
	Logger *slog.Logger
	// DB is the PostgreSQL pool. It connects lazily; nil only in tests that
	// need no database.
	DB *pgxpool.Pool
	// Storage is the object store; nil until a storage adapter is wired in
	// cmd/agent-dash (workstream W2b).
	Storage storage.Store
}

// Routes are the muxes modules register on. Patterns use Go's ServeMux
// syntax ("GET /auth/site", "POST /oauth/token"); duplicate or conflicting
// patterns panic at startup.
type Routes struct {
	// App serves requests whose Host is APP_ORIGIN's host.
	App *http.ServeMux
	// Sites serves requests for <id>.SITES_DOMAIN; SiteID(r.Context())
	// returns the id, already validated by siteid.Valid.
	Sites *http.ServeMux

	appMW, sitesMW []Middleware
}

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// UseApp wraps every app-host request, e.g. the session middleware that
// puts the signed-in user into the context (see internal/identity).
// Middleware added first runs outermost.
func (r *Routes) UseApp(mw Middleware) { r.appMW = append(r.appMW, mw) }

// UseSites wraps every site-host request. Middleware added first runs outermost.
func (r *Routes) UseSites(mw Middleware) { r.sitesMW = append(r.sitesMW, mw) }

func chain(h http.Handler, mws []Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Module registers one feature's routes. It runs once at startup; an error
// aborts startup.
type Module func(r *Routes, d *Deps) error

// New returns the root handler with every module registered.
func New(d *Deps, modules ...Module) (http.Handler, error) {
	rt := &Routes{App: http.NewServeMux(), Sites: http.NewServeMux()}
	for _, m := range modules {
		if err := m(rt, d); err != nil {
			return nil, err
		}
	}
	h := &root{
		deps:  d,
		app:   chain(rt.App, rt.appMW),
		sites: chain(rt.Sites, rt.sitesMW),
	}
	return logRequests(d.Logger, recoverPanics(d.Logger, h)), nil
}

type siteIDKey struct{}

// SiteID returns the site id of a request on a site host, or "" on any
// other host.
func SiteID(ctx context.Context) string {
	id, _ := ctx.Value(siteIDKey{}).(string)
	return id
}

type root struct {
	deps       *Deps
	app, sites http.Handler
}

func (h *root) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := Hostname(r.Host)
	if id, ok := h.siteHost(host); ok {
		// Site hosts serve their own content at every path, including
		// /healthz and /readyz.
		h.sites.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), siteIDKey{}, id)))
		return
	}
	// Probes use the pod IP as Host, so health answers on every other host.
	switch r.URL.Path {
	case "/healthz":
		h.healthz(w, r)
		return
	case "/readyz":
		h.readyz(w, r)
		return
	}
	if host == h.deps.Config.AppHost {
		h.app.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// siteHost returns the site id when host is exactly <id>.SITES_DOMAIN.
func (h *root) siteHost(host string) (string, bool) {
	id, ok := strings.CutSuffix(host, "."+h.deps.Config.SitesDomain)
	if !ok || !siteid.Valid(id) {
		return "", false
	}
	return id, true
}

// Hostname returns the lowercased host of a Host header without port or
// trailing dot.
func Hostname(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func (h *root) healthz(w http.ResponseWriter, r *http.Request) {
	if !probeMethod(w, r) {
		return
	}
	writeText(w, http.StatusOK, "ok\n")
}

// readyz reports ready when the database and storage backend answer.
func (h *root) readyz(w http.ResponseWriter, r *http.Request) {
	if !probeMethod(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var failed []string
	if h.deps.DB != nil {
		if err := h.deps.DB.Ping(ctx); err != nil {
			h.deps.Logger.Warn("readiness check failed", "check", "database", "err", err)
			failed = append(failed, "database")
		}
	}
	if h.deps.Storage != nil {
		if err := h.deps.Storage.Ping(ctx); err != nil {
			h.deps.Logger.Warn("readiness check failed", "check", "storage", "err", err)
			failed = append(failed, "storage")
		}
	}
	if len(failed) > 0 {
		writeText(w, http.StatusServiceUnavailable, "not ready: "+strings.Join(failed, ", ")+"\n")
		return
	}
	writeText(w, http.StatusOK, "ok\n")
}

func probeMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	writeText(w, http.StatusMethodNotAllowed, "method not allowed\n")
	return false
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
