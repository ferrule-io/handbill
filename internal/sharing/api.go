package sharing

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites"
)

// Register installs the UI's sharing endpoints. Every call re-checks the
// caller's current role, so role changes and removals apply to the next
// request.
//
//	GET    /app-api/sites/{id}/members          members and pending invites (manager)
//	POST   /app-api/sites/{id}/members          invite {email, role, notify}
//	PATCH  /app-api/sites/{id}/members/{member} {role} (owner)
//	DELETE /app-api/sites/{id}/members/{member} remove a member or revoke an invite
//	GET    /app-api/sites/{id}/public-link      {enabled, updatedAt} (manager)
//	POST   /app-api/sites/{id}/public-link      {op: enable|disable|rotate} (owner)
//	GET    /app-api/sites/{id}/transfer         pending transfer (owner)
//	POST   /app-api/sites/{id}/transfer         start {email, notify} (owner)
//	DELETE /app-api/sites/{id}/transfer         cancel (owner)
//	POST   /app-api/invites/lookup              {token}: what the link offers (public)
//	POST   /app-api/invites/accept              {token}: accept it as the signed-in user
//
// Tokens travel in JSON bodies, never in URLs.
func Register(r *server.Routes, d *server.Deps) error {
	a := &api{svc: NewService(d)}
	appapi.Handle(r, d, "GET /app-api/sites/{id}/members", a.members)
	appapi.Handle(r, d, "POST /app-api/sites/{id}/members", a.invite)
	appapi.Handle(r, d, "PATCH /app-api/sites/{id}/members/{member}", a.setRole)
	appapi.Handle(r, d, "DELETE /app-api/sites/{id}/members/{member}", a.revoke)
	appapi.Handle(r, d, "GET /app-api/sites/{id}/public-link", a.publicLink)
	appapi.Handle(r, d, "POST /app-api/sites/{id}/public-link", a.setPublicLink)
	appapi.Handle(r, d, "GET /app-api/sites/{id}/transfer", a.transfer)
	appapi.Handle(r, d, "POST /app-api/sites/{id}/transfer", a.startTransfer)
	appapi.Handle(r, d, "DELETE /app-api/sites/{id}/transfer", a.cancelTransfer)
	appapi.Handle(r, d, "POST /app-api/invites/lookup", a.lookup, appapi.Public())
	appapi.Handle(r, d, "POST /app-api/invites/accept", a.accept)
	return nil
}

type api struct{ svc *Service }

func user(r *http.Request) uuid.UUID {
	u, _ := identity.FromContext(r.Context())
	return u.ID
}

// apiError maps service errors to API errors.
func apiError(err error) error {
	var ie *sites.InputError
	if errors.As(err, &ie) {
		return appapi.NewError(http.StatusBadRequest, "invalid_request", upperFirst(ie.Msg)+".")
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return appapi.NewError(http.StatusNotFound, "not_found", "Site not found.")
	case errors.Is(err, ErrForbidden):
		return appapi.NewError(http.StatusForbidden, "forbidden", "Your role on this site does not allow that.")
	case errors.Is(err, ErrNoSuchMember):
		return appapi.NewError(http.StatusNotFound, "member_not_found", "That member or invite is no longer here.")
	case errors.Is(err, ErrAlreadyMember):
		return appapi.NewError(http.StatusConflict, "already_member", "That address already has access. Change its role in the member list instead.")
	case errors.Is(err, ErrOwner):
		return appapi.NewError(http.StatusConflict, "owner", "The owner’s access only changes by transferring ownership.")
	case errors.Is(err, ErrLinkUnusable):
		return appapi.NewError(http.StatusGone, "link_unusable", "This link was already used, has expired, or was revoked.")
	case errors.Is(err, ErrAlreadyOwner):
		return appapi.NewError(http.StatusConflict, "already_owner", "You already own this site. Sign in with the account that should receive it.")
	case errors.Is(err, ErrPublicLinkEnabled):
		return appapi.NewError(http.StatusConflict, "public_link_enabled", "The public link is already on. Rotate it to get a new link.")
	case errors.Is(err, ErrPublicLinkDisabled):
		return appapi.NewError(http.StatusConflict, "public_link_disabled", "The public link is off. Turn it on first.")
	}
	return err
}

func upperFirst(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}

// --- Members ------------------------------------------------------------------

type memberJSON struct {
	UserID        uuid.UUID `json:"userId"`
	Email         *string   `json:"email"`
	EmailVerified bool      `json:"emailVerified"`
	Role          rbac.Role `json:"role"`
	You           bool      `json:"you"`
	Since         time.Time `json:"since"`
}

