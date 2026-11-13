package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/token"
)

// recoveryCodeCount codes are issued at a time (docs/design.md#accounts).
const recoveryCodeCount = 10

// issueRecoveryCodes replaces userID's recovery codes with a fresh set and
// returns their plaintext, which is shown once and never stored.
func issueRecoveryCodes(ctx context.Context, q *db.Queries, userID uuid.UUID) ([]string, error) {
	if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
		return nil, err
	}
	codes := make([]string, recoveryCodeCount)
	for i := range codes {
		codes[i] = token.NewRecoveryCode()
		h, err := token.HashRecoveryCode(codes[i])
		if err != nil {
			return nil, err
		}
		if err := q.CreateRecoveryCode(ctx, db.CreateRecoveryCodeParams{ID: newID(), UserID: userID, CodeHash: h}); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

// regenerateRecoveryCodes issues a new set, invalidating every earlier code.
// The sign-up flow calls it once to show the first set.
func (m *module) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) error {
	s, err := fullSession(r)
	if err != nil {
		return err
	}
	var codes []string
	err = db.InTx(r.Context(), m.d.DB, func(q *db.Queries) error {
		if err := q.LockUser(r.Context(), s.UserID); err != nil {
			return err
		}
		codes, err = issueRecoveryCodes(r.Context(), q, s.UserID)
		return err
	})
	if err != nil {
		return err
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]any{"codes": codes})
	return nil
}

var errBadRecovery = appapi.NewError(http.StatusUnauthorized, "invalid_recovery",
	"That email and recovery code don't match an account. Check the code and try again.")

// dummyRecoveryHash keeps the response time of an unknown email close to
// that of a known one.
var dummyRecoveryHash, _ = token.HashRecoveryCode(token.NewRecoveryCode())

// recoverySignIn trades an email and an unused recovery code for a
// restricted session that can only register a new passkey.
func (m *module) recoverySignIn(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	email, ok := normalizeEmail(in.Email)
	if !ok {
		return errBadRequest("invalid_email", "Enter a valid email address.")
	}
	claims, err := m.q.ListEmailClaims(r.Context(), email)
	if err != nil {
		return err
	}
	if len(claims) == 0 {
		token.VerifyRecoveryCode(dummyRecoveryHash, in.Code)
		return errBadRecovery
	}
	for _, c := range claims {
		codes, err := m.q.ListUnusedRecoveryCodes(r.Context(), c.UserID)
		if err != nil {
			return err
		}
		for _, code := range codes {
			if !token.VerifyRecoveryCode(code.CodeHash, in.Code) {
				continue
			}
			err := db.InTx(r.Context(), m.d.DB, func(q *db.Queries) error {
				n, err := q.ConsumeRecoveryCode(r.Context(), code.ID)
				if err != nil {
					return err
				}
				if n == 0 { // used concurrently
					return errBadRecovery
				}
				return m.replaceSession(r.Context(), q, w, r, c.UserID, true)
			})
			if err != nil {
				return err
			}
			appapi.WriteJSON(w, http.StatusOK, map[string]bool{"recovery": true})
			return nil
		}
	}
	return errBadRecovery
}
