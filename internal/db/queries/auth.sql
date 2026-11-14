-- Sessions, passkeys, Google identities, recovery codes, and sign-in
-- ceremonies (internal/auth).

-- name: CreateSession :one
INSERT INTO sessions (id, token_hash, user_id, user_agent, expires_at, restricted)
VALUES (@id, @token_hash, @user_id, @user_agent, @expires_at, @restricted)
RETURNING *;

-- GetSession returns an unexpired session with the user's primary email
-- (absent for accounts without one).
-- name: GetSession :one
SELECT s.id, s.user_id, s.created_at, s.last_seen_at, s.expires_at, s.restricted,
       e.email AS email, e.verified AS email_verified
FROM sessions s
LEFT JOIN emails e ON e.user_id = s.user_id AND e.is_primary
WHERE s.token_hash = @token_hash AND s.expires_at > now();

-- name: RenewSession :exec
UPDATE sessions SET last_seen_at = now(), expires_at = @expires_at WHERE id = @id;

-- name: ListSessions :many
SELECT * FROM sessions
WHERE user_id = @user_id AND expires_at > now() AND NOT restricted
ORDER BY last_seen_at DESC, id;

-- name: DeleteSession :execrows
DELETE FROM sessions WHERE id = @id;

-- name: DeleteUserSessions :execrows
DELETE FROM sessions WHERE user_id = @user_id;

-- name: DeleteOtherSessions :execrows
DELETE FROM sessions WHERE user_id = @user_id AND id <> @keep;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= now();

-- name: CreatePasskey :one
INSERT INTO passkey_credentials (
    id, user_id, credential_id, public_key, attestation_type, aaguid, sign_count,
    transports, attachment, user_verified, backup_eligible, backup_state, name
) VALUES (
    @id, @user_id, @credential_id, @public_key, @attestation_type, @aaguid, @sign_count,
    @transports, @attachment, @user_verified, @backup_eligible, @backup_state, @name
)
RETURNING *;

-- name: ListPasskeys :many
SELECT * FROM passkey_credentials WHERE user_id = @user_id ORDER BY created_at, id;

-- name: GetPasskeyByCredentialID :one
SELECT * FROM passkey_credentials WHERE credential_id = @credential_id;

-- name: UpdatePasskeyUse :exec
UPDATE passkey_credentials
SET sign_count = @sign_count, backup_state = @backup_state, user_verified = @user_verified,
    clone_warning = @clone_warning, last_used_at = now()
WHERE id = @id;

-- name: SetPasskeyCloneWarning :exec
UPDATE passkey_credentials SET clone_warning = true WHERE id = @id;

-- name: RenamePasskey :one
UPDATE passkey_credentials SET name = @name
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeletePasskey :execrows
DELETE FROM passkey_credentials WHERE id = @id AND user_id = @user_id;

-- CountSignInMethods counts what can start a full session: passkeys and a
-- linked Google identity (recovery codes only lead to a restricted one).
-- name: CountSignInMethods :one
SELECT (SELECT count(*) FROM passkey_credentials p WHERE p.user_id = @user_id)
     + (SELECT count(*) FROM google_identities g WHERE g.user_id = @user_id);

-- name: CreateGoogleIdentity :one
INSERT INTO google_identities (id, user_id, sub, email, email_verified, last_used_at)
VALUES (@id, @user_id, @sub, @email, @email_verified, now())
RETURNING *;

-- name: GetGoogleIdentityBySub :one
SELECT * FROM google_identities WHERE sub = @sub;

-- name: GetGoogleIdentityForUser :one
SELECT * FROM google_identities WHERE user_id = @user_id;

-- name: UpdateGoogleIdentityUse :exec
UPDATE google_identities SET email = @email, email_verified = @email_verified, last_used_at = now()
WHERE id = @id;

-- name: DeleteGoogleIdentity :execrows
DELETE FROM google_identities WHERE user_id = @user_id;

-- name: CreateRecoveryCode :exec
INSERT INTO recovery_codes (id, user_id, code_hash, lookup_hash)
VALUES (@id, @user_id, @code_hash, @lookup_hash);

-- name: DeleteRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id = @user_id;

-- GetRecoveryCodeForSignIn returns the unused code with lookup_hash when
-- its account claims email (verified or not); no row otherwise.
-- name: GetRecoveryCodeForSignIn :one
SELECT r.id, r.user_id, r.code_hash FROM recovery_codes r
WHERE r.lookup_hash = @lookup_hash AND r.consumed_at IS NULL
  AND EXISTS (SELECT 1 FROM emails e WHERE e.user_id = r.user_id AND e.email = @email);

-- name: ConsumeRecoveryCode :execrows
UPDATE recovery_codes SET consumed_at = now() WHERE id = @id AND consumed_at IS NULL;

-- name: RecoveryCodeStatus :one
SELECT count(*) FILTER (WHERE consumed_at IS NULL) AS remaining,
       count(*) AS total,
       max(created_at) AS issued_at
FROM recovery_codes WHERE user_id = @user_id;

-- name: CreateAuthFlow :exec
INSERT INTO auth_flows (id, token_hash, kind, user_id, data, expires_at)
VALUES (@id, @token_hash, @kind, @user_id, @data, @expires_at);

-- ConsumeAuthFlow deletes and returns an unexpired flow of the given kind;
-- no row when it is unknown, used, or expired.
-- name: ConsumeAuthFlow :one
DELETE FROM auth_flows
WHERE token_hash = @token_hash AND kind = @kind AND expires_at > now()
RETURNING *;

-- name: DeleteExpiredAuthFlows :exec
DELETE FROM auth_flows WHERE expires_at <= now();
