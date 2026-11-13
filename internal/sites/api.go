package sites

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/server"
)

// Register installs the UI's site endpoints under /app-api/sites. Callers
// without any role on a site get the same 404 whether or not it exists.
func Register(r *server.Routes, d *server.Deps) error {
	a := &api{svc: NewService(d), deps: d}
	appapi.Handle(r, d, "GET /app-api/sites", a.list)
	appapi.Handle(r, d, "GET /app-api/sites/{id}", a.get)
	appapi.Handle(r, d, "PATCH /app-api/sites/{id}", a.update)
	appapi.Handle(r, d, "DELETE /app-api/sites/{id}", a.delete)
	appapi.Handle(r, d, "GET /app-api/sites/{id}/versions", a.versions)
	appapi.Handle(r, d, "POST /app-api/sites/{id}/rollback", a.rollback)
	return nil
}

type api struct {
	svc  *Service
	deps *server.Deps
}

type siteJSON struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	URL              string     `json:"url"`
	Role             rbac.Role  `json:"role"`
	CurrentVersionID *uuid.UUID `json:"currentVersionId"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type versionJSON struct {
	ID          uuid.UUID `json:"id"`
	PublishedBy *string   `json:"publishedBy"` // email; null when unknown
	CreatedAt   time.Time `json:"createdAt"`
	FileCount   int       `json:"fileCount"`
	Size        int64     `json:"size"`
	Current     bool      `json:"current"`
}

func (a *api) siteJSON(s Site, role rbac.Role) siteJSON {
	out := siteJSON{
		ID: s.ID, Title: s.Title, Description: s.Description,
		URL:  a.deps.Config.SiteOrigin(s.ID) + "/",
		Role: role, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
	if s.CurrentVersionID.Valid {
		out.CurrentVersionID = &s.CurrentVersionID.UUID
	}
	return out
}

func versionToJSON(v Version, current uuid.NullUUID) versionJSON {
	out := versionJSON{
		ID: v.ID, CreatedAt: v.CreatedAt, FileCount: v.FileCount, Size: v.Size,
		Current: current.Valid && current.UUID == v.ID,
	}
	if v.PublisherEmail != "" {
		out.PublishedBy = &v.PublisherEmail
	}
	return out
}

var errSiteNotFound = appapi.NewError(http.StatusNotFound, "not_found", "Site not found.")

// member authorizes the signed-in user for action on the {id} site.
func (a *api) member(r *http.Request, action rbac.Action) (Site, rbac.Role, error) {
	u, _ := identity.FromContext(r.Context())
	id := r.PathValue("id")
	role, err := a.svc.Role(r.Context(), id, u.ID)
	if err != nil {
		return Site{}, "", err
	}
	if role == "" {
		return Site{}, "", errSiteNotFound
	}
	if !rbac.Can(role, action) {
		return Site{}, "", appapi.NewError(http.StatusForbidden, "forbidden", "Your role on this site does not allow that.")
	}
	site, err := a.svc.Get(r.Context(), id)
	if err != nil {
		return Site{}, "", apiError(err)
	}
	return site, role, nil
}

// apiError maps service errors to API errors.
func apiError(err error) error {
	var ie *InputError
	switch {
	case errors.As(err, &ie):
		return appapi.NewError(http.StatusBadRequest, "invalid_request", ie.Msg)
	case errors.Is(err, ErrNotFound):
		return errSiteNotFound
	}
	return err
}

func (a *api) list(w http.ResponseWriter, r *http.Request) error {
	u, _ := identity.FromContext(r.Context())
	ms, err := a.svc.ListForUser(r.Context(), u.ID)
	if err != nil {
		return err
	}
	out := make([]siteJSON, len(ms))
	for i, m := range ms {
		out[i] = a.siteJSON(m.Site, m.Role)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"sites": out})
	return nil
}

func (a *api) get(w http.ResponseWriter, r *http.Request) error {
	site, role, err := a.member(r, rbac.View)
	if err != nil {
		return err
	}
	var current *versionJSON
	if site.CurrentVersionID.Valid {
		v, err := a.svc.GetVersion(r.Context(), site.ID, site.CurrentVersionID.UUID)
		if err != nil {
			return err
		}
		j := versionToJSON(v, site.CurrentVersionID)
		current = &j
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"site": a.siteJSON(site, role), "currentVersion": current})
	return nil
}

func (a *api) update(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	site, role, err := a.member(r, rbac.EditMeta)
	if err != nil {
		return err
	}
	site, err = a.svc.Update(r.Context(), site.ID, in.Title, in.Description)
	if err != nil {
		return apiError(err)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"site": a.siteJSON(site, role)})
	return nil
}

func (a *api) delete(w http.ResponseWriter, r *http.Request) error {
	site, _, err := a.member(r, rbac.Delete)
	if err != nil {
		return err
	}
	if err := a.svc.Delete(r.Context(), site.ID); err != nil {
		return apiError(err)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{})
	return nil
}

func (a *api) versions(w http.ResponseWriter, r *http.Request) error {
	site, _, err := a.member(r, rbac.Publish)
	if err != nil {
		return err
	}
	vs, err := a.svc.ListVersions(r.Context(), site.ID)
	if err != nil {
		return err
	}
	out := make([]versionJSON, len(vs))
	for i, v := range vs {
		out[i] = versionToJSON(v, site.CurrentVersionID)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"versions": out})
	return nil
}

func (a *api) rollback(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		VersionID uuid.UUID `json:"versionId"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	site, _, err := a.member(r, rbac.Publish)
	if err != nil {
		return err
	}
	if err := a.svc.Rollback(r.Context(), site.ID, in.VersionID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return appapi.NewError(http.StatusNotFound, "version_not_found", "That version does not belong to this site.")
		}
		return err
	}
	v, err := a.svc.GetVersion(r.Context(), site.ID, in.VersionID)
	if err != nil {
		return err
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"currentVersion": versionToJSON(v, uuid.NullUUID{UUID: v.ID, Valid: true})})
	return nil
}
