// Package upload serves PUT /api/deployments/{id}/archive: a whole site
// as one .tar.gz or .zip, uploaded into an open staged deployment with a
// short-lived signed URL from the get_upload_url MCP tool
// (docs/design.md#publishing). The URL is the only credential: no session,
// CSRF, or CORS applies.
package upload

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/token"
)

// ServeHTTP is PUT /api/deployments/{id}/archive?grant=<signed>.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g, err := h.signer.ParseUploadGrant(r.URL.Query().Get("grant"), h.now())
	if errors.Is(err, token.ErrExpired) {
		writeErr(w, http.StatusForbidden, "expired_grant", "The upload URL expired: call get_upload_url again.")
		return
	}
	if err != nil {
		writeErr(w, http.StatusForbidden, "invalid_grant", "The upload URL is invalid.")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id != g.DeploymentID {
		writeErr(w, http.StatusForbidden, "invalid_grant", "The upload URL is invalid.")
		return
	}
	dep, err := h.sites.GetDeployment(r.Context(), id)
	if errors.Is(err, sites.ErrNotFound) || (err == nil && dep.SiteID != g.SiteID) {
		writeErr(w, http.StatusNotFound, "not_found", "The deployment was not found.")
		return
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	// The grant is its requester's: it works only while they are the
	// deployment's creator and can still publish to the site, so a manager
	// removed or demoted within the grant's lifetime is refused.
	if !dep.IsCreator(g.UserID) {
		writeErr(w, http.StatusForbidden, "forbidden", "Only the deployment's creator can upload into it.")
		return
	}
	role, err := h.sites.Role(r.Context(), dep.SiteID, g.UserID)
	if err != nil {
		h.fail(w, err)
		return
	}
	if !rbac.Can(role, rbac.Publish) {
		writeErr(w, http.StatusForbidden, "forbidden", "You can no longer publish to this site.")
		return
	}
	if dep.State != "open" {
		writeErr(w, http.StatusConflict, "deployment_closed", "The deployment is already finalized or abandoned: start a new one with create_deployment.")
		return
	}

	// The format is sniffed: curl -T sends no Content-Type.
	limited := &limitReader{r: http.MaxBytesReader(w, r.Body, maxArchiveBytes)}
	body := bufio.NewReader(limited)
	magic, _ := body.Peek(4)
	var format string
	switch {
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		format = "tar.gz"
	case bytes.HasPrefix(magic, []byte{'P', 'K', 0x03, 0x04}):
		format = "zip"
	default:
		writeErr(w, http.StatusUnsupportedMediaType, "unsupported_format", "Upload a .tar.gz or .zip archive.")
		return
	}
	err = h.sites.AddArchive(r.Context(), id, g.UserID, body, format)
	var ie *sites.InputError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{
			"deployment_id": id.String(), "status": "uploaded", "next": "Call finalize_deployment to publish.",
		})
	case limited.tooLarge:
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "The archive is larger than the upload limit.")
	case errors.As(err, &ie):
		writeErr(w, http.StatusBadRequest, "invalid_archive", ie.Msg)
	case errors.Is(err, sites.ErrDeploymentClosed):
		writeErr(w, http.StatusConflict, "deployment_closed", "The deployment is already finalized or abandoned: start a new one with create_deployment.")
	case errors.Is(err, sites.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "The deployment was not found.")
	case errors.Is(err, sites.ErrNotCreator):
		writeErr(w, http.StatusForbidden, "forbidden", "Only the deployment's creator can upload into it.")
	default:
		h.fail(w, err)
	}
}

func (h *handler) fail(w http.ResponseWriter, err error) {
	h.d.Logger.Error("upload: archive failed", "err", err)
	writeErr(w, http.StatusInternalServerError, "internal", "Something went wrong on our side. Try again.")
}

// limitReader records whether the body exceeded maxArchiveBytes: archive
// readers report the failure as an invalid archive.
type limitReader struct {
	r        io.Reader
	tooLarge bool
}

func (l *limitReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		l.tooLarge = true
	}
	return n, err
}
