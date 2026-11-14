package oauth

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/token"
)

// tokenResponse is a successful token response (OAuth 2.1 §3.2.3).
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// readForm parses an application/x-www-form-urlencoded body of at most
// maxFormBytes. A parameter that appears more than once is invalid_request.
func readForm(w http.ResponseWriter, r *http.Request) (url.Values, *oauthError) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/x-www-form-urlencoded" {
		return nil, badRequest("invalid_request", "The body must be application/x-www-form-urlencoded.")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		return nil, badRequest("invalid_request", "The request body could not be read.")
	}
	for k, vs := range r.PostForm {
		if len(vs) > 1 {
			return nil, badRequest("invalid_request", "The "+k+" parameter must not be repeated.")
		}
	}
	return r.PostForm, nil
}

// authenticateClient finds the public client a token or revocation request
// names. Any client authentication is refused (CIMD-03 §5): handbill
// issues no secrets. This runs before any code or token is touched, so a
// client that tries HTTP Basic first can retry with form parameters.
func (m *module) authenticateClient(ctx context.Context, r *http.Request, form url.Values) (db.OauthClient, *oauthError) {
	if r.Header.Get("Authorization") != "" || form.Has("client_secret") || form.Has("client_assertion") || form.Has("client_assertion_type") {
		return db.OauthClient{}, newError(http.StatusUnauthorized, "invalid_client", "Only public clients are supported: send client_id without client authentication.")
	}
	clientID := form.Get("client_id")
	if clientID == "" {
		return db.OauthClient{}, newError(http.StatusUnauthorized, "invalid_client", "client_id is required.")
	}
	c, err := db.New(m.d.DB).GetClientByClientID(ctx, clientID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && c.Registration == registrationDCR && !m.d.Config.OAuthDCREnabled) {
		return db.OauthClient{}, newError(http.StatusUnauthorized, "invalid_client", "Unknown client.")
	}
	if err != nil {
		m.d.Logger.Error("oauth: load client", "err", err)
		return db.OauthClient{}, newError(http.StatusInternalServerError, "server_error", "Try again.")
	}
	return c, nil
}

func writeTokenError(w http.ResponseWriter, r *http.Request, e *oauthError) {
	if e.status == http.StatusUnauthorized && r.Header.Get("Authorization") != "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="handbill"`)
	}
	writeError(w, e)
}

// token is POST /oauth/token (plan §4.5).
func (m *module) token(w http.ResponseWriter, r *http.Request) {
	form, e := readForm(w, r)
	if e == nil {
		var client db.OauthClient
		client, e = m.authenticateClient(r.Context(), r, form)
		if e == nil {
			var resp tokenResponse
			switch form.Get("grant_type") {
			case "":
				e = badRequest("invalid_request", "grant_type is required.")
			case grantAuthorizationCode:
				resp, e = m.exchangeCode(r.Context(), client, form)
			case grantRefreshToken:
				resp, e = m.refresh(r.Context(), client, form)
			default:
				e = badRequest("unsupported_grant_type", "grant_type must be authorization_code or refresh_token.")
			}
			if e == nil {
				w.Header().Set("Pragma", "no-cache")
				writeJSON(w, http.StatusOK, resp)
				return
			}
		}
	}
	writeTokenError(w, r, e)
}

var errServer = newError(http.StatusInternalServerError, "server_error", "Something went wrong on our side. Try again.")

func invalidGrant(description string) *oauthError { return badRequest("invalid_grant", description) }

// exchangeCode consumes the code and issues its tokens in one transaction,
// so a concurrent replay, which locks the consumed code row before revoking
// the family, waits until those tokens exist. A code that fails a check
// below stays consumed.
func (m *module) exchangeCode(ctx context.Context, client db.OauthClient, form url.Values) (tokenResponse, *oauthError) {
	verifier := form.Get("code_verifier")
	if !validVerifier(verifier) {
		return tokenResponse{}, badRequest("invalid_request", "code_verifier is missing or malformed.")
	}
	hash := token.Hash(form.Get("code"))
	var resp tokenResponse
	var oe *oauthError
	err := db.InTx(ctx, m.d.DB, func(q *db.Queries) error {
		code, err := q.ConsumeAuthorizationCode(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			oe = invalidGrant("The authorization code is invalid, expired, or already used.")
			old, err := q.GetAuthorizationCodeForUpdate(ctx, hash)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && old.ConsumedAt == nil) {
				return nil
			}
			if err != nil {
				return err
			}
			// A replayed code may have leaked: revoke what it issued.
			if err := m.revokeFamily(ctx, q, old.ID); err != nil {
				return err
			}
			m.d.Logger.Warn("oauth: authorization code reused; token family revoked", "family_id", old.ID)
			return nil
		}
		if err != nil {
			return err
		}
		grant, err := q.GetGrantWithClient(ctx, code.GrantID)
		if errors.Is(err, pgx.ErrNoRows) {
			oe = invalidGrant("The authorization was revoked.")
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case grant.ClientKey != client.ClientID:
			oe = invalidGrant("The authorization code was issued to another client.")
		case form.Get("redirect_uri") != code.RedirectUri:
			oe = invalidGrant("redirect_uri does not match the authorization request.")
		case !verifyPKCE(verifier, code.CodeChallenge):
			oe = invalidGrant("code_verifier does not match the code challenge.")
		case form.Has("resource") && form.Get("resource") != code.Resource:
			oe = badRequest("invalid_target", "The resource does not match the authorization request.")
		default:
			resp, err = m.issue(ctx, q, grant.ID, code.ID, uuid.NullUUID{}, code.Resource, code.Scopes, effectiveGrantTypes(grant.Metadata))
		}
		return err
	})
	if err != nil {
		m.d.Logger.Error("oauth: exchange code", "err", err)
		return tokenResponse{}, errServer
	}
	if oe != nil {
		return tokenResponse{}, oe
	}
	return resp, nil
}

