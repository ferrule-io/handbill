package mcp

import (
	"net/http"

	"github.com/mjquinlan2000/agent-dash/internal/config"
)

// checkOrigin enforces the Streamable HTTP DNS-rebinding rule: requests
// without Origin (native and server-side clients) pass, Origin equal to
// APP_ORIGIN passes, and anything else (including "null", site origins,
// and http://localhost:*) gets 403. It runs before authentication. No CORS
// headers are ever sent, so browser preflights fail closed.
//
// The SDK's own localhost protection is disabled: it refuses loopback
// listeners whose Host is not literally a loopback name, which breaks
// dash.localhost, tunnels, and sidecars. internal/server already routes
// only APP_ORIGIN's host to the app mux.
func checkOrigin(cfg *config.Config, next http.Handler) http.Handler {
	allowed := cfg.AppOrigin.String()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o, ok := r.Header["Origin"]; ok && (len(o) != 1 || o[0] != allowed) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Forbidden: Origin not allowed"}}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
