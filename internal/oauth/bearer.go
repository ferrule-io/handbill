package oauth

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/token"
)

// Principal is who an MCP request acts as: the user who approved the
// grant, through the client it was granted to.
type Principal struct {
	UserID        uuid.UUID
	Email         string // primary email; "" if none
	EmailVerified bool
	ClientID      string // CIMD URL or DCR id
	ClientName    string
	GrantID       uuid.UUID
	Scopes        []string // at most offline_access; no effect
}

type principalKey struct{}

// WithPrincipal returns ctx carrying p (RequireBearer does this; tests
// inject principals directly).
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the request's principal, if RequireBearer admitted it.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// A token.New value: 32 bytes, base64url without padding.
var tokenRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// RequireBearer admits requests with a valid access token for
// Resource(cfg) and answers 401 with an RFC 9728 WWW-Authenticate challenge
// otherwise. It overwrites identity.User with the token's user, so a
// session cookie that came along never acts on its own. Tokens in the
// query string or form body are never read.
func RequireBearer(d *server.Deps, next http.Handler) http.Handler {
	metaURL := ResourceMetadataURL(d.Config)
	resource := Resource(d.Config)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, tok, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if !strings.EqualFold(scheme, "Bearer") {
			unauthorized(w, `Bearer resource_metadata="`+metaURL+`"`, "Authentication required.")
			return
		}
		invalid := func() {
			unauthorized(w, `Bearer error="invalid_token", error_description="The access token is invalid, expired, or revoked.", resource_metadata="`+metaURL+`"`,
				"The access token is invalid, expired, or revoked.")
		}
		tok = strings.TrimSpace(tok)
		if !tokenRE.MatchString(tok) {
			invalid()
			return
		}
		q := db.New(d.DB)
		row, err := q.GetAccessTokenPrincipal(r.Context(), db.GetAccessTokenPrincipalParams{TokenHash: token.Hash(tok), Resource: resource})
		if errors.Is(err, pgx.ErrNoRows) {
			invalid()
			return
		}
		if err != nil {
			d.Logger.Error("oauth: check access token", "err", err)
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := q.TouchGrant(r.Context(), row.GrantID); err != nil {
			d.Logger.Warn("oauth: touch grant", "err", err)
		}
		p := Principal{
			UserID:        row.UserID,
			EmailVerified: row.EmailVerified,
			ClientID:      row.ClientKey,
			ClientName:    row.ClientName,
			GrantID:       row.GrantID,
			Scopes:        row.Scopes,
		}
		if row.Email != nil {
			p.Email = *row.Email
		}
		ctx := WithPrincipal(r.Context(), p)
		ctx = identity.WithUser(ctx, identity.User{ID: p.UserID, Email: p.Email, EmailVerified: p.EmailVerified})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func unauthorized(w http.ResponseWriter, challenge, description string) {
	w.Header().Set("WWW-Authenticate", challenge)
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token", "error_description": description})
}
