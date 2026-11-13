-- OAuth clients, authorization requests, grants, codes, and tokens
-- (internal/oauth).

-- name: GetClientByClientID :one
SELECT * FROM oauth_clients WHERE client_id = @client_id;

-- UpsertCIMDClient stores a freshly fetched client ID metadata document.
-- name: UpsertCIMDClient :one
INSERT INTO oauth_clients (
    id, client_id, registration, metadata, client_name, redirect_uris,
    token_endpoint_auth_method, fetched_at, cache_expires_at, etag, last_modified
) VALUES (
    @id, @client_id, 'cimd', @metadata, @client_name, @redirect_uris,
    @token_endpoint_auth_method, now(), @cache_expires_at, @etag, @last_modified
)
ON CONFLICT (client_id) DO UPDATE SET
    metadata = EXCLUDED.metadata,
    client_name = EXCLUDED.client_name,
    redirect_uris = EXCLUDED.redirect_uris,
    token_endpoint_auth_method = EXCLUDED.token_endpoint_auth_method,
    fetched_at = EXCLUDED.fetched_at,
    cache_expires_at = EXCLUDED.cache_expires_at,
    etag = EXCLUDED.etag,
    last_modified = EXCLUDED.last_modified,
    updated_at = now()
WHERE oauth_clients.registration = 'cimd'
RETURNING *;

-- RefreshCIMDClientCache records a 304 revalidation: the document is unchanged.
-- name: RefreshCIMDClientCache :exec
UPDATE oauth_clients
SET fetched_at = now(), cache_expires_at = @cache_expires_at, etag = @etag,
    last_modified = @last_modified, updated_at = now()
WHERE id = @id AND registration = 'cimd';

-- name: CreateDCRClient :one
INSERT INTO oauth_clients (id, client_id, registration, metadata, client_name, redirect_uris, token_endpoint_auth_method)
VALUES (@id, @id::text, 'dcr', @metadata, @client_name, @redirect_uris, 'none')
RETURNING *;

-- name: DeleteClientGrants :execrows
DELETE FROM oauth_grants WHERE client_id = @client_id;

-- name: DeleteExpiredAuthorizationRequests :exec
DELETE FROM oauth_authorization_requests WHERE user_id = @user_id AND expires_at < now();

-- name: CreateAuthorizationRequest :one
INSERT INTO oauth_authorization_requests (
    id, token_hash, user_id, client_id, redirect_uri, state, code_challenge, resource, scopes, expires_at
) VALUES (
    @id, @token_hash, @user_id, @client_id, @redirect_uri, @state, @code_challenge, @resource, @scopes, @expires_at
)
RETURNING *;

-- name: GetAuthorizationRequest :one
SELECT r.id, r.client_id, r.redirect_uri, r.expires_at,
       c.client_id AS client_key, c.client_name, c.registration, c.redirect_uris
FROM oauth_authorization_requests r
JOIN oauth_clients c ON c.id = r.client_id
WHERE r.token_hash = @token_hash AND r.user_id = @user_id AND r.expires_at > now();

-- name: TakeAuthorizationRequest :one
DELETE FROM oauth_authorization_requests
WHERE token_hash = @token_hash AND user_id = @user_id AND expires_at > now()
RETURNING *;

-- name: GrantExists :one
SELECT EXISTS (SELECT 1 FROM oauth_grants WHERE user_id = @user_id AND client_id = @client_id);

-- name: UpsertGrant :one
INSERT INTO oauth_grants (id, user_id, client_id, scopes)
VALUES (@id, @user_id, @client_id, @scopes)
ON CONFLICT (user_id, client_id) DO UPDATE SET scopes = EXCLUDED.scopes, updated_at = now()
RETURNING *;

-- name: CreateAuthorizationCode :one
INSERT INTO oauth_authorization_codes (id, token_hash, grant_id, redirect_uri, code_challenge, code_challenge_method, resource, scopes, expires_at)
VALUES (@id, @token_hash, @grant_id, @redirect_uri, @code_challenge, 'S256', @resource, @scopes, @expires_at)
RETURNING *;

