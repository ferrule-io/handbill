package oauth

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/token"
)

type consentClient struct {
	ClientID string `json:"clientId"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Verified bool   `json:"verified"`
}

type consentRedirect struct {
	URI      string `json:"uri"`
	Host     string `json:"host"`
	Loopback bool   `json:"loopback"`
}

type consentRequest struct {
	Client               consentClient   `json:"client"`
	Redirect             consentRedirect `json:"redirect"`
	Account              consentAccount  `json:"account"`
	PreviouslyAuthorized bool            `json:"previouslyAuthorized"`
	ExpiresAt            time.Time       `json:"expiresAt"`
}

type consentAccount struct {
	Email string `json:"email"`
}

var errRequestNotFound = appapi.NewError(http.StatusNotFound, "request_not_found",
	"This request expired or belongs to another account. Start the connection again from your app.")

// describeClient is how the consent screen and the grants list show a
// client. A CIMD client's host is its client_id URL's host; a DCR client
// is known only by its first redirect URI's host and is unverified.
func describeClient(clientID, name, registration string, redirectURIs []string) consentClient {
	c := consentClient{ClientID: clientID, Name: name, Verified: registration == registrationCIMD}
	hostOf := clientID
	if !c.Verified && len(redirectURIs) > 0 {
		hostOf = redirectURIs[0]
	}
	if u, err := url.Parse(hostOf); err == nil {
		c.Host = u.Host
	}
	return c
}

// getConsent is GET /app-api/oauth/consent?request=<t>.
func (m *module) getConsent(w http.ResponseWriter, r *http.Request) error {
	user, _ := identity.FromContext(r.Context())
	q := db.New(m.d.DB)
	req, err := q.GetAuthorizationRequest(r.Context(), db.GetAuthorizationRequestParams{
		TokenHash: token.Hash(r.URL.Query().Get("request")), UserID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errRequestNotFound
	}
	if err != nil {
		return err
	}
	prev, err := q.GrantExists(r.Context(), db.GrantExistsParams{UserID: user.ID, ClientID: req.ClientID})
	if err != nil {
		return err
	}
	redirect := consentRedirect{URI: req.RedirectUri, Loopback: isLoopbackRedirect(req.RedirectUri)}
	if u, err := url.Parse(req.RedirectUri); err == nil {
		redirect.Host = u.Host
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"request": consentRequest{
		Client:               describeClient(req.ClientKey, req.ClientName, req.Registration, req.RedirectUris),
		Redirect:             redirect,
		Account:              consentAccount{Email: user.Email},
		PreviouslyAuthorized: prev,
		ExpiresAt:            req.ExpiresAt.UTC(),
	}})
	return nil
}

// postConsent is POST /app-api/oauth/consent {"request", "approve"}: it
// consumes the request and answers where to send the browser.
func (m *module) postConsent(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Request string `json:"request"`
		Approve bool   `json:"approve"`
	}
	if err := appapi.ReadJSON(r, &body); err != nil {
		return err
	}
	user, _ := identity.FromContext(r.Context())
	iss := Issuer(m.d.Config)
	var redirectTo string
	err := db.InTx(r.Context(), m.d.DB, func(q *db.Queries) error {
		req, err := q.TakeAuthorizationRequest(r.Context(), db.TakeAuthorizationRequestParams{
			TokenHash: token.Hash(body.Request), UserID: user.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errRequestNotFound
		}
		if err != nil {
			return err
		}
		if !body.Approve {
			redirectTo = errorRedirectURL(req.RedirectUri, iss, "access_denied", "The user denied access.", req.State)
			return nil
		}
		grant, err := q.UpsertGrant(r.Context(), db.UpsertGrantParams{
			ID: uuid.Must(uuid.NewV7()), UserID: user.ID, ClientID: req.ClientID, Scopes: req.Scopes,
		})
		if err != nil {
			return err
		}
		code, hash := token.New()
		if _, err := q.CreateAuthorizationCode(r.Context(), db.CreateAuthorizationCodeParams{
			ID:            uuid.Must(uuid.NewV7()),
			TokenHash:     hash,
			GrantID:       grant.ID,
			RedirectUri:   req.RedirectUri,
			CodeChallenge: req.CodeChallenge,
			Resource:      req.Resource,
			Scopes:        req.Scopes,
			ExpiresAt:     m.now().Add(codeTTL),
		}); err != nil {
			return err
		}
		p := url.Values{"code": {code}, "iss": {iss}}
		if req.State != nil {
			p.Set("state", *req.State)
		}
		redirectTo = redirectURL(req.RedirectUri, p)
		return nil
	})
	if err != nil {
		return err
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]string{"redirectTo": redirectTo})
	return nil
}
