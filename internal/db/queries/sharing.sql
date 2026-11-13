-- Sharing (internal/sharing): memberships, invites, ownership transfers, and
-- public links. Roles are checked by the caller; these queries only keep
-- the invariants the schema cannot (never downgrading, never touching the
-- owner row outside a transfer).

-- Members ---------------------------------------------------------------

-- ListMembers returns a site's members with their primary email (NULL for
-- an account without one): the owner, then managers, then viewers.
-- name: ListMembers :many
SELECT m.user_id, m.role, m.created_at, e.email
FROM memberships m
LEFT JOIN emails e ON e.user_id = m.user_id AND e.is_primary
WHERE m.site_id = @site_id
ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'manager' THEN 1 ELSE 2 END, e.email NULLS LAST, m.created_at;

-- GrantMembership gives user_id role on a site without ever lowering an
-- existing role: a viewer may become a manager; managers and the owner are
-- left as they are. role must be 'manager' or 'viewer'.
-- name: GrantMembership :exec
INSERT INTO memberships (site_id, user_id, role) VALUES (@site_id, @user_id, @role)
ON CONFLICT (site_id, user_id) DO UPDATE SET role = EXCLUDED.role, updated_at = now()
WHERE memberships.role = 'viewer' AND EXCLUDED.role = 'manager';

-- SetMemberRole changes a non-owner member's role; no row when the user is
-- not a member or is the owner.
-- name: SetMemberRole :execrows
UPDATE memberships SET role = @role, updated_at = now()
WHERE site_id = @site_id AND user_id = @user_id AND role <> 'owner';

-- DeleteMembership removes a non-owner member.
-- name: DeleteMembership :execrows
DELETE FROM memberships WHERE site_id = @site_id AND user_id = @user_id AND role <> 'owner';

-- Invites ---------------------------------------------------------------

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

-- MarkInviteConsumed records an invite as accepted by user_id at creation,
-- when the address's verified holder got the grant automatically.
-- name: MarkInviteConsumed :exec
UPDATE invites SET consumed_at = now(), consumed_by = @user_id WHERE id = @id;

-- RevokePendingInvitesForEmail retires an address's earlier pending invites
-- to a site when it is invited again.
-- name: RevokePendingInvitesForEmail :execrows
UPDATE invites SET revoked_at = now()
WHERE site_id = @site_id AND email = @email AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: GetPendingInvite :one
SELECT * FROM invites
WHERE id = @id AND site_id = @site_id AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > now();

-- name: RevokeInvite :execrows
UPDATE invites SET revoked_at = now()
WHERE id = @id AND site_id = @site_id AND consumed_at IS NULL AND revoked_at IS NULL;

-- ListPendingInvites returns a site's unexpired pending invites, newest
-- first, with the inviter's primary email.
-- name: ListPendingInvites :many
SELECT i.id, i.email, i.role, i.created_at, i.expires_at, e.email AS invited_by_email
FROM invites i
LEFT JOIN emails e ON e.user_id = i.invited_by AND e.is_primary
WHERE i.site_id = @site_id AND i.consumed_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now()
ORDER BY i.created_at DESC, i.id DESC;

-- GetInviteByToken returns an invite in any state, with what the invite
-- page shows its holder.
-- name: GetInviteByToken :one
SELECT i.id, i.site_id, i.email, i.role, i.expires_at, i.consumed_at, i.consumed_by, i.revoked_at,
       s.title AS site_title, e.email AS invited_by_email
FROM invites i
JOIN sites s ON s.id = i.site_id
LEFT JOIN emails e ON e.user_id = i.invited_by AND e.is_primary
WHERE i.token_hash = @token_hash;

-- Ownership transfers -----------------------------------------------------

-- name: CreateTransfer :one
INSERT INTO ownership_transfers (id, token_hash, site_id, email, from_user_id, expires_at)
VALUES (@id, @token_hash, @site_id, @email, @from_user_id, @expires_at)
RETURNING *;

-- RevokePendingTransfers cancels a site's pending transfers (a site has at
-- most one: starting a new one, canceling, or completing one retires the
-- rest).
-- name: RevokePendingTransfers :execrows
UPDATE ownership_transfers SET revoked_at = now()
WHERE site_id = @site_id AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: GetPendingTransfer :one
SELECT * FROM ownership_transfers
WHERE site_id = @site_id AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > now()
ORDER BY created_at DESC
LIMIT 1;

-- ConsumeTransfer accepts a pending transfer for user_id; no row when it is
-- unknown, already accepted, canceled, or expired.
-- name: ConsumeTransfer :one
UPDATE ownership_transfers SET consumed_at = now(), consumed_by = @user_id
WHERE token_hash = @token_hash AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > now()
RETURNING id, site_id, email, from_user_id;

-- name: GetTransferByToken :one
SELECT t.id, t.site_id, t.email, t.expires_at, t.consumed_at, t.consumed_by, t.revoked_at,
       s.title AS site_title, e.email AS from_email
FROM ownership_transfers t
JOIN sites s ON s.id = t.site_id
LEFT JOIN emails e ON e.user_id = t.from_user_id AND e.is_primary
WHERE t.token_hash = @token_hash;

-- The three statements of a completed transfer, in this order inside one
-- transaction: memberships_one_owner is checked per statement, so the old
-- owner is demoted before the new one is promoted; sites_owner_membership
-- is deferred to commit.
-- name: DemoteOwner :execrows
UPDATE memberships SET role = 'manager', updated_at = now()
WHERE site_id = @site_id AND user_id = @user_id AND role = 'owner';

-- name: PromoteOwner :exec
INSERT INTO memberships (site_id, user_id, role) VALUES (@site_id, @user_id, 'owner')
ON CONFLICT (site_id, user_id) DO UPDATE SET role = 'owner', updated_at = now();

-- name: SetSiteOwner :execrows
UPDATE sites SET owner_id = @owner_id, updated_at = now() WHERE id = @id;

-- Public links ------------------------------------------------------------

-- name: GetPublicLink :one
SELECT * FROM public_links WHERE site_id = @site_id;

-- EnablePublicLink creates the link, or turns a disabled one back on with a
-- new token and the next generation; no row when it is already enabled.
-- name: EnablePublicLink :one
INSERT INTO public_links (site_id, token_hash) VALUES (@site_id, @token_hash)
ON CONFLICT (site_id) DO UPDATE
SET token_hash = EXCLUDED.token_hash, enabled = true, generation = public_links.generation + 1, updated_at = now()
WHERE NOT public_links.enabled
RETURNING *;

-- RotatePublicLink replaces an enabled link's token and bumps its
-- generation; no row when there is no enabled link.
-- name: RotatePublicLink :one
UPDATE public_links
SET token_hash = @token_hash, generation = generation + 1, updated_at = now()
WHERE site_id = @site_id AND enabled
RETURNING *;

-- name: DisablePublicLink :execrows
UPDATE public_links SET enabled = false, updated_at = now() WHERE site_id = @site_id AND enabled;

-- name: GetPublicLinkByToken :one
SELECT site_id, enabled, generation FROM public_links WHERE token_hash = @token_hash;