-- name: ConsumeAuthorizationCode :one
UPDATE oauth_authorization_codes SET consumed_at = now()
WHERE token_hash = @token_hash AND consumed_at IS NULL AND expires_at > now()
RETURNING *;

-- GetAuthorizationCodeByHash detects reuse after a failed consume.
-- name: GetAuthorizationCodeByHash :one
SELECT * FROM oauth_authorization_codes WHERE token_hash = @token_hash;

-- name: GetGrantWithClient :one
SELECT g.id, g.user_id, g.client_id, g.scopes,
       c.client_id AS client_key, c.client_name, c.registration, c.metadata
FROM oauth_grants g
JOIN oauth_clients c ON c.id = g.client_id
WHERE g.id = @id;

-- name: CreateAccessToken :one
INSERT INTO oauth_access_tokens (id, token_hash, grant_id, family_id, resource, scopes, expires_at)
VALUES (@id, @token_hash, @grant_id, @family_id, @resource, @scopes, @expires_at)
RETURNING *;

-- name: CreateRefreshToken :one
INSERT INTO oauth_refresh_tokens (id, token_hash, grant_id, family_id, parent_id, resource, scopes, expires_at)
VALUES (@id, @token_hash, @grant_id, @family_id, @parent_id, @resource, @scopes, @expires_at)
RETURNING *;

-- name: GetRefreshTokenForUpdate :one
SELECT * FROM oauth_refresh_tokens WHERE token_hash = @token_hash FOR UPDATE;

-- name: MarkRefreshTokenRotated :exec
UPDATE oauth_refresh_tokens SET rotated_at = now() WHERE id = @id;

-- name: RevokeAccessTokenFamily :exec
UPDATE oauth_access_tokens SET revoked_at = now() WHERE family_id = @family_id AND revoked_at IS NULL;

-- name: RevokeRefreshTokenFamily :exec
UPDATE oauth_refresh_tokens SET revoked_at = now() WHERE family_id = @family_id AND revoked_at IS NULL;

-- RevokeAccessTokenByHash revokes one access token of one client (RFC 7009).
-- name: RevokeAccessTokenByHash :execrows
UPDATE oauth_access_tokens t SET revoked_at = now()
FROM oauth_grants g
WHERE t.grant_id = g.id AND g.client_id = @client_id AND t.token_hash = @token_hash AND t.revoked_at IS NULL;

-- name: GetRefreshTokenByHashForClient :one
SELECT t.* FROM oauth_refresh_tokens t
JOIN oauth_grants g ON g.id = t.grant_id
WHERE t.token_hash = @token_hash AND g.client_id = @client_id;

-- GetAccessTokenPrincipal resolves a bearer token for /mcp.
-- name: GetAccessTokenPrincipal :one
SELECT t.id, t.grant_id, t.family_id, t.scopes, t.expires_at,
       g.user_id, c.client_id AS client_key, c.client_name,
       e.email, coalesce(e.verified, false)::boolean AS email_verified
FROM oauth_access_tokens t
JOIN oauth_grants g ON g.id = t.grant_id
JOIN oauth_clients c ON c.id = g.client_id
LEFT JOIN emails e ON e.user_id = g.user_id AND e.is_primary
WHERE t.token_hash = @token_hash AND t.resource = @resource
  AND t.revoked_at IS NULL AND t.expires_at > now();

-- name: TouchGrant :exec
UPDATE oauth_grants SET last_used_at = now()
WHERE id = @id AND (last_used_at IS NULL OR last_used_at < now() - interval '5 minutes');

-- name: ListGrantsForUser :many
SELECT g.id, g.created_at, g.last_used_at,
       c.client_id AS client_key, c.client_name, c.registration, c.redirect_uris
FROM oauth_grants g
JOIN oauth_clients c ON c.id = g.client_id
WHERE g.user_id = @user_id
ORDER BY g.last_used_at DESC NULLS LAST, g.created_at DESC;

-- name: DeleteGrant :execrows
DELETE FROM oauth_grants WHERE id = @id AND user_id = @user_id;
