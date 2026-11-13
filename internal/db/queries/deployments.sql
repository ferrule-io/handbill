-- Staged publishes (internal/sites). A deployment collects files and, in
-- merge mode, deleted paths; Finalize turns it into an immutable version.

-- name: CreateDeployment :one
INSERT INTO deployments (id, site_id, created_by, mode)
VALUES (@id, @site_id, @created_by, @mode)
RETURNING *;

-- name: GetDeployment :one
SELECT * FROM deployments WHERE id = @id;

-- LockDeployment takes the row lock Finalize holds; AddFiles and DeletePaths
-- share-lock it, so files cannot be added while a deployment is finalizing.
-- name: LockDeployment :one
SELECT * FROM deployments WHERE id = @id FOR UPDATE;

-- name: ShareLockDeployment :one
SELECT * FROM deployments WHERE id = @id FOR SHARE;

-- AddDeploymentFiles upserts files; paths must be unique within one call.
-- name: AddDeploymentFiles :exec
INSERT INTO deployment_files (deployment_id, path, deleted, blob_sha256, size, content_type)
SELECT @deployment_id::uuid, unnest(@paths::text[]), false, unnest(@blobs::bytea[]),
       unnest(@sizes::bigint[]), unnest(@content_types::text[])
ON CONFLICT (deployment_id, path) DO UPDATE
SET deleted = false, blob_sha256 = excluded.blob_sha256, size = excluded.size, content_type = excluded.content_type;

-- AddDeploymentDeletions records deleted paths; paths must be unique.
-- name: AddDeploymentDeletions :exec
INSERT INTO deployment_files (deployment_id, path, deleted)
SELECT @deployment_id::uuid, unnest(@paths::text[]), true
ON CONFLICT (deployment_id, path) DO UPDATE
SET deleted = true, blob_sha256 = NULL, size = NULL, content_type = NULL;

-- name: CountDeploymentFiles :one
SELECT count(*)::int FROM deployment_files WHERE deployment_id = @deployment_id;

-- CreateVersionFilesFromDeployment fills a new version: the base version's
-- files not touched by the deployment (merge mode; base NULL for replace),
-- plus the deployment's files.
-- name: CreateVersionFilesFromDeployment :execrows
INSERT INTO version_files (version_id, path, blob_sha256, size, content_type)
SELECT @version_id::uuid, f.path, f.blob_sha256, f.size, f.content_type
FROM version_files f
WHERE f.version_id = sqlc.narg(base_version_id)::uuid
  AND NOT EXISTS (
    SELECT 1 FROM deployment_files d WHERE d.deployment_id = @deployment_id AND d.path = f.path
  )
UNION ALL
SELECT @version_id::uuid, d.path, d.blob_sha256, d.size, d.content_type
FROM deployment_files d
WHERE d.deployment_id = @deployment_id AND NOT d.deleted;

-- name: FinalizeDeployment :execrows
UPDATE deployments
SET state = 'finalized', version_id = @version_id, finalized_at = now(), updated_at = now()
WHERE id = @id AND state = 'open';

-- name: AbandonDeployment :exec
UPDATE deployments SET state = 'abandoned', updated_at = now()
WHERE id = @id AND state = 'open';
