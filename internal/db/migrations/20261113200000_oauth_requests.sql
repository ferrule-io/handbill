-- Pending OAuth authorization requests (docs/plans/w5-oauth-mcp.md):
-- /oauth/authorize validates the request, stores it here bound to the
-- signed-in user, and sends the browser to /consent?request=<token>. The
-- decision consumes the row (DELETE ... RETURNING). The plaintext handle is
-- a token.New() value.

-- +goose Up

CREATE TABLE oauth_authorization_requests (
    id             uuid PRIMARY KEY,
    token_hash     bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id      uuid NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    redirect_uri   text NOT NULL,
    state          text,
    code_challenge text NOT NULL,
    resource       text NOT NULL,
    scopes         text[] NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL
);
CREATE INDEX oauth_authorization_requests_user_id ON oauth_authorization_requests (user_id);
CREATE INDEX oauth_authorization_requests_expires_at ON oauth_authorization_requests (expires_at);

-- +goose Down

DROP TABLE oauth_authorization_requests;
