package oauth

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/identity"
)

type grantJSON struct {
	ID         uuid.UUID     `json:"id"`
	Client     consentClient `json:"client"`
	CreatedAt  time.Time     `json:"createdAt"`
	LastUsedAt *time.Time    `json:"lastUsedAt"`
}

// listGrants is GET /app-api/oauth/grants: the apps connected to the
// signed-in account, most recently used first.
func (m *module) listGrants(w http.ResponseWriter, r *http.Request) error {
	user, _ := identity.FromContext(r.Context())
	rows, err := db.New(m.d.DB).ListGrantsForUser(r.Context(), user.ID)
	if err != nil {
		return err
	}
	out := make([]grantJSON, len(rows))
	for i, g := range rows {
		out[i] = grantJSON{
			ID:         g.ID,
			Client:     describeClient(g.ClientKey, g.ClientName, g.Registration, g.RedirectUris),
			CreatedAt:  g.CreatedAt.UTC(),
			LastUsedAt: g.LastUsedAt,
		}
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"grants": out})
	return nil
}

// deleteGrant is DELETE /app-api/oauth/grants/{id}. Codes and tokens
// cascade, so the app's next /mcp call and refresh fail at once.
func (m *module) deleteGrant(w http.ResponseWriter, r *http.Request) error {
	notFound := appapi.NewError(http.StatusNotFound, "not_found", "That connection no longer exists.")
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return notFound
	}
	user, _ := identity.FromContext(r.Context())
	n, err := db.New(m.d.DB).DeleteGrant(r.Context(), db.DeleteGrantParams{ID: id, UserID: user.ID})
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
	return nil
}
