-- The step-2 schema (docs/roadmap.md#2-data-model-and-storage). Conventions:
--
-- * Row ids are uuid, generated in Go (uuid.NewV7); the schema targets
--   PostgreSQL 17, so there is no uuidv7() default.
-- * Every lookup token is stored only as its SHA-256 in a unique bytea
--   token_hash column (internal/token). Single-use tokens are consumed by one
--   UPDATE ... SET consumed_at = now() WHERE token_hash = $1 AND consumed_at
--   IS NULL [AND revoked_at IS NULL] AND expires_at > now() RETURNING ...
-- * Email addresses are stored lowercased (CHECK email = lower(email)); callers
--   normalize before writing or comparing.
-- * Deleting a site deletes everything that belongs to it (versions, files,
--   deployments, memberships, invites, transfers, public link, tickets, blob
--   records). Storage objects under sites/<id>/ are the caller's to delete.
-- * Deleting a user deletes their credentials, sessions, tokens, OAuth grants,
--   and memberships, and keeps history on other people's sites (publisher and
--   inviter references become NULL). A user who still owns a site cannot be
--   deleted (sites.owner_id ON DELETE RESTRICT): delete or transfer the sites
--   first, so their storage is cleaned up through site deletion.

-- +goose Up

-- Accounts --------------------------------------------------------------