type inviteJSON struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Role      rbac.Role `json:"role"`
	InvitedBy *string   `json:"invitedBy"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (a *api) members(w http.ResponseWriter, r *http.Request) error {
	me := user(r)
	ms, is, err := a.svc.Members(r.Context(), r.PathValue("id"), me)
	if err != nil {
		return apiError(err)
	}
	members := make([]memberJSON, len(ms))
	for i, m := range ms {
		members[i] = memberJSON{
			UserID: m.UserID, Email: optional(m.Email), EmailVerified: m.EmailVerified,
			Role: m.Role, You: m.UserID == me, Since: m.Since,
		}
	}
	invites := make([]inviteJSON, len(is))
	for i, v := range is {
		invites[i] = inviteJSON{ID: v.ID, Email: v.Email, Role: v.Role, InvitedBy: optional(v.InvitedBy), CreatedAt: v.CreatedAt, ExpiresAt: v.ExpiresAt}
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"members": members, "invites": invites, "mailEnabled": a.svc.MailEnabled()})
	return nil
}

func (a *api) invite(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email  string    `json:"email"`
		Role   rbac.Role `json:"role"`
		Notify bool      `json:"notify"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	inv, err := a.svc.Invite(r.Context(), r.PathValue("id"), user(r), in.Email, in.Role, in.Notify)
	if err != nil {
		return apiError(err)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{
		"invite":             map[string]any{"id": inv.ID, "email": inv.Email, "role": inv.Role, "expiresAt": inv.ExpiresAt},
		"link":               inv.Link,
		"granted":            inv.Granted,
		"emailed":            inv.Emailed,
		"emailNotSentReason": optional(string(inv.NotEmailedReason)),
	})
	return nil
}

func memberID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("member"))
	if err != nil {
		return uuid.Nil, apiError(ErrNoSuchMember)
	}
	return id, nil
}

func (a *api) setRole(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Role rbac.Role `json:"role"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	member, err := memberID(r)
	if err != nil {
		return err
	}
	if err := a.svc.SetRole(r.Context(), r.PathValue("id"), user(r), member, in.Role); err != nil {
		return apiError(err)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"role": in.Role})
	return nil
}

func (a *api) revoke(w http.ResponseWriter, r *http.Request) error {
	member, err := memberID(r)
	if err != nil {
		return err
	}
	if err := a.svc.Revoke(r.Context(), r.PathValue("id"), user(r), member); err != nil {
		return apiError(err)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{})
	return nil
}

// --- Public link ----------------------------------------------------------------

func (a *api) publicLink(w http.ResponseWriter, r *http.Request) error {
	pl, err := a.svc.PublicLinkState(r.Context(), r.PathValue("id"), user(r))
	if err != nil {
		return apiError(err)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"enabled": pl.Enabled, "updatedAt": pl.UpdatedAt})
	return nil
}

func (a *api) setPublicLink(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Op string `json:"op"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	link, err := a.svc.SetPublicLink(r.Context(), r.PathValue("id"), user(r), in.Op)
	if err != nil {
		return apiError(err)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"enabled": in.Op != LinkDisable, "link": optional(link)})
	return nil
}

// --- Ownership transfer -----------------------------------------------------------

type transferJSON struct {
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (a *api) transfer(w http.ResponseWriter, r *http.Request) error {
	t, err := a.svc.PendingTransfer(r.Context(), r.PathValue("id"), user(r))
	if err != nil {
		return apiError(err)
	}
	var out *transferJSON
	if t != nil {
		out = &transferJSON{Email: t.Email, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt}
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"transfer": out, "mailEnabled": a.svc.MailEnabled()})
	return nil
}

func (a *api) startTransfer(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email  string `json:"email"`
		Notify bool   `json:"notify"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	t, err := a.svc.StartTransfer(r.Context(), r.PathValue("id"), user(r), in.Email, in.Notify)
	if err != nil {
		return apiError(err)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{
		"transfer":           transferJSON{Email: t.Email, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt},
		"link":               t.Link,
		"emailed":            t.Emailed,
		"emailNotSentReason": optional(string(t.NotEmailedReason)),
	})
	return nil
}

func (a *api) cancelTransfer(w http.ResponseWriter, r *http.Request) error {
	if err := a.svc.CancelTransfer(r.Context(), r.PathValue("id"), user(r)); err != nil {
		return apiError(err)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{})
	return nil
}

// --- Invite page ------------------------------------------------------------------

type tokenIn struct {
	Token string `json:"token"`
}

func (a *api) lookup(w http.ResponseWriter, r *http.Request) error {
	var in tokenIn
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	p, err := a.svc.Lookup(r.Context(), in.Token, user(r))
	if errors.Is(err, ErrLinkUnusable) {
		return appapi.NewError(http.StatusNotFound, "link_unknown", "This link isn’t valid. Check that you copied all of it.")
	}
	if err != nil {
		return err
	}
	out := map[string]any{"kind": p.Kind, "status": p.Status, "acceptedByYou": p.AcceptedByYou, "site": nil}
	if p.SiteID != "" {
		out["site"] = map[string]string{"id": p.SiteID, "title": p.SiteTitle}
		out["from"] = optional(p.From)
		out["email"] = p.Email
		out["role"] = p.Role
		out["expiresAt"] = p.ExpiresAt
	}
	appapi.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (a *api) accept(w http.ResponseWriter, r *http.Request) error {
	var in tokenIn
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	u, _ := identity.FromContext(r.Context())
	siteID, err := a.svc.Accept(r.Context(), in.Token, u)
	if err != nil {
		return apiError(err)
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"siteId": siteID})
	return nil
}
