package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/db"
)

// waUser adapts an account to go-webauthn. The user handle is the user id's
// 16 bytes, so a discoverable credential names its account directly.
type waUser struct {
	id    uuid.UUID
	name  string
	creds []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte                         { b := u.id; return b[:] }
func (u *waUser) WebAuthnName() string                       { return u.name }
func (u *waUser) WebAuthnDisplayName() string                { return u.name }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func toCredential(p db.PasskeyCredential) webauthn.Credential {
	transports := make([]protocol.AuthenticatorTransport, len(p.Transports))
	for i, t := range p.Transports {
		transports[i] = protocol.AuthenticatorTransport(t)
	}
	return webauthn.Credential{
		ID:              p.CredentialID,
		PublicKey:       p.PublicKey,
		AttestationType: p.AttestationType,
		Transport:       transports,
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   p.UserVerified,
			BackupEligible: p.BackupEligible,
			BackupState:    p.BackupState,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:       p.Aaguid,
			SignCount:    uint32(p.SignCount),
			CloneWarning: p.CloneWarning,
			Attachment:   protocol.AuthenticatorAttachment(p.Attachment),
		},
	}
}

// loadUser returns userID's go-webauthn view, named by its primary email.
func loadUser(ctx context.Context, q *db.Queries, userID uuid.UUID) (*waUser, error) {
	rows, err := q.ListPasskeys(ctx, userID)
	if err != nil {
		return nil, err
	}
	u := &waUser{id: userID, name: "handbill account"}
	for _, p := range rows {
		u.creds = append(u.creds, toCredential(p))
	}
	emails, err := q.ListUserEmails(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(emails) > 0 {
		u.name = emails[0].Email
	}
	return u, nil
}

func descriptors(creds []webauthn.Credential) []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, len(creds))
	for i := range creds {
		out[i] = creds[i].Descriptor()
	}
	return out
}

func insertPasskey(ctx context.Context, q *db.Queries, userID uuid.UUID, c *webauthn.Credential, name string) (db.PasskeyCredential, error) {
	transports := make([]string, len(c.Transport))
	for i, t := range c.Transport {
		transports[i] = string(t)
	}
	p, err := q.CreatePasskey(ctx, db.CreatePasskeyParams{
		ID:              newID(),
		UserID:          userID,
		CredentialID:    c.ID,
		PublicKey:       c.PublicKey,
		AttestationType: c.AttestationType,
		Aaguid:          c.Authenticator.AAGUID,
		SignCount:       int64(c.Authenticator.SignCount),
		Transports:      transports,
		Attachment:      string(c.Authenticator.Attachment),
		UserVerified:    c.Flags.UserVerified,
		BackupEligible:  c.Flags.BackupEligible,
		BackupState:     c.Flags.BackupState,
		Name:            name,
	})
	if isUniqueViolation(err) {
		return p, appapi.NewError(http.StatusConflict, "passkey_exists", "That passkey is already registered.")
	}
	return p, err
}

type passkeyView struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Synced       bool       `json:"synced"`
	Attachment   string     `json:"attachment"`
	CloneWarning bool       `json:"cloneWarning"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastUsedAt   *time.Time `json:"lastUsedAt"`
}

func toPasskeyView(p db.PasskeyCredential) passkeyView {
	return passkeyView{
		ID: p.ID, Name: p.Name, Synced: p.BackupState, Attachment: p.Attachment,
		CloneWarning: p.CloneWarning, CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt,
	}
}

// passkeyFailed hides go-webauthn's reason from the client; it is only
// useful for debugging, so it is logged at debug level.
func (m *module) passkeyFailed(err error) error {
	m.d.Logger.Debug("passkey verification failed", "err", err)
	return appapi.NewError(http.StatusBadRequest, "passkey_failed", "The passkey could not be verified. Try again.")
}

type beginResponse struct {
	Flow    string `json:"flow"`
	Options any    `json:"options"`
}

type finishRequest struct {
	Flow       string          `json:"flow"`
	Credential json.RawMessage `json:"credential"`
	Name       string          `json:"name,omitempty"`
}

// Sign-up: one screen, an email and a new passkey.

type signupFlow struct {
	Session webauthn.SessionData `json:"session"`
	UserID  uuid.UUID            `json:"userId"`
	Email   string               `json:"email"`
}

func (m *module) signupBegin(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email string `json:"email"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	email, ok := normalizeEmail(in.Email)
	if !ok {
		return errBadRequest("invalid_email", "Enter a valid email address.")
	}
	u := &waUser{id: newID(), name: email}
	creation, sess, err := m.wa.BeginRegistration(u)
	if err != nil {
		return err
	}
	flow, err := m.startFlow(r.Context(), flowPasskeySignup, uuid.NullUUID{}, signupFlow{Session: *sess, UserID: u.id, Email: email})
	if err != nil {
		return err
	}
	appapi.WriteJSON(w, http.StatusOK, beginResponse{Flow: flow, Options: creation.Response})
	return nil
}

