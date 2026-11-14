-- Background maintenance (internal/maintenance). @before is a cutoff the
-- caller computes as now minus a grace or retention period, so a request
-- racing its row's expiry still finds the row.

-- name: DeleteStaleSessions :execrows
DELETE FROM sessions WHERE expires_at < @before::timestamptz;

-- name: DeleteStaleSiteTickets :execrows
DELETE FROM site_tickets WHERE expires_at < @before::timestamptz;

-- name: DeleteStaleAuthFlows :execrows
DELETE FROM auth_flows WHERE expires_at < @before::timestamptz;

-- name: DeleteStaleEmailVerifications :execrows
DELETE FROM email_verifications WHERE expires_at < @before::timestamptz;

-- name: DeleteStaleAuthorizationRequests :execrows
DELETE FROM oauth_authorization_requests WHERE expires_at < @before::timestamptz;

-- Consumed codes are kept until they expire so a replayed code is still
-- recognized as a replay (and revokes its tokens) while it could be valid.
-- name: DeleteStaleAuthorizationCodes :execrows
DELETE FROM oauth_authorization_codes WHERE expires_at < @before::timestamptz;

-- name: DeleteStaleAccessTokens :execrows
DELETE FROM oauth_access_tokens WHERE expires_at < @before::timestamptz;

-- Rotated refresh tokens are kept until they expire for reuse detection.
-- name: DeleteStaleRefreshTokens :execrows
DELETE FROM oauth_refresh_tokens WHERE expires_at < @before::timestamptz;

-- Clients nobody approved: DCR registrations and cached CIMD documents
-- without grants or pending authorization requests. A CIMD client is
-- fetched again on its next use.
-- name: DeleteUnusedOAuthClients :execrows
DELETE FROM oauth_clients c
WHERE c.created_at < @before::timestamptz
  AND (c.registration = 'dcr' OR c.cache_expires_at < @before::timestamptz)
  AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM oauth_authorization_requests r WHERE r.client_id = c.id);

-- Pending and revoked invites and transfers past their expiry. Accepted
-- ones are kept as the record of how a member got their role (they go
-- with the site). A retention period keeps expired links answering
-- "expired" rather than "not found" for a while.
-- name: DeleteStaleInvites :execrows
DELETE FROM invites WHERE consumed_at IS NULL AND expires_at < @before::timestamptz;

-- name: DeleteStaleOwnershipTransfers :execrows
DELETE FROM ownership_transfers WHERE consumed_at IS NULL AND expires_at < @before::timestamptz;

-- name: AbandonStaleDeployments :execrows
UPDATE deployments SET state = 'abandoned', updated_at = now()
WHERE state = 'open' AND created_at < @before::timestamptz;

-- Abandoned deployments are of no further use; deleting them (and their
-- files) leaves their blobs to blob GC.
-- name: DeleteAbandonedDeployments :execrows
DELETE FROM deployments WHERE state = 'abandoned' AND updated_at < @before::timestamptz;

-- ListGCBlobs returns up to @max_rows blobs no version or open deployment
-- of their site uses, last staged before the grace period.
-- name: ListGCBlobs :many
SELECT b.site_id, b.sha256 FROM site_blobs b
WHERE b.staged_at < @before::timestamptz
  AND NOT EXISTS (
    SELECT 1 FROM version_files vf JOIN versions v ON v.id = vf.version_id
    WHERE v.site_id = b.site_id AND vf.blob_sha256 = b.sha256)
  AND NOT EXISTS (
    SELECT 1 FROM deployment_files df JOIN deployments d ON d.id = df.deployment_id
    WHERE d.site_id = b.site_id AND d.state = 'open' AND NOT df.deleted AND df.blob_sha256 = b.sha256)
ORDER BY b.staged_at
LIMIT @max_rows;

-- DeleteGCBlob deletes one blob row if it is still unused and stale. The
-- row stays locked until the caller's transaction commits, after the
-- storage object is deleted: a concurrent upload that stages the blob
-- waits for the commit and then stores it again.
-- name: DeleteGCBlob :execrows
DELETE FROM site_blobs b
WHERE b.site_id = @site_id AND b.sha256 = @sha256
  AND b.staged_at < @before::timestamptz
  AND NOT EXISTS (
    SELECT 1 FROM version_files vf JOIN versions v ON v.id = vf.version_id
    WHERE v.site_id = b.site_id AND vf.blob_sha256 = b.sha256)
  AND NOT EXISTS (
    SELECT 1 FROM deployment_files df JOIN deployments d ON d.id = df.deployment_id
    WHERE d.site_id = b.site_id AND d.state = 'open' AND NOT df.deleted AND df.blob_sha256 = b.sha256);

-- name: QueueStorageDeletion :exec
INSERT INTO storage_deletions (prefix) VALUES (@prefix) ON CONFLICT (prefix) DO NOTHING;

-- name: ListStorageDeletions :many
SELECT prefix FROM storage_deletions ORDER BY created_at LIMIT @max_rows;

-- name: DeleteStorageDeletion :exec
DELETE FROM storage_deletions WHERE prefix = @prefix;
