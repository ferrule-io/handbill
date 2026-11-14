package upload

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/token"
)

// GrantTTL is how long an upload URL works.
const GrantTTL = 15 * time.Minute

// URL returns the upload URL for a deployment: the raw-file endpoint when
// the grant carries a path, the archive endpoint otherwise.
func URL(c *config.Config, s *token.Signer, g token.UploadGrant) string {
	endpoint := "/archive"
	if g.Path != "" {
		endpoint = "/file"
	}
	return c.AppOrigin.String() + "/api/deployments/" + g.DeploymentID.String() + endpoint + "?grant=" + url.QueryEscape(s.UploadGrant(g))
}

type handler struct {
	d      *server.Deps
	sites  *sites.Service
	signer *token.Signer
	now    func() time.Time
}

// Register installs the upload endpoints on the app mux. Other methods get
// the mux's 405.
func Register(r *server.Routes, d *server.Deps) error {
	h := &handler{d: d, sites: sites.NewService(d), signer: token.NewSigner(d.Config.SecretKey), now: time.Now}
	r.App.HandleFunc("PUT /api/deployments/{id}/archive", h.archive)
	r.App.HandleFunc("PUT /api/deployments/{id}/file", h.file)
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
