# agent-dash — Design

Decisions here implement [INTENT.md](../INTENT.md).

## Origins and URLs

Hosted sites contain agent-authored HTML and JS, so they never share an origin
with the app or with each other (URL option B).

| Surface | URL | Notes |
|---|---|---|
| App (UI, auth, OAuth, MCP) | `https://dash.example.com` | `APP_ORIGIN` |
| Site content | `https://<site-id>.sites.example.com/<path>` | `SITES_DOMAIN`; one wildcard cert for `*.sites.example.com` |

- `<site-id>` is random: 12 characters of lowercase base32 (`a-z2-7`, ~60 bits),
  assigned at creation and never changed. A wildcard cert covers a single label,
  so the id is always exactly one label. Sites have a title for display; the
  URL carries no owner or slug.
- The id is not a secret. Access is always enforced by the session or a
  public-link token (below).
- `/_auth/*` is reserved on every site host and never served from site content.

### App routes

| Route | Purpose |
|---|---|
| `/` | Sites the user owns, manages, or can view |
| `/sites/<site-id>` | Settings, members, invites, public link, versions |
| `/invite/<token>` | Accept an invite (signs up or signs in first) |
| `/signup`, `/login` | Passkey or Google |
| `/auth/google/callback` | Google OIDC callback |
| `/auth/site` | Issues site tickets (handoff below) |
| `/settings/security` | Passkeys, linked Google identity, recovery codes, OAuth grants |
| `/mcp` | MCP endpoint (OAuth bearer tokens only) |
| `/.well-known/oauth-protected-resource` | Protected resource metadata for `/mcp` |
| `/.well-known/oauth-authorization-server` | AS metadata, `client_id_metadata_document_supported: true` |
| `/oauth/authorize`, `/oauth/token` | OAuth 2.1 |
| `/api/deployments/<id>/archive` | Archive upload target returned by `get_upload_url` |

### Site sign-in handoff

The app session cookie is host-only on `dash.example.com`; site hosts never
receive it.

1. A request to `https://<id>.sites.example.com/<path>` without a valid site
   cookie redirects to `https://dash.example.com/auth/site?site=<id>&return=<path>`.
2. The app requires a session (sign-in/sign-up if needed) and the viewer role,
   then redirects to `https://<id>.sites.example.com/_auth/callback?ticket=<t>`.
   The ticket is signed, bound to the site id and user, valid ~60 s, single use.
3. The site host redeems the ticket, sets `__Host-site` (Secure, HttpOnly,
   SameSite=Lax, Path=/, host-only), and redirects to `<path>`.

Each site request re-checks the role so revocation takes effect immediately.

### Public links

An owner can enable "anyone with the link" per site. The link is
`https://<id>.sites.example.com/_auth/share/<token>`; it sets a site cookie and
redirects to `/`, removing the token from the address bar. Rotating the token
invalidates the old link and every cookie issued from it. Disabling it reverts
the site to member-only.

### CSRF and CORS

Site hosts are same-site with the app (`*.example.com`), so `SameSite` cookies
do not stop an artifact from sending credentialed requests to the app. Required:

- Every state-changing app request (including `/oauth/authorize` consent)
  checks `Origin` exactly equals `APP_ORIGIN` and validates a CSRF token.
- App cookies use the `__Host-` prefix so site hosts cannot set or overwrite them.
- `/mcp` and `/oauth/token` authenticate by bearer token / client credentials
  only and never accept the session cookie.
- No CORS response ever allows a site origin; credentialed CORS is limited to
  `APP_ORIGIN`.
- Site responses send `X-Content-Type-Options: nosniff`, no CORS headers, and
  `Referrer-Policy: no-referrer` on `/_auth/*`.

## Accounts

- Anyone can sign up and create sites.
- Sign-in methods: passkey (WebAuthn) or Google OIDC. No passwords.
- Email verification:
  - Google: verified when the ID token has `email_verified: true`.
  - Passkey sign-up: the typed email is unverified unless SMTP is configured and
    the user confirms a verification link.
- Google identities are linked to an existing account only explicitly, from
  `/settings/security` while signed in. A Google sign-in with a new `sub` creates
  a new account; if another account holds the same email unverified, that
  account loses the email claim.
