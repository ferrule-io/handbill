-- Background maintenance (internal/maintenance, docs/design.md#operations).

-- +goose Up

-- Blob GC deletes a blob only when no version or open deployment uses it
-- and it was last staged more than BLOB_GC_GRACE ago. Staging an existing
-- blob refreshes staged_at (sites.putBlob), so a publish that reuses an old
-- unreferenced blob is never raced by GC.
ALTER TABLE site_blobs RENAME COLUMN created_at TO staged_at;
CREATE INDEX site_blobs_staged_at ON site_blobs (staged_at);

-- Storage prefixes still to delete: a site's sites/<id>/ prefix is queued
-- in the transaction that deletes the site and removed once its objects
-- are gone, so a failed storage cleanup is retried by maintenance.
CREATE TABLE storage_deletions (
    prefix     text PRIMARY KEY CHECK (prefix <> '' AND right(prefix, 1) = '/'),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Maintenance looks blobs up by hash within a site.
CREATE INDEX version_files_blob ON version_files (blob_sha256);
CREATE INDEX deployment_files_blob ON deployment_files (blob_sha256) WHERE NOT deleted;
CREATE INDEX deployments_abandoned ON deployments (updated_at) WHERE state = 'abandoned';
CREATE INDEX sessions_expires_at ON sessions (expires_at);
CREATE INDEX site_tickets_expires_at ON site_tickets (expires_at);
CREATE INDEX email_verifications_expires_at ON email_verifications (expires_at);
CREATE INDEX oauth_authorization_codes_expires_at ON oauth_authorization_codes (expires_at);
CREATE INDEX oauth_access_tokens_expires_at ON oauth_access_tokens (expires_at);
CREATE INDEX oauth_refresh_tokens_expires_at ON oauth_refresh_tokens (expires_at);
CREATE INDEX invites_expires_at ON invites (expires_at) WHERE consumed_at IS NULL;
CREATE INDEX ownership_transfers_expires_at ON ownership_transfers (expires_at) WHERE consumed_at IS NULL;

-- +goose Down
DROP INDEX ownership_transfers_expires_at;
DROP INDEX invites_expires_at;
DROP INDEX oauth_refresh_tokens_expires_at;
DROP INDEX oauth_access_tokens_expires_at;
DROP INDEX oauth_authorization_codes_expires_at;
DROP INDEX email_verifications_expires_at;
DROP INDEX site_tickets_expires_at;
DROP INDEX sessions_expires_at;
DROP INDEX deployments_abandoned;
DROP INDEX deployment_files_blob;
DROP INDEX version_files_blob;
DROP TABLE storage_deletions;
DROP INDEX site_blobs_staged_at;
ALTER TABLE site_blobs RENAME COLUMN staged_at TO created_at;
