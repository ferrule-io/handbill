// Package upload serves uploads into an open staged deployment with a
// short-lived signed URL from the get_upload_url MCP tool
// (docs/design.md#publishing):
//
//   - PUT /api/deployments/{id}/archive: a whole site as one .tar.gz or .zip;
//   - PUT /api/deployments/{id}/file: one raw file at the path the URL's
//     grant carries, its content type taken from the path's extension.
//
// The URL is the only credential: no session, CSRF, or CORS applies.
package upload

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/token"
)

// open checks the request's grant against its endpoint (file or archive)
// and the deployment's current state. It writes the error and returns false
// when the upload is refused.
func (h *handler) open(w http.ResponseWriter, r *http.Request, file bool) (token.UploadGrant, bool) {
	g, err := h.signer.ParseUploadGrant(r.URL.Query().Get("grant"), h.now())
	if errors.Is(err, token.ErrExpired) {
		writeErr(w, http.StatusForbidden, "expired_grant", "The upload URL expired: call get_upload_url again.")
		return g, false
	}
	if err != nil || (g.Path != "") != file {
		writeErr(w, http.StatusForbidden, "invalid_grant", "The upload URL is invalid.")
		return g, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id != g.DeploymentID {
		writeErr(w, http.StatusForbidden, "invalid_grant", "The upload URL is invalid.")
		return g, false
	}
	dep, err := h.sites.GetDeployment(r.Context(), id)
	if errors.Is(err, sites.ErrNotFound) || (err == nil && dep.SiteID != g.SiteID) {
		writeErr(w, http.StatusNotFound, "not_found", "The deployment was not found.")
		return g, false
	}
	if err != nil {
		h.fail(w, err)
		return g, false
	}
	// The grant is its requester's: it works only while they are the
	// deployment's creator and can still publish to the site, so a manager
	// removed or demoted within the grant's lifetime is refused.
	if !dep.IsCreator(g.UserID) {
		writeErr(w, http.StatusForbidden, "forbidden", "Only the deployment's creator can upload into it.")
		return g, false
	}
	role, err := h.sites.Role(r.Context(), dep.SiteID, g.UserID)
	if err != nil {
		h.fail(w, err)
		return g, false
	}
	if !rbac.Can(role, rbac.Publish) {
		writeErr(w, http.StatusForbidden, "forbidden", "You can no longer publish to this site.")
		return g, false
	}
	if dep.State != "open" {
		writeErr(w, http.StatusConflict, "deployment_closed", "The deployment is already finalized or abandoned: start a new one with create_deployment.")
		return g, false
	}
	return g, true
}

// archive is PUT /api/deployments/{id}/archive?grant=<signed>.
func (h *handler) archive(w http.ResponseWriter, r *http.Request) {
	g, ok := h.open(w, r, false)
	if !ok {
		return
	}
	id := g.DeploymentID

	// The format is sniffed: curl -T sends no Content-Type.
	limited := &limitReader{r: http.MaxBytesReader(w, r.Body, h.d.Config.Limits.MaxArchiveSize)}
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
	err := h.sites.AddArchive(r.Context(), id, g.UserID, body, format)
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
	default:
		h.closedOrFail(w, err)
	}
}

// file is PUT /api/deployments/{id}/file?grant=<signed>: the body is the
// file. Its content type comes from the grant's path, never from the
// request (curl -T sends none, --data-binary a form type).
func (h *handler) file(w http.ResponseWriter, r *http.Request) {
	g, ok := h.open(w, r, true)
	if !ok {
		return
	}
	limit := h.d.Config.Limits.MaxFileSize
	tooLarge := fmt.Sprintf("The file is larger than %d bytes.", limit)
	if r.ContentLength > limit {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", tooLarge)
		return
	}
	// One byte past the limit lets the size check see an oversized body
	// instead of a silent cut at the limit.
	body := http.MaxBytesReader(w, r.Body, limit+1)
	err := h.sites.AddFile(r.Context(), g.DeploymentID, g.UserID, g.Path, body)
	var ie *sites.InputError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{
			"deployment_id": g.DeploymentID.String(), "path": g.Path, "status": "uploaded",
			"next": "Call finalize_deployment to publish, or upload more files first.",
		})
	case errors.Is(err, sites.ErrFileTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", tooLarge)
	case errors.As(err, &ie):
		writeErr(w, http.StatusBadRequest, "invalid_file", ie.Msg)
	default:
		h.closedOrFail(w, err)
	}
}

// closedOrFail answers the deployment errors an upload can meet after its
// checks passed (a concurrent finalize, delete, or creator change), and
// anything else as a server failure.
func (h *handler) closedOrFail(w http.ResponseWriter, err error) {
	switch {
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
	h.d.Logger.Error("upload failed", "err", err)
	writeErr(w, http.StatusInternalServerError, "internal", "Something went wrong on our side. Try again.")
}

// limitReader records whether the body exceeded MAX_ARCHIVE_SIZE: archive
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
