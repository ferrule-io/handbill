// Package httpserver runs handbill's HTTP listeners: the timeouts every
// connection gets, the longer per-request deadlines of uploads, MCP calls,
// and site file streaming, and graceful shutdown
// (docs/design.md#operations).
package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ferrule-io/handbill/internal/server"
)

// Connection-wide limits. Handlers that legitimately need longer extend
// their own deadlines (Register).
const (
	ReadHeaderTimeout = 10 * time.Second
	// ReadTimeout bounds reading a request body: JSON and form bodies are
	// small (MAX_JSON_BODY).
	ReadTimeout = 60 * time.Second
	// WriteTimeout bounds a handler's whole run and response.
	WriteTimeout = 2 * time.Minute
	IdleTimeout  = 2 * time.Minute
	// MaxHeaderBytes stays at net/http's 1 MiB default on purpose: site
	// hosts share a registrable domain, so one site can set cookies on the
	// parent domain; a lower cap would let it lock its viewers out of other
	// sites and the app ("cookie bombing"). Browsers cap a domain's cookies
	// well below 1 MiB.
	MaxHeaderBytes = 1 << 20
)

// Per-route deadlines, set from the start of the request.
const (
	// UploadTimeout covers a MAX_ARCHIVE_SIZE archive or MAX_FILE_SIZE file
	// on a slow uplink.
	UploadTimeout = 30 * time.Minute
	// MCPTimeout covers a MAX_MCP_BODY publish_site call.
	MCPTimeout = 5 * time.Minute
	// SiteWriteTimeout covers streaming a MAX_FILE_SIZE file to a slow
	// viewer.
	SiteWriteTimeout = 30 * time.Minute
)

// New returns a server for handler with the connection-wide limits.
func New(addr string, handler http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// Serve serves srv on ln until ctx is done, then shuts down gracefully:
// the listener closes at once and in-flight requests get up to drain to
// finish before their connections are closed.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, drain time.Duration, log *slog.Logger) error {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("shutdown: requests still running after the drain timeout were cut off", "addr", ln.Addr().String(), "drain", drain.String())
		_ = srv.Close()
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Register extends the read and write deadlines of the routes that need
// longer than the connection-wide limits.
func Register(r *server.Routes, d *server.Deps) error {
	r.UseApp(extend(d.Logger, func(req *http.Request) (read, write time.Duration) {
		switch _, pattern := r.App.Handler(req); pattern {
		case "PUT /api/deployments/{id}/archive", "PUT /api/deployments/{id}/file":
			return UploadTimeout, UploadTimeout
		case "POST /mcp":
			return MCPTimeout, MCPTimeout
		}
		return 0, 0
	}))
	r.UseSites(extend(d.Logger, func(req *http.Request) (read, write time.Duration) {
		return 0, SiteWriteTimeout
	}))
	return nil
}

// extend sets the deadlines chosen for a request; zero keeps the default.
func extend(log *slog.Logger, choose func(*http.Request) (read, write time.Duration)) server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			read, write := choose(r)
			rc := http.NewResponseController(w)
			now := time.Now()
			if read > 0 {
				if err := rc.SetReadDeadline(now.Add(read)); err != nil {
					log.Warn("cannot extend the read deadline", "err", err)
				}
			}
			if write > 0 {
				if err := rc.SetWriteDeadline(now.Add(write)); err != nil {
					log.Warn("cannot extend the write deadline", "err", err)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