func (m *module) signupFinish(w http.ResponseWriter, r *http.Request) error {
	var in finishRequest
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	var f signupFlow
	if _, err := consumeFlow(r.Context(), m.q, flowPasskeySignup, in.Flow, &f); err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(in.Credential)
	if err != nil {
		return m.passkeyFailed(err)
	}
	cred, err := m.wa.CreateCredential(&waUser{id: f.UserID, name: f.Email}, f.Session, parsed)
	if err != nil {
		return m.passkeyFailed(err)
	}
	var emailID uuid.UUID
	err = db.InTx(r.Context(), m.d.DB, func(q *db.Queries) error {
		if _, err := q.CreateUser(r.Context(), f.UserID); err != nil {
			return err
		}
		e, err := q.CreateEmail(r.Context(), db.CreateEmailParams{ID: newID(), UserID: f.UserID, Email: f.Email, IsPrimary: true})
		if err != nil {
			return err
		}
		emailID = e.ID
		if _, err := insertPasskey(r.Context(), q, f.UserID, cred, passkeyName(r.UserAgent(), cred)); err != nil {
			return err
		}
		return m.replaceSession(r.Context(), q, w, r, f.UserID, false)
	})
	if err != nil {
		return err
	}
	if m.mail.Enabled() {
		if err := m.sendVerificationEmail(r.Context(), emailID, f.Email); err != nil {
			m.d.Logger.Warn("sign-up verification email failed", "err", err)
		}
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"user": meUser{ID: f.UserID, Email: f.Email}})
	return nil
}

// Sign-in: usernameless (discoverable credential) or email-first.

type loginFlow struct {
	Session webauthn.SessionData `json:"session"`
	// Candidates limits an email-first sign-in to the accounts claiming the
	// address; empty for a usernameless one.
	Candidates []uuid.UUID `json:"candidates,omitempty"`
}

