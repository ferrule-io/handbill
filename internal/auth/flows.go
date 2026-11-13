package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mjquinlan2000/agent-dash/internal/appapi"
	"github.com/mjquinlan2000/agent-dash/internal/db"
	"github.com/mjquinlan2000/agent-dash/internal/token"
)

// flowTTL bounds every sign-in ceremony (WebAuthn prompt, Google round trip).
const flowTTL = 10 * time.Minute

const (
	flowPasskeySignup   = "passkey_signup"
	flowPasskeyLogin    = "passkey_login"
	flowPasskeyRegister = "passkey_register"
	flowGoogle          = "google"
)

var errFlowExpired = appapi.NewError(http.StatusBadRequest, "flow_expired", "That sign-in attempt expired. Start again.")

// startFlow stores data for a ceremony and returns the token the browser
// presents to finish it.
func (m *module) startFlow(ctx context.Context, kind string, userID uuid.NullUUID, data any) (string, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	// Abandoned ceremonies are cleaned up as new ones start.
	if err := m.q.DeleteExpiredAuthFlows(ctx); err != nil {
		return "", err
	}
	plain, hash := token.New()
	err = m.q.CreateAuthFlow(ctx, db.CreateAuthFlowParams{
		ID: newID(), TokenHash: hash, Kind: kind, UserID: userID, Data: b,
		ExpiresAt: time.Now().Add(flowTTL),
	})
	return plain, err
}

// consumeFlow ends a ceremony, decoding its data into dst. Unknown, used,
// and expired flows are errFlowExpired.
func consumeFlow(ctx context.Context, q *db.Queries, kind, plain string, dst any) (db.AuthFlow, error) {
	if plain == "" {
		return db.AuthFlow{}, errFlowExpired
	}
	f, err := q.ConsumeAuthFlow(ctx, db.ConsumeAuthFlowParams{TokenHash: token.Hash(plain), Kind: kind})
	if isNoRows(err) {
		return f, errFlowExpired
	}
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(f.Data, dst)
}
