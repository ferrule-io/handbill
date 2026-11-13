-- name: CreateUser :one
INSERT INTO users (id) VALUES (@id) RETURNING *;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = @id;

-- name: CreateEmail :one
INSERT INTO emails (id, user_id, email, is_primary)
VALUES (@id, @user_id, @email, @is_primary)
RETURNING *;

-- DisplaceUnverifiedEmailClaims deletes other accounts' unverified claims to
-- an address that user_id is about to hold verified (docs/design.md#accounts).
-- name: DisplaceUnverifiedEmailClaims :execrows
DELETE FROM emails WHERE email = @email AND user_id <> @user_id AND NOT verified;

-- name: MarkEmailVerified :one
UPDATE emails SET verified = true, verified_at = now()
WHERE id = @id
RETURNING *;

-- name: CreateEmailVerification :one
INSERT INTO email_verifications (id, token_hash, email_id, expires_at)
VALUES (@id, @token_hash, @email_id, @expires_at)
RETURNING *;

-- ConsumeEmailVerification marks a verification token used; no row when it
-- is unknown, already used, or expired.
-- name: ConsumeEmailVerification :one
UPDATE email_verifications SET consumed_at = now()
WHERE token_hash = @token_hash AND consumed_at IS NULL AND expires_at > now()
RETURNING email_id;
