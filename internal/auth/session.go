package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mjquinlan2000/agent-dash/internal/appapi"
	"github.com/mjquinlan2000/agent-dash/internal/db"
	"github.com/mjquinlan2000/agent-dash/internal/identity"
	"github.com/mjquinlan2000/agent-dash/internal/token"
)

// SessionCookie holds the plaintext session token; the database stores its
// SHA-256. The __Host- prefix pins it to the app host (Secure, Path=/, no
// Domain), so site hosts can neither read nor overwrite it.
const SessionCookie = "__Host-session"

// Session lifetimes (docs/design.md#accounts). A session expires after
// sessionIdle without use and never lives past sessionAbsolute; use renews
// the idle deadline at most once per renewEvery so ordinary browsing does
// not write on every request. A recovery session is restricted to
// registering a passkey and expires after recoveryTTL without renewal.
const (
	sessionIdle     = 30 * 24 * time.Hour
	sessionAbsolute = 90 * 24 * time.Hour
	renewEvery      = time.Hour
	recoveryTTL     = 15 * time.Minute
)

// session is the request's session, full or restricted.
type session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Restricted bool
	User       identity.User
}

type sessionKey struct{}

func sessionFrom(ctx context.Context) (*session, bool) {
	s, ok := ctx.Value(sessionKey{}).(*session)
	return s, ok
}

// fullSession returns the signed-in user's unrestricted session.
func fullSession(r *http.Request) (*session, error) {
	s, ok := sessionFrom(r.Context())
	if !ok || s.Restricted {
		return nil, errUnauth
	}
	return s, nil
}

// sessionMiddleware loads the session cookie. Only unrestricted sessions
// become identity.User; a restricted one is visible to this package alone.
func (m *module) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		// Static assets never need the user.
		if err != nil || c.Value == "" || strings.HasPrefix(r.URL.Path, "/_next/") {
			next.ServeHTTP(w, r)
			return
		}
		row, err := m.q.GetSession(r.Context(), token.Hash(c.Value))
		if err != nil {
			if isNoRows(err) {
				clearCookie(w, SessionCookie)
			} else {
				m.d.Logger.Error("session lookup failed", "err", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		s := &session{ID: row.ID, UserID: row.UserID, Restricted: row.Restricted, User: identity.User{ID: row.UserID}}
		if row.Email != nil {
			s.User.Email = *row.Email
			s.User.EmailVerified = row.EmailVerified != nil && *row.EmailVerified
		}
		if !row.Restricted && time.Since(row.LastSeenAt) >= renewEvery {
			exp := renewedExpiry(row.CreatedAt, time.Now())
			if err := m.q.RenewSession(r.Context(), db.RenewSessionParams{ID: row.ID, ExpiresAt: exp}); err != nil {
				m.d.Logger.Error("session renewal failed", "err", err)
			} else {
				setCookie(w, SessionCookie, c.Value, exp)
			}
		}
		ctx := context.WithValue(r.Context(), sessionKey{}, s)
		if !s.Restricted {
			ctx = identity.WithUser(ctx, s.User)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// renewedExpiry is the idle deadline from now, capped at the absolute one.
func renewedExpiry(created, now time.Time) time.Time {
	exp := now.Add(sessionIdle)
	if limit := created.Add(sessionAbsolute); exp.After(limit) {
		exp = limit
	}
	return exp
}

// startSession creates a session for userID and sets its cookie.
func (m *module) startSession(ctx context.Context, q *db.Queries, w http.ResponseWriter, r *http.Request, userID uuid.UUID, restricted bool) error {
	plain, hash := token.New()
	ttl := sessionIdle
	if restricted {
		ttl = recoveryTTL
	}
	exp := time.Now().Add(ttl)
	if _, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:         newID(),
		TokenHash:  hash,
		UserID:     userID,
		UserAgent:  truncate(r.UserAgent(), 512),
		ExpiresAt:  exp,
		Restricted: restricted,
	}); err != nil {
		return err
	}
	setCookie(w, SessionCookie, plain, exp)
	return nil
}

// replaceSession ends the request's current session (if any) and starts a
// new one, so a session token is never reused across a privilege change.
func (m *module) replaceSession(ctx context.Context, q *db.Queries, w http.ResponseWriter, r *http.Request, userID uuid.UUID, restricted bool) error {
	if s, ok := sessionFrom(r.Context()); ok {
		if _, err := q.DeleteSession(ctx, s.ID); err != nil {
			return err
		}
	}
	return m.startSession(ctx, q, w, r, userID, restricted)
}

func setCookie(w http.ResponseWriter, name, value string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  exp,
		MaxAge:   max(1, int(time.Until(exp).Seconds())),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

type meUser struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"emailVerified"`
}

func toMeUser(u identity.User) *meUser {
	return &meUser{ID: u.ID, Email: u.Email, EmailVerified: u.EmailVerified}
}

// me answers GET /app-api/me: the signed-in user (null when signed out or
// in a recovery session), the CSRF token, and which sign-in features exist.
func (m *module) me(w http.ResponseWriter, r *http.Request) error {
	out := struct {
		User          *meUser `json:"user"`
		Recovery      bool    `json:"recovery"`
		CSRFToken     string  `json:"csrfToken"`
		GoogleEnabled bool    `json:"googleEnabled"`
		MailEnabled   bool    `json:"mailEnabled"`
	}{
		CSRFToken:     appapi.CSRFToken(w, r, m.d),
		GoogleEnabled: m.google != nil,
		MailEnabled:   m.mail.Enabled(),
	}
	if s, ok := sessionFrom(r.Context()); ok {
		if s.Restricted {
			out.Recovery = true
		} else {
			out.User = toMeUser(s.User)
		}
	}
	appapi.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (m *module) signOut(w http.ResponseWriter, r *http.Request) error {
	if s, ok := sessionFrom(r.Context()); ok {
		if _, err := m.q.DeleteSession(r.Context(), s.ID); err != nil {
			return err
		}
	}
	clearCookie(w, SessionCookie)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (m *module) revokeOtherSessions(w http.ResponseWriter, r *http.Request) error {
	s, err := fullSession(r)
	if err != nil {
		return err
	}
	n, err := m.q.DeleteOtherSessions(r.Context(), db.DeleteOtherSessionsParams{UserID: s.UserID, Keep: s.ID})
	if err != nil {
		return err
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]int64{"revoked": n})
	return nil
}
