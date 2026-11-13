-- CreateSite must run in the same transaction as the owner's membership
-- (CreateMembership with role 'owner'); sites_owner_membership is checked
-- at commit.
-- name: CreateSite :one
INSERT INTO sites (id, title, description, owner_id)
VALUES (@id, @title, @description, @owner_id)
RETURNING *;

-- name: GetSite :one
SELECT * FROM sites WHERE id = @id;

-- name: DeleteSite :execrows
DELETE FROM sites WHERE id = @id;

-- name: CreateMembership :exec
INSERT INTO memberships (site_id, user_id, role) VALUES (@site_id, @user_id, @role);

-- name: CreateVersion :one
INSERT INTO versions (id, site_id, published_by)
VALUES (@id, @site_id, @published_by)
RETURNING *;

-- name: CreateVersionFile :exec
INSERT INTO version_files (version_id, path, blob_sha256, size, content_type)
VALUES (@version_id, @path, @blob_sha256, @size, @content_type);

-- name: SetCurrentVersion :execrows
UPDATE sites SET current_version_id = @version_id, updated_at = now() WHERE id = @id;

-- name: CreateSiteTicket :one
INSERT INTO site_tickets (id, token_hash, site_id, user_id, return_path, expires_at)
VALUES (@id, @token_hash, @site_id, @user_id, @return_path, @expires_at)
RETURNING *;

-- ConsumeSiteTicket redeems a ticket; no row when it is unknown, already
-- used, or expired.
-- name: ConsumeSiteTicket :one
UPDATE site_tickets SET consumed_at = now()
WHERE token_hash = @token_hash AND consumed_at IS NULL AND expires_at > now()
RETURNING site_id, user_id, return_path;