func (m *module) loginBegin(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email string `json:"email"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	var (
		f    loginFlow
		opts []webauthn.LoginOption
	)
	if strings.TrimSpace(in.Email) != "" {
		email, ok := normalizeEmail(in.Email)
		if !ok {
			return errBadRequest("invalid_email", "Enter a valid email address.")
		}
		users, creds, err := m.emailCandidates(r.Context(), email)
		if err != nil {
			return err
		}
		f.Candidates = users
		if len(creds) == 0 {
			return appapi.NewError(http.StatusNotFound, "no_passkey", "No passkey is registered for that email. Try Google or a recovery code.")
		}
		opts = append(opts, webauthn.WithAllowedCredentials(descriptors(creds)))
	}
	assertion, sess, err := m.wa.BeginDiscoverableLogin(opts...)
	if err != nil {
		return err
	}
	f.Session = *sess
	flow, err := m.startFlow(r.Context(), flowPasskeyLogin, uuid.NullUUID{}, f)
	if err != nil {
		return err
	}
	appapi.WriteJSON(w, http.StatusOK, beginResponse{Flow: flow, Options: assertion.Response})
	return nil
}

// emailCandidates returns the accounts an email-first sign-in may reach:
// the verified holder of the address if there is one, else every account
// claiming it unverified, with their credentials.
func (m *module) emailCandidates(ctx context.Context, email string) ([]uuid.UUID, []webauthn.Credential, error) {
	claims, err := m.q.ListEmailClaims(ctx, email)
	if err != nil {
		return nil, nil, err
	}
	var (
		users []uuid.UUID
		creds []webauthn.Credential
	)
	if len(claims) > 0 && claims[0].Verified {
		claims = claims[:1]
	}
	for _, c := range claims {
		users = append(users, c.UserID)
		rows, err := m.q.ListPasskeys(ctx, c.UserID)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range rows {
			creds = append(creds, toCredential(p))
		}
	}
	return users, creds, nil
}

var errUnknownPasskey = appapi.NewError(http.StatusBadRequest, "unknown_passkey",
	"This passkey isn't registered here. It may have been removed, or belong to another account.")

func (m *module) loginFinish(w http.ResponseWriter, r *http.Request) error {
	var in finishRequest
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	var f loginFlow
	if _, err := consumeFlow(r.Context(), m.q, flowPasskeyLogin, in.Flow, &f); err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Credential)
	if err != nil {
		return m.passkeyFailed(err)
	}
	row, err := m.q.GetPasskeyByCredentialID(r.Context(), parsed.RawID)
	if isNoRows(err) {
		return errUnknownPasskey
	}
	if err != nil {
		return err
	}
	if len(f.Candidates) > 0 && !slices.Contains(f.Candidates, row.UserID) {
		return errUnknownPasskey
	}
	u, err := loadUser(r.Context(), m.q, row.UserID)
	if err != nil {
		return err
	}
	// The ceremony started without a user (usernameless, or several
	// candidate accounts); bind it to the credential's owner, whose handle
	// go-webauthn then checks against the assertion's.
	sess := f.Session
	sess.UserID = u.WebAuthnID()
	sess.AllowedCredentialIDs = nil
	cred, err := m.wa.ValidateLogin(u, sess, parsed)
	if err != nil {
		return m.passkeyFailed(err)
	}
	if cred.Authenticator.CloneWarning {
		// The signature counter went backwards: two copies of the key exist.
		if err := m.q.SetPasskeyCloneWarning(r.Context(), row.ID); err != nil {
			return err
		}
		return appapi.NewError(http.StatusForbidden, "passkey_cloned",
			"This passkey reported an out-of-order signature counter, which can mean it was copied. Use another passkey or a recovery code, then remove this one.")
	}
	err = db.InTx(r.Context(), m.d.DB, func(q *db.Queries) error {
		if err := q.UpdatePasskeyUse(r.Context(), db.UpdatePasskeyUseParams{
			ID: row.ID, SignCount: int64(cred.Authenticator.SignCount), BackupState: cred.Flags.BackupState,
			UserVerified: cred.Flags.UserVerified, CloneWarning: false,
		}); err != nil {
			return err
		}
		return m.replaceSession(r.Context(), q, w, r, row.UserID, false)
	})
	if err != nil {
		return err
	}
	return m.writeUser(w, r.Context(), row.UserID)
}

// writeUser answers {"user": ...} for a freshly signed-in account.
func (m *module) writeUser(w http.ResponseWriter, ctx context.Context, userID uuid.UUID) error {
	out := meUser{ID: userID}
	emails, err := m.q.ListUserEmails(ctx, userID)
	if err != nil {
		return err
	}
	if len(emails) > 0 && emails[0].IsPrimary {
		out.Email, out.EmailVerified = emails[0].Email, emails[0].Verified
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"user": out})
	return nil
}

// Adding a passkey: from settings, or from a recovery session (where it
// upgrades the session to a normal one).

type registerFlow struct {
	Session webauthn.SessionData `json:"session"`
}

func (m *module) registerBegin(w http.ResponseWriter, r *http.Request) error {
	s, ok := sessionFrom(r.Context())
	if !ok {
		return errUnauth
	}
	u, err := loadUser(r.Context(), m.q, s.UserID)
	if err != nil {
		return err
	}
	creation, sess, err := m.wa.BeginRegistration(u, webauthn.WithExclusions(descriptors(u.creds)))
	if err != nil {
		return err
	}
	flow, err := m.startFlow(r.Context(), flowPasskeyRegister, uuid.NullUUID{UUID: s.UserID, Valid: true}, registerFlow{Session: *sess})
	if err != nil {
		return err
	}
	appapi.WriteJSON(w, http.StatusOK, beginResponse{Flow: flow, Options: creation.Response})
	return nil
}

func (m *module) registerFinish(w http.ResponseWriter, r *http.Request) error {
	s, ok := sessionFrom(r.Context())
	if !ok {
		return errUnauth
	}
	var in finishRequest
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	var f registerFlow
	af, err := consumeFlow(r.Context(), m.q, flowPasskeyRegister, in.Flow, &f)
	if err != nil {
		return err
	}
	if !af.UserID.Valid || af.UserID.UUID != s.UserID {
		return errFlowExpired
	}
	name, err := passkeyNameInput(in.Name)
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(in.Credential)
	if err != nil {
		return m.passkeyFailed(err)
	}
	u, err := loadUser(r.Context(), m.q, s.UserID)
	if err != nil {
		return err
	}
	cred, err := m.wa.CreateCredential(u, f.Session, parsed)
	if err != nil {
		return m.passkeyFailed(err)
	}
	if name == "" {
		name = passkeyName(r.UserAgent(), cred)
	}
	var p db.PasskeyCredential
	err = db.InTx(r.Context(), m.d.DB, func(q *db.Queries) error {
		var err error
		if p, err = insertPasskey(r.Context(), q, s.UserID, cred, name); err != nil {
			return err
		}
		if s.Restricted {
			return m.replaceSession(r.Context(), q, w, r, s.UserID, false)
		}
		return nil
	})
	if err != nil {
		return err
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"passkey": toPasskeyView(p), "upgraded": s.Restricted})
	return nil
}

func passkeyNameInput(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 64 {
		return "", errBadRequest("invalid_name", "Keep the name under 64 characters.")
	}
	return s, nil
}

func (m *module) renamePasskey(w http.ResponseWriter, r *http.Request) error {
	s, err := fullSession(r)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return errPasskeyNotFound
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	name, err := passkeyNameInput(in.Name)
	if err != nil {
		return err
	}
	if name == "" {
		return errBadRequest("invalid_name", "Give the passkey a name.")
	}
	p, err := m.q.RenamePasskey(r.Context(), db.RenamePasskeyParams{ID: id, UserID: s.UserID, Name: name})
	if isNoRows(err) {
		return errPasskeyNotFound
	}
	if err != nil {
		return err
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"passkey": toPasskeyView(p)})
	return nil
}

var errPasskeyNotFound = appapi.NewError(http.StatusNotFound, "not_found", "That passkey no longer exists.")

var errLastMethod = appapi.NewError(http.StatusConflict, "last_method",
	"This is your only way to sign in. Add another passkey or link Google first.")

func (m *module) deletePasskey(w http.ResponseWriter, r *http.Request) error {
	s, err := fullSession(r)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return errPasskeyNotFound
	}
	err = db.InTx(r.Context(), m.d.DB, func(q *db.Queries) error {
		if err := q.LockUser(r.Context(), s.UserID); err != nil {
			return err
		}
		deleted, err := q.DeletePasskey(r.Context(), db.DeletePasskeyParams{ID: id, UserID: s.UserID})
		if err != nil {
			return err
		}
		if deleted == 0 {
			return errPasskeyNotFound
		}
		// Rolled back when nothing would be left to sign in with.
		n, err := q.CountSignInMethods(r.Context(), s.UserID)
		if err != nil {
			return err
		}
		if n == 0 {
			return errLastMethod
		}
		return nil
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
