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

-- CreateVersion stamps clock_timestamp(), not the transaction start: a
-- finalize waits for the site lock first, so creation times follow the
-- order versions became current.
-- name: CreateVersion :one
INSERT INTO versions (id, site_id, published_by, created_at)
VALUES (@id, @site_id, @published_by, clock_timestamp())
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

-- GetSiteForUpdate locks the site row; Finalize holds it so concurrent merge
-- publishes build on each other's versions instead of the same base.
-- name: GetSiteForUpdate :one
SELECT * FROM sites WHERE id = @id FOR UPDATE;

-- UpdateSite changes the fields that are not NULL.
-- name: UpdateSite :one
UPDATE sites
SET title = coalesce(sqlc.narg(title), title),
    description = coalesce(sqlc.narg(description), description),
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: ListSitesForUser :many
SELECT sqlc.embed(sites), m.role
FROM memberships m
JOIN sites ON sites.id = m.site_id
WHERE m.user_id = @user_id
ORDER BY sites.updated_at DESC, sites.id;

-- name: GetMemberRole :one
SELECT role FROM memberships WHERE site_id = @site_id AND user_id = @user_id;

-- GetSiteAccess is the per-request state the site file server checks a site
-- cookie against; role and public-link columns are NULL when absent.
-- name: GetSiteAccess :one
SELECT s.id, s.current_version_id, m.role, pl.enabled AS public_link_enabled, pl.generation AS public_link_generation
FROM sites s
LEFT JOIN memberships m ON m.site_id = s.id AND m.user_id = @user_id
LEFT JOIN public_links pl ON pl.site_id = s.id
WHERE s.id = @site_id;

-- name: ListVersions :many
SELECT v.id, v.site_id, v.published_by, v.created_at,
       count(f.path)::int AS file_count,
       coalesce(sum(f.size), 0)::bigint AS size,
       e.email AS publisher_email
FROM versions v
LEFT JOIN version_files f ON f.version_id = v.id
LEFT JOIN emails e ON e.user_id = v.published_by AND e.is_primary
WHERE v.site_id = @site_id
GROUP BY v.id, e.email
ORDER BY v.created_at DESC, v.id DESC;

-- name: GetVersion :one
SELECT v.id, v.site_id, v.published_by, v.created_at,
       count(f.path)::int AS file_count,
       coalesce(sum(f.size), 0)::bigint AS size,
       e.email AS publisher_email
FROM versions v
LEFT JOIN version_files f ON f.version_id = v.id
LEFT JOIN emails e ON e.user_id = v.published_by AND e.is_primary
WHERE v.id = @id AND v.site_id = @site_id
GROUP BY v.id, e.email;

-- RollbackSite repoints the site to one of its own versions; no row when
-- either does not exist.
-- name: RollbackSite :execrows
UPDATE sites SET current_version_id = @version_id::uuid, updated_at = now()
WHERE sites.id = @site_id::text
  AND EXISTS (SELECT 1 FROM versions v WHERE v.id = @version_id::uuid AND v.site_id = @site_id::text);

-- name: ListVersionFiles :many
SELECT path, blob_sha256, size, content_type FROM version_files
WHERE version_id = @version_id
ORDER BY path;

-- FindVersionFile returns the first of paths (in order) present in a version.
-- name: FindVersionFile :one
SELECT path, blob_sha256, size, content_type FROM version_files
WHERE version_id = @version_id AND path = ANY(@paths::text[])
ORDER BY array_position(@paths::text[], path)
LIMIT 1;

-- name: SiteBlobExists :one
SELECT EXISTS (SELECT 1 FROM site_blobs WHERE site_id = @site_id AND sha256 = @sha256);

-- name: CreateSiteBlob :exec
INSERT INTO site_blobs (site_id, sha256, size) VALUES (@site_id, @sha256, @size)
ON CONFLICT (site_id, sha256) DO NOTHING;
