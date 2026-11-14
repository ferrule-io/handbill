-- Recovery codes gain a keyed lookup hash (docs/design.md#accounts):
-- HMAC-SHA256 of the normalized code under a key derived from SECRET_KEY
-- (token.RecoveryCodeLookup), so a sign-in finds the one candidate code and
-- runs argon2id once instead of once per unused code of every account
-- claiming the address. Rotating SECRET_KEY invalidates every code.
--
-- Existing codes cannot be backfilled (their plaintext is never stored), so
-- they are deleted; nothing has been released with them. Accounts
-- regenerate codes from settings, whose security page shows none remaining.

-- +goose Up

DELETE FROM recovery_codes;
ALTER TABLE recovery_codes
    ADD COLUMN lookup_hash bytea NOT NULL UNIQUE CHECK (octet_length(lookup_hash) = 32);

-- +goose Down

ALTER TABLE recovery_codes DROP COLUMN lookup_hash;
