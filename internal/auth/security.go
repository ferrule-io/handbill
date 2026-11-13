package auth

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mjquinlan2000/agent-dash/internal/appapi"
)

type emailView struct {
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
	Primary  bool   `json:"primary"`
}

type googleView struct {
	Email      string     `json:"email"`
	LinkedAt   time.Time  `json:"linkedAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

type sessionView struct {
	ID         uuid.UUID `json:"id"`
	Device     string    `json:"device"`
	Current    bool      `json:"current"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type recoveryView struct {
	Remaining int64      `json:"remaining"`
	Total     int64      `json:"total"`
	IssuedAt  *time.Time `json:"issuedAt"`
}

// security answers GET /app-api/auth/security: everything the security
// settings page shows.
func (m *module) security(w http.ResponseWriter, r *http.Request) error {
	s, err := fullSession(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := struct {
		Emails        []emailView   `json:"emails"`
		Passkeys      []passkeyView `json:"passkeys"`
		Google        *googleView   `json:"google"`
		RecoveryCodes recoveryView  `json:"recoveryCodes"`
		Sessions      []sessionView `json:"sessions"`
	}{Emails: []emailView{}, Passkeys: []passkeyView{}, Sessions: []sessionView{}}

	emails, err := m.q.ListUserEmails(ctx, s.UserID)
	if err != nil {
		return err
	}
	for _, e := range emails {
		out.Emails = append(out.Emails, emailView{Email: e.Email, Verified: e.Verified, Primary: e.IsPrimary})
	}
	passkeys, err := m.q.ListPasskeys(ctx, s.UserID)
	if err != nil {
		return err
	}
	for _, p := range passkeys {
		out.Passkeys = append(out.Passkeys, toPasskeyView(p))
	}
	if gi, err := m.q.GetGoogleIdentityForUser(ctx, s.UserID); err == nil {
		out.Google = &googleView{Email: gi.Email, LinkedAt: gi.CreatedAt, LastUsedAt: gi.LastUsedAt}
	} else if !isNoRows(err) {
		return err
	}
	rc, err := m.q.RecoveryCodeStatus(ctx, s.UserID)
	if err != nil {
		return err
	}
	out.RecoveryCodes = recoveryView{Remaining: rc.Remaining, Total: rc.Total}
	if t, ok := rc.IssuedAt.(time.Time); ok {
		out.RecoveryCodes.IssuedAt = &t
	}
	sessions, err := m.q.ListSessions(ctx, s.UserID)
	if err != nil {
		return err
	}
	for _, x := range sessions {
		out.Sessions = append(out.Sessions, sessionView{
			ID: x.ID, Device: describeUserAgent(x.UserAgent), Current: x.ID == s.ID,
			CreatedAt: x.CreatedAt, LastSeenAt: x.LastSeenAt,
		})
	}
	appapi.WriteJSON(w, http.StatusOK, out)
	return nil
}
