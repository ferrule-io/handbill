-- Accounts and sessions (docs/design.md#accounts, roadmap step 3).

-- +goose Up

-- A recovery-code sign-in creates a restricted session: it can only register
-- a new passkey, which replaces it with a normal session.
ALTER TABLE sessions ADD COLUMN restricted boolean NOT NULL DEFAULT false;

-- Sign-in ceremonies in flight: WebAuthn challenges (go-webauthn SessionData)
-- and Google OIDC state, nonce, and PKCE verifier. The browser holds the
-- plaintext token; a flow is single use (consumed by DELETE ... RETURNING)
-- and short-lived. user_id is set for ceremonies started while signed in
-- (adding a passkey, linking Google).
CREATE TABLE auth_flows (
    id uuid PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    kind text NOT NULL CHECK (kind IN ('passkey_signup', 'passkey_login', 'passkey_register', 'google')),
    user_id uuid REFERENCES users (id) ON DELETE CASCADE,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX auth_flows_expires_at ON auth_flows (expires_at);
CREATE INDEX auth_flows_user_id ON auth_flows (user_id);

-- +goose Down

DROP TABLE auth_flows;
ALTER TABLE sessions DROP COLUMN restricted;
