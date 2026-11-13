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

-- name: GetEmail :one
SELECT * FROM emails WHERE id = @id;

-- name: GetUserEmail :one
SELECT * FROM emails WHERE user_id = @user_id AND email = @email;

-- name: ListUserEmails :many
SELECT * FROM emails WHERE user_id = @user_id ORDER BY is_primary DESC, created_at, id;

-- name: HasPrimaryEmail :one
SELECT EXISTS (SELECT 1 FROM emails WHERE user_id = @user_id AND is_primary);

-- ListEmailClaims returns the accounts claiming an address, the verified
-- holder (if any) first, then the oldest claims.
-- name: ListEmailClaims :many
SELECT user_id, verified FROM emails WHERE email = @email
ORDER BY verified DESC, created_at, id;

-- name: GetVerifiedEmailHolder :one
SELECT user_id FROM emails WHERE email = @email AND verified;

-- LockUser serializes changes to one account's sign-in methods (e.g. so two
-- concurrent removals cannot delete the last two).
-- name: LockUser :exec
SELECT id FROM users WHERE id = @id FOR UPDATE;