- Recovery codes: 10 single-use codes generated at sign-up and shown once,
  stored hashed. A code signs the user in to register a new passkey.
  Regenerating invalidates all previous codes.

## Sharing

- `share_site` (or the UI) creates an invite for an email and role and always
  returns an invite link: `https://dash.example.com/invite/<token>`. With SMTP
  configured and `notify` set, the link is also emailed.
- The grant binds to whichever account accepts the invite link. It is applied
  automatically, without the link, only to an existing account whose matching
  email is verified.
- Invites are single use and can be revoked while pending.

## Roles

| Action | Owner | Manager | Viewer |
|---|:-:|:-:|:-:|
| View site | ✓ | ✓ | ✓ |
| Publish, update files, roll back | ✓ | ✓ | |
| Edit title/description | ✓ | ✓ | |
| Invite/remove viewers | ✓ | ✓ | |
| Add/remove managers | ✓ | | |
| Enable/disable/rotate public link | ✓ | | |
| Transfer ownership | ✓ | | |
| Delete site | ✓ | | |

- Exactly one owner per site.
- Ownership transfer targets an email and completes when the recipient accepts
  (same binding rules as invites). The previous owner becomes a manager.

## Versions

Every publish creates an immutable version; the site serves its current
version. Previous versions are kept and any can be restored with
`rollback_site`, which repoints the site (no copy). Publishing is atomic:
viewers see either the old version or the new one.

## MCP authorization

- OAuth 2.1 per the MCP authorization spec (2025-11-25), with PKCE (S256) and
  resource indicators binding tokens to `APP_ORIGIN/mcp`.
- Client registration via Client ID Metadata Documents (CIMD) only: the
  `client_id` is an HTTPS URL to the client's metadata JSON.
- CIMD fetching is an SSRF surface: HTTPS only, resolve and reject private,
  loopback, and link-local addresses, short timeout, response size cap, no
  redirects to disallowed hosts, cache per HTTP headers. Verify the document's
  `client_id` equals its URL and that the requested `redirect_uri` is listed.
- The consent screen shows the client name and the `client_id` host.
- Users review and revoke OAuth grants in `/settings/security`.

## MCP tools

`site` arguments take a site id. Roles are the minimum required.

| Tool | Purpose | Role |
|---|---|---|
| `whoami` | Caller's account | any |
| `list_sites` | Sites the caller owns or manages, with role and URL | any |
| `create_site` | Create from a title/description; returns id and URL | any |
| `get_site` | Metadata, URL, current version, public-link state, member count | manager |
| `update_site` | Title, description | manager |
| `delete_site` | Delete the site and its versions | owner |
| `transfer_ownership` | Start a transfer to an email; returns the acceptance link | owner |
| `publish_site` | One-shot atomic publish: `files[{path, content, encoding: utf8\|base64}]`, `mode: replace\|merge`, `delete_paths[]`, `notify` | manager |
| `create_deployment` | Start a staged publish (`mode: replace\|merge`); returns a deployment id | manager |
| `upload_files` | Add files to a staged deployment | manager |
| `get_upload_url` | Short-lived signed URL to upload a `.tar.gz`/`.zip` into a staged deployment | manager |
| `finalize_deployment` | Atomically publish a staged deployment as a new version | manager |
| `list_files` | Paths, sizes, content types in the current version | manager |
| `read_file` | Read one file from the current version | manager |
| `list_versions` | Versions with timestamps and publisher | manager |
| `rollback_site` | Make a previous version current | manager |
| `list_members` | Members, roles, pending invites | manager |
| `share_site` | Invite an email as viewer or manager; returns the invite link | manager (viewer), owner (manager) |
| `set_member_role` | Change a member between viewer and manager | owner |
| `revoke_access` | Remove a member or pending invite | manager (viewers), owner (managers) |
| `set_public_link` | `enable`, `disable`, or `rotate`; returns the link | owner |

## Storage

One adapter interface with S3, GCS, and local filesystem implementations.
Versions are stored as immutable file sets keyed by site id and version id.