CREATE TABLE users (
    id         uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Email claims. Any number of accounts may claim an address unverified; at
-- most one account holds it verified. Verifying an address deletes other
-- accounts' unverified claims to it (docs/design.md#accounts).
CREATE TABLE emails (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email       text NOT NULL CHECK (email <> '' AND email = lower(email)),
    verified    boolean NOT NULL DEFAULT false,
    is_primary  boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    verified_at timestamptz,
    CHECK (verified = (verified_at IS NOT NULL)),
    UNIQUE (user_id, email)
);
CREATE UNIQUE INDEX emails_verified_email ON emails (email) WHERE verified;
CREATE UNIQUE INDEX emails_one_primary ON emails (user_id) WHERE is_primary;
CREATE INDEX emails_email ON emails (email);

CREATE TABLE email_verifications (
    id          uuid PRIMARY KEY,
    token_hash  bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    email_id    uuid NOT NULL REFERENCES emails (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz
);
CREATE INDEX email_verifications_email_id ON email_verifications (email_id);

-- WebAuthn credentials (github.com/go-webauthn/webauthn Credential). The
-- WebAuthn user handle is the user id's 16 bytes.
CREATE TABLE passkey_credentials (
    id               uuid PRIMARY KEY,
    user_id          uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id    bytea NOT NULL UNIQUE,
    public_key       bytea NOT NULL,
    attestation_type text NOT NULL DEFAULT '',
    aaguid           bytea,
    sign_count       bigint NOT NULL DEFAULT 0 CHECK (sign_count >= 0),
    transports       text[] NOT NULL DEFAULT '{}',
    attachment       text NOT NULL DEFAULT '',
    user_verified    boolean NOT NULL DEFAULT false,
    backup_eligible  boolean NOT NULL DEFAULT false,
    backup_state     boolean NOT NULL DEFAULT false,
    clone_warning    boolean NOT NULL DEFAULT false,
    name             text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    last_used_at     timestamptz
);
CREATE INDEX passkey_credentials_user_id ON passkey_credentials (user_id);

CREATE TABLE google_identities (
    id             uuid PRIMARY KEY,
    user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    sub            text NOT NULL UNIQUE,
    email          text NOT NULL DEFAULT '' CHECK (email = lower(email)),
    email_verified boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_used_at   timestamptz
);
CREATE INDEX google_identities_user_id ON google_identities (user_id);

CREATE TABLE sessions (
    id           uuid PRIMARY KEY,
    token_hash   bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_agent   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user_id ON sessions (user_id);

-- Recovery codes are low-volume and compared per account, so they are
-- argon2id PHC strings (token.HashRecoveryCode), not lookup hashes.
CREATE TABLE recovery_codes (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash   text NOT NULL CHECK (code_hash LIKE '$argon2id$%'),
    created_at  timestamptz NOT NULL DEFAULT now(),
    consumed_at timestamptz
);
CREATE INDEX recovery_codes_user_id ON recovery_codes (user_id);

-- Sites -----------------------------------------------------------------

-- Exactly one owner per site: memberships allows at most one 'owner' row per
-- site (memberships_one_owner), and sites.(id, owner_id, owner_role) must
-- match an owner membership (sites_owner_membership, checked at commit), so a
-- committed site always has exactly one, and sites.owner_id names it.
CREATE TABLE sites (
    id                 text PRIMARY KEY CHECK (id ~ '^[a-z2-7]{12}$'),
    title              text NOT NULL,
    description        text NOT NULL DEFAULT '',
    owner_id           uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    owner_role         text NOT NULL DEFAULT 'owner' CHECK (owner_role = 'owner'),
    current_version_id uuid,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sites_owner_id ON sites (owner_id);

CREATE TABLE memberships (
    site_id    text NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('owner', 'manager', 'viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, user_id),
    UNIQUE (site_id, user_id, role)
);
CREATE UNIQUE INDEX memberships_one_owner ON memberships (site_id) WHERE role = 'owner';
CREATE INDEX memberships_user_id ON memberships (user_id);

ALTER TABLE sites ADD CONSTRAINT sites_owner_membership
    FOREIGN KEY (id, owner_id, owner_role) REFERENCES memberships (site_id, user_id, role)
    DEFERRABLE INITIALLY DEFERRED;

-- Versions are immutable once written (triggers below); a publish inserts a
-- version and its files, a rollback only repoints sites.current_version_id.
-- The one permitted change is published_by becoming NULL when that user is
-- deleted.
CREATE TABLE versions (
    id           uuid PRIMARY KEY,
    site_id      text NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    published_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (site_id, id)
);
CREATE INDEX versions_site_id_created_at ON versions (site_id, created_at DESC);
CREATE INDEX versions_published_by ON versions (published_by);

-- The current version must belong to the site.
ALTER TABLE sites ADD CONSTRAINT sites_current_version
    FOREIGN KEY (id, current_version_id) REFERENCES versions (site_id, id)
    DEFERRABLE INITIALLY DEFERRED;

-- Files reference content-addressed blobs stored at
-- sites/<site-id>/blobs/<hex(blob_sha256)>.
CREATE TABLE version_files (
    version_id   uuid NOT NULL REFERENCES versions (id) ON DELETE CASCADE,
    path         text NOT NULL CHECK (path <> '' AND left(path, 1) <> '/'),
    blob_sha256  bytea NOT NULL CHECK (octet_length(blob_sha256) = 32),
    size         bigint NOT NULL CHECK (size >= 0),
    content_type text NOT NULL,
    PRIMARY KEY (version_id, path)
);

-- +goose StatementBegin
CREATE FUNCTION reject_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER versions_immutable BEFORE UPDATE ON versions
    FOR EACH ROW
    WHEN (NEW.id IS DISTINCT FROM OLD.id
          OR NEW.site_id IS DISTINCT FROM OLD.site_id
          OR NEW.created_at IS DISTINCT FROM OLD.created_at
          OR (NEW.published_by IS NOT NULL AND NEW.published_by IS DISTINCT FROM OLD.published_by))
    EXECUTE FUNCTION reject_update();
CREATE TRIGGER version_files_immutable BEFORE UPDATE ON version_files
    FOR EACH ROW EXECUTE FUNCTION reject_update();

-- Blobs uploaded for a site. The storage interface cannot list objects, so
-- this is how blob GC (W9) finds blobs no version or open deployment uses.
CREATE TABLE site_blobs (
    site_id    text NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    sha256     bytea NOT NULL CHECK (octet_length(sha256) = 32),
    size       bigint NOT NULL CHECK (size >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, sha256)
);

-- A staged publish. Finalizing turns its files (merged onto the current
-- version in merge mode, minus deleted paths) into a new version.
CREATE TABLE deployments (
    id           uuid PRIMARY KEY,
    site_id      text NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    created_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    mode         text NOT NULL CHECK (mode IN ('replace', 'merge')),
    state        text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'finalized', 'abandoned')),
    version_id   uuid REFERENCES versions (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    finalized_at timestamptz,
    CHECK ((state = 'finalized') = (finalized_at IS NOT NULL)),
    CHECK (version_id IS NULL OR state = 'finalized')
);
CREATE INDEX deployments_site_id ON deployments (site_id, created_at DESC);
CREATE INDEX deployments_open ON deployments (created_at) WHERE state = 'open';
CREATE INDEX deployments_created_by ON deployments (created_by);

-- A row is either a file (deleted = false) or, in merge mode, the deletion
-- of a path from the current version (deleted = true, no blob).
CREATE TABLE deployment_files (
    deployment_id uuid NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    path          text NOT NULL CHECK (path <> '' AND left(path, 1) <> '/'),
    deleted       boolean NOT NULL DEFAULT false,
    blob_sha256   bytea CHECK (octet_length(blob_sha256) = 32),
    size          bigint CHECK (size >= 0),
    content_type  text,
    PRIMARY KEY (deployment_id, path),
    CHECK (
        (deleted AND blob_sha256 IS NULL AND size IS NULL AND content_type IS NULL)
        OR (NOT deleted AND blob_sha256 IS NOT NULL AND size IS NOT NULL AND content_type IS NOT NULL)
    )
);

-- Sharing ---------------------------------------------------------------

CREATE TABLE invites (
    id          uuid PRIMARY KEY,
    token_hash  bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    site_id     text NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    email       text NOT NULL CHECK (email <> '' AND email = lower(email)),
    role        text NOT NULL CHECK (role IN ('manager', 'viewer')),
    invited_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    consumed_by uuid REFERENCES users (id) ON DELETE SET NULL,
    revoked_at  timestamptz,
    CHECK (consumed_at IS NULL OR revoked_at IS NULL)
);
CREATE INDEX invites_site_id ON invites (site_id);
CREATE INDEX invites_pending_email ON invites (email) WHERE consumed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX invites_invited_by ON invites (invited_by);
CREATE INDEX invites_consumed_by ON invites (consumed_by);

-- Ownership transfers: same binding and expiry rules as invites; accepting
-- makes the recipient owner and the previous owner a manager.
CREATE TABLE ownership_transfers (
    id           uuid PRIMARY KEY,
    token_hash   bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    site_id      text NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    email        text NOT NULL CHECK (email <> '' AND email = lower(email)),
    from_user_id uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    consumed_at  timestamptz,
    consumed_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    revoked_at   timestamptz,
    CHECK (consumed_at IS NULL OR revoked_at IS NULL)
);
CREATE INDEX ownership_transfers_site_id ON ownership_transfers (site_id);
CREATE INDEX ownership_transfers_pending_email ON ownership_transfers (email) WHERE consumed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX ownership_transfers_from_user_id ON ownership_transfers (from_user_id);
CREATE INDEX ownership_transfers_consumed_by ON ownership_transfers (consumed_by);

-- One public link per site, created on first enable. Rotating replaces the
-- token and bumps generation; site cookies carry the generation they were
-- issued under. Generations start at 1 so a member cookie (LinkGen 0) never
-- matches.
CREATE TABLE public_links (
    site_id    text PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    enabled    boolean NOT NULL DEFAULT true,
    generation bigint NOT NULL DEFAULT 1 CHECK (generation >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Site sign-in handoff tickets (docs/design.md#site-sign-in-handoff).
CREATE TABLE site_tickets (
    id          uuid PRIMARY KEY,
    token_hash  bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    site_id     text NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    return_path text NOT NULL DEFAULT '/' CHECK (left(return_path, 1) = '/'),
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz
);
CREATE INDEX site_tickets_site_id ON site_tickets (site_id);
CREATE INDEX site_tickets_user_id ON site_tickets (user_id);

-- OAuth 2.1 -------------------------------------------------------------

-- Clients: CIMD clients (client_id is the metadata document URL, cached per
-- its HTTP cache headers) and, with OAUTH_DCR_ENABLED, DCR-registered clients
-- (client_id generated; shown as unverified on consent).
CREATE TABLE oauth_clients (
    id                         uuid PRIMARY KEY,
    client_id                  text NOT NULL UNIQUE,
    registration               text NOT NULL CHECK (registration IN ('cimd', 'dcr')),
    metadata                   jsonb NOT NULL,
    client_name                text NOT NULL DEFAULT '',
    redirect_uris              text[] NOT NULL,
    token_endpoint_auth_method text NOT NULL DEFAULT 'none',
    client_secret_hash         bytea UNIQUE CHECK (octet_length(client_secret_hash) = 32),
    fetched_at                 timestamptz,
    cache_expires_at           timestamptz,
    etag                       text,
    last_modified              text,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    CHECK (registration <> 'cimd' OR fetched_at IS NOT NULL),
    CHECK (registration = 'cimd' OR (fetched_at IS NULL AND cache_expires_at IS NULL AND etag IS NULL AND last_modified IS NULL)),
    CHECK (client_secret_hash IS NULL OR registration = 'dcr')
);

-- A user's consent to a client, reviewed and revoked in /settings/security.
-- Revoking deletes the grant, and with it every code and token issued
-- under it.
CREATE TABLE oauth_grants (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id    uuid NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    scopes       text[] NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    UNIQUE (user_id, client_id)
);
CREATE INDEX oauth_grants_client_id ON oauth_grants (client_id);

CREATE TABLE oauth_authorization_codes (
    id                    uuid PRIMARY KEY,
    token_hash            bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    grant_id              uuid NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    redirect_uri          text NOT NULL,
    code_challenge        text NOT NULL,
    code_challenge_method text NOT NULL DEFAULT 'S256' CHECK (code_challenge_method = 'S256'),
    resource              text NOT NULL,
    scopes                text[] NOT NULL DEFAULT '{}',
    created_at            timestamptz NOT NULL DEFAULT now(),
    expires_at            timestamptz NOT NULL,
    consumed_at           timestamptz
);
CREATE INDEX oauth_authorization_codes_grant_id ON oauth_authorization_codes (grant_id);

-- Token families: every access and refresh token descending from one
-- authorization code shares family_id (the code's id). Presenting a consumed
-- code or a rotated refresh token revokes the whole family.
CREATE TABLE oauth_access_tokens (
    id         uuid PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    grant_id   uuid NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    family_id  uuid NOT NULL,
    resource   text NOT NULL,
    scopes     text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE INDEX oauth_access_tokens_grant_id ON oauth_access_tokens (grant_id);
CREATE INDEX oauth_access_tokens_family_id ON oauth_access_tokens (family_id);

-- Refresh tokens rotate: using one sets rotated_at and issues a child
-- (parent_id); each token has at most one child.
CREATE TABLE oauth_refresh_tokens (
    id         uuid PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    grant_id   uuid NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    family_id  uuid NOT NULL,
    parent_id  uuid UNIQUE REFERENCES oauth_refresh_tokens (id) ON DELETE SET NULL,
    resource   text NOT NULL,
    scopes     text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    rotated_at timestamptz,
    revoked_at timestamptz
);
CREATE INDEX oauth_refresh_tokens_grant_id ON oauth_refresh_tokens (grant_id);
CREATE INDEX oauth_refresh_tokens_family_id ON oauth_refresh_tokens (family_id);

-- +goose Down

DROP TABLE oauth_refresh_tokens;
DROP TABLE oauth_access_tokens;
DROP TABLE oauth_authorization_codes;
DROP TABLE oauth_grants;
DROP TABLE oauth_clients;
DROP TABLE site_tickets;
DROP TABLE public_links;
DROP TABLE ownership_transfers;
DROP TABLE invites;
DROP TABLE deployment_files;
DROP TABLE deployments;
DROP TABLE site_blobs;
DROP TABLE version_files;
ALTER TABLE sites DROP CONSTRAINT sites_current_version;
DROP TABLE versions;
DROP FUNCTION reject_update();
ALTER TABLE sites DROP CONSTRAINT sites_owner_membership;
DROP TABLE memberships;
DROP TABLE sites;
DROP TABLE recovery_codes;
DROP TABLE sessions;
DROP TABLE google_identities;
DROP TABLE passkey_credentials;
DROP TABLE email_verifications;
DROP TABLE emails;
DROP TABLE users;