func (m *module) refresh(ctx context.Context, client db.OauthClient, form url.Values) (tokenResponse, *oauthError) {
	var resp tokenResponse
	var oe *oauthError
	err := db.InTx(ctx, m.d.DB, func(q *db.Queries) error {
		rt, err := q.GetRefreshTokenForUpdate(ctx, token.Hash(form.Get("refresh_token")))
		if errors.Is(err, pgx.ErrNoRows) {
			oe = invalidGrant("The refresh token is invalid.")
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case rt.RevokedAt != nil:
			oe = invalidGrant("The refresh token was revoked.")
			return nil
		case rt.RotatedAt != nil:
			// Reuse of a rotated token (OAuth 2.1 §4.3.1): revoke the family.
			// There is no grace window.
			if err := m.revokeFamily(ctx, q, rt.FamilyID); err != nil {
				return err
			}
			m.d.Logger.Warn("oauth: refresh token reused; token family revoked", "family_id", rt.FamilyID)
			oe = invalidGrant("The refresh token was already used.")
			return nil
		case !rt.ExpiresAt.After(m.now()):
			oe = invalidGrant("The refresh token expired.")
			return nil
		}
		grant, err := q.GetGrantWithClient(ctx, rt.GrantID)
		if err != nil {
			return err
		}
		if grant.ClientKey != client.ClientID {
			oe = invalidGrant("The refresh token was issued to another client.")
			return nil
		}
		grantTypes := effectiveGrantTypes(grant.Metadata)
		if !slices.Contains(grantTypes, grantRefreshToken) {
			oe = badRequest("unauthorized_client", "The client is not registered for the refresh_token grant.")
			return nil
		}
		if form.Has("resource") && form.Get("resource") != rt.Resource {
			oe = badRequest("invalid_target", "The resource does not match the refresh token.")
			return nil
		}
		if form.Has("scope") {
			for _, s := range strings.Fields(form.Get("scope")) {
				if !slices.Contains(rt.Scopes, s) {
					oe = badRequest("invalid_scope", "The scope exceeds the original grant.")
					return nil
				}
			}
		}
		if err := q.MarkRefreshTokenRotated(ctx, rt.ID); err != nil {
			return err
		}
		resp, err = m.issue(ctx, q, rt.GrantID, rt.FamilyID, uuid.NullUUID{UUID: rt.ID, Valid: true}, rt.Resource, rt.Scopes, grantTypes)
		return err
	})
	if err != nil {
		m.d.Logger.Error("oauth: refresh", "err", err)
		return tokenResponse{}, errServer
	}
	if oe != nil {
		return tokenResponse{}, oe
	}
	return resp, nil
}

// issue creates an access token and, when the client may refresh, a
// refresh token in a family, and touches the grant.
func (m *module) issue(ctx context.Context, q *db.Queries, grantID, family uuid.UUID, parent uuid.NullUUID, resource string, scopes, grantTypes []string) (tokenResponse, error) {
	now := m.now()
	access, accessHash := token.New()
	if _, err := q.CreateAccessToken(ctx, db.CreateAccessTokenParams{
		ID: uuid.Must(uuid.NewV7()), TokenHash: accessHash, GrantID: grantID, FamilyID: family,
		Resource: resource, Scopes: scopes, ExpiresAt: now.Add(accessTokenTTL),
	}); err != nil {
		return tokenResponse{}, err
	}
	resp := tokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(accessTokenTTL.Seconds())}
	if slices.Contains(grantTypes, grantRefreshToken) {
		refresh, refreshHash := token.New()
		if _, err := q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
			ID: uuid.Must(uuid.NewV7()), TokenHash: refreshHash, GrantID: grantID, FamilyID: family,
			ParentID: parent, Resource: resource, Scopes: scopes, ExpiresAt: now.Add(refreshTokenTTL),
		}); err != nil {
			return tokenResponse{}, err
		}
		resp.RefreshToken = refresh
	}
	if len(scopes) > 0 {
		resp.Scope = strings.Join(scopes, " ")
	}
	return resp, q.TouchGrant(ctx, grantID)
}

func (m *module) revokeFamily(ctx context.Context, q *db.Queries, family uuid.UUID) error {
	if err := q.RevokeAccessTokenFamily(ctx, family); err != nil {
		return err
	}
	return q.RevokeRefreshTokenFamily(ctx, family)
}
