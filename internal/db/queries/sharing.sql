-- name: CreateInvite :one
INSERT INTO invites (id, token_hash, site_id, email, role, invited_by, expires_at)
VALUES (@id, @token_hash, @site_id, @email, @role, @invited_by, @expires_at)
RETURNING *;

-- ConsumeInvite accepts a pending invite for user_id; no row when it is
-- unknown, already accepted, revoked, or expired.
-- name: ConsumeInvite :one
UPDATE invites SET consumed_at = now(), consumed_by = @user_id
WHERE token_hash = @token_hash AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > now()
RETURNING id, site_id, email, role;
