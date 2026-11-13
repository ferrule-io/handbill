package upload

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/mjquinlan2000/agent-dash/internal/config"
	"github.com/mjquinlan2000/agent-dash/internal/server"
	"github.com/mjquinlan2000/agent-dash/internal/sites"
	"github.com/mjquinlan2000/agent-dash/internal/token"
)

// GrantTTL is how long an upload URL works.
const GrantTTL = 15 * time.Minute

// maxArchiveBytes caps the request body; a variable so tests can lower it.
var maxArchiveBytes = sites.MaxArchiveSize

// URL returns the upload URL for a deployment.
func URL(c *config.Config, s *token.Signer, g token.UploadGrant) string {
	return c.AppOrigin.String() + "/api/deployments/" + g.DeploymentID.String() + "/archive?grant=" + url.QueryEscape(s.UploadGrant(g))
}

type handler struct {
	d      *server.Deps
	sites  *sites.Service
	signer *token.Signer
	now    func() time.Time
}

// Register installs the upload endpoint on the app mux. Other methods get
// the mux's 405.
func Register(r *server.Routes, d *server.Deps) error {
	h := &handler{d: d, sites: sites.NewService(d), signer: token.NewSigner(d.Config.SecretKey), now: time.Now}
	r.App.Handle("PUT /api/deployments/{id}/archive", h)
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
