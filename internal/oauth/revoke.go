package oauth

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/token"
)

// revoke is POST /oauth/revoke (RFC 7009). Revoking a refresh token
// revokes its family; an access token is revoked alone. Unknown tokens,
// other clients' tokens, and already revoked ones all answer 200. The
// grant is kept: the settings page removes grants.
func (m *module) revoke(w http.ResponseWriter, r *http.Request) {
	form, e := readForm(w, r)
	if e == nil {
		var client db.OauthClient
		client, e = m.authenticateClient(r.Context(), r, form)
		if e == nil {
			if !form.Has("token") {
				e = badRequest("invalid_request", "token is required.")
			} else if err := m.revokeToken(r.Context(), client, form.Get("token"), form.Get("token_type_hint")); err != nil {
				m.d.Logger.Error("oauth: revoke", "err", err)
				e = errServer
			}
		}
	}
	if e != nil {
		writeTokenError(w, r, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

func (m *module) revokeToken(ctx context.Context, client db.OauthClient, tok, hint string) error {
	q := db.New(m.d.DB)
	hash := token.Hash(tok)
	refresh := func() (bool, error) {
		rt, err := q.GetRefreshTokenByHashForClient(ctx, db.GetRefreshTokenByHashForClientParams{TokenHash: hash, ClientID: client.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, m.revokeFamily(ctx, q, rt.FamilyID)
	}
	access := func() (bool, error) {
		n, err := q.RevokeAccessTokenByHash(ctx, db.RevokeAccessTokenByHashParams{TokenHash: hash, ClientID: client.ID})
		return n > 0, err
	}
	order := []func() (bool, error){refresh, access}
	if hint == "access_token" {
		order = []func() (bool, error){access, refresh}
	}
	for _, f := range order {
		if done, err := f(); done || err != nil {
			return err
		}
	}
	return nil
}
