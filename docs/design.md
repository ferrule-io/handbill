# handbill — Design

Decisions here implement [INTENT.md](../INTENT.md).

## Architecture

One Go binary, one container image. No Node.js at runtime.

```mermaid
flowchart LR
  agent[MCP client] -->|OAuth bearer| mcp["/mcp"]
  browser[Browser] --> app["dash.example.com<br/>app UI + auth + OAuth"]
  browser --> sites["*.sites.example.com<br/>site file server"]
  subgraph bin[handbill binary]
    mcp
    app
    sites
  end
  bin --> pg[(PostgreSQL)]
  bin --> store[(S3 / GCS / local)]
```

The server routes by `Host`: `APP_ORIGIN` host serves the UI, auth, OAuth, and
`/mcp`; any `<id>.SITES_DOMAIN` host serves that site's files.

### Backend (Go)

| Concern | Choice |
|---|---|
| HTTP routing | `net/http` `ServeMux` |
| PostgreSQL | `pgx` with `sqlc`-generated queries |
| Migrations | `goose`, embedded, run at startup |
| Passkeys | `github.com/go-webauthn/webauthn` |
| Google sign-in | `github.com/coreos/go-oidc` + `golang.org/x/oauth2` |
| MCP | `github.com/modelcontextprotocol/go-sdk` (≥ 1.8) |
| OAuth 2.1 authorization server | In-house, limited to authorization code + PKCE, refresh tokens, CIMD, optional DCR |
| Storage | `aws-sdk-go-v2` (S3), `cloud.google.com/go/storage` (GCS), filesystem |

### Frontend (Next.js)

- Next.js App Router + TypeScript with `output: 'export'`; the build output is
  embedded into the Go binary with `embed`. No SSR, server actions, or
  middleware — all data comes from the Go JSON endpoints under `/app-api/`
  (session-cookie authenticated, CSRF-protected; internal to the UI, not a
  public API).
- Every UI route is a fixed path; there are no dynamic segments, because static
  export cannot serve `[id]` routes for IDs created at runtime (neither on direct
  load nor on client-side `<Link>` navigation, which fetches per-route RSC
  payloads). Parameters travel in the query string or fragment:
  `/site?id=<site-id>` and `/invite#token=<token>`. The client reads them with
  `useSearchParams` (inside `Suspense`) or `location.hash`.
- Go serves the export as files: `/<route>` → `<route>.html`, RSC payloads and
  assets as-is, unknown paths → the export's `404.html` with status 404. There
  is no catch-all HTML fallback.
- Acceptance check for the scaffold: with a site and an invite created at
  runtime, both a direct page load and client-side `<Link>` navigation to
  `/site?id=…` and `/invite#token=…` render correctly.
- Tailwind CSS + shadcn/ui; `@simplewebauthn/browser` for passkey ceremonies.
- Screens: sign-up, sign-in, recovery-code display and use, site list, site
  detail (overview, members and invites, public link, versions, transfer,
  delete), invite accept, OAuth consent, security settings (passkeys, Google
  link, recovery codes, OAuth grants).
- One pattern per kind of feedback, in `web/components/ui/`: `CopyButton`
  (every copy-to-clipboard), `ConfirmDialog` (every consequential action;
  stays open while pending, shows errors inline), `FormError` (inline errors
  under forms), `Spinner` (pending buttons, with an "…ing" label); and
  `LoadError` in `components/layout/empty-state.tsx` for failed loads with a
  retry. Mutations disable their control while pending.
- Accessibility: colors meet WCAG AA (text 4.5:1, field outlines and focus
  rings 3:1) in both themes; `--brand` is the brand-pack vermilion for marks,
  ticks, and focus rings, and `--brand-strong` the AA-safe one for text and
  fills under text. Touch screens (`pointer-coarse`) get 44 px targets.
  Client-supplied strings (OAuth client names and hosts) render inside
  `<bdi>`. The consent screen's Allow button stays disabled until the page
  has been visible and focused for 500 ms (clickjacking). `web/e2e/a11y.spec.ts`
  runs axe on every main screen in light and dark and fails on serious or
  critical violations.

### Site file server

No rendering. For a `GET`/`HEAD` on `<id>.SITES_DOMAIN`: authorize (site
cookie, else redirect to the handoff), resolve the path against the site's
current version, and stream the blob from storage. Every path except the
reserved `/_auth/` is site content, including `/healthz` and `/readyz`.

- Resolution: `/` and paths ending in `/` → `<dir>/index.html`; `/foo` →
  `foo`, else (when `foo/index.html` exists) a 301 to `/foo/` keeping the
  query string, so relative links resolve like on other static hosts, else
  `foo.html`. Paths are case-sensitive.
  Anything else → the site's `404.html` with status 404 if it has one, else a
  plain-text 404. A site with nothing published answers a plain 404.
- Headers: `Content-Type` from the file's recorded type, `Content-Length`,
  `ETag` = the blob's SHA-256 (`If-None-Match` → 304), `Cache-Control:
  private, no-cache` (so every load re-checks access), `X-Content-Type-Options:
  nosniff`, and `Strict-Transport-Security: max-age=31536000` when
  `APP_ORIGIN` is `https`.
- Service workers are refused: a request carrying `Service-Worker: script`
  gets 403 before authorization. A worker registered by site content would
  control `/_auth/` navigations (stealing tickets and public-link tokens) and
  keep serving cached content after its visitor's access is revoked. Every
  response that issues a site cookie also sends `Clear-Site-Data:
  "storage"` (which leaves cookies alone), so the origin's storage and any
  worker registered earlier are dropped; site content cannot rely on
  `localStorage` or IndexedDB surviving a sign-in.
- One request resolves against the version that the access check returned,
  so a concurrent publish or rollback never mixes versions in one response.

### Repository layout

| Path | Contents |
|---|---|
| `cmd/handbill/` | Server entrypoint |
| `internal/` | Go packages (http, auth, oauth, mcp, sites, storage, db) |
| `web/` | Next.js app |
| `deploy/helm/handbill/` | Helm chart |
| `Dockerfile` | Multi-stage: build `web/`, build Go with embedded assets, distroless runtime |

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
| `/site?id=<site-id>` | Settings, members, invites, public link, versions |
| `/invite#token=<token>` | Accept an invite or ownership transfer (signs up or signs in first). The token is in the fragment so it never reaches server or proxy logs. |
| `/signup`, `/login` | Passkey or Google; both honor `?return=<same-origin path>` |
| `/signup/codes` | The new account's first recovery codes, shown once (both sign-up paths land here) |
| `/login/recover` | Recovery code sign-in, then registering a new passkey |
| `/verify-email#token=<token>` | Consumes an email verification link (fragment, like invites) |
| `/auth/google/callback` | Google OIDC callback |
| `/auth/site` | Issues site tickets (handoff below) |
| `/settings/security` | Passkeys, linked Google identity, recovery codes, OAuth grants |
| `/consent?request=<id>` | OAuth consent screen; `?error=<code>` when a request can't continue |
| `/mcp` | MCP endpoint (OAuth bearer tokens only) |
| `/.well-known/oauth-protected-resource/mcp` | Protected resource metadata for `/mcp` (the URL `WWW-Authenticate` points to); the root `/.well-known/oauth-protected-resource` serves the same document |
| `/.well-known/oauth-authorization-server` | AS metadata, `client_id_metadata_document_supported: true` |
| `/oauth/authorize`, `/oauth/token` | OAuth 2.1 |
| `/oauth/revoke` | Token revocation (RFC 7009) |
| `/oauth/register` | Dynamic Client Registration; only when `OAUTH_DCR_ENABLED=true` |
| `/api/deployments/<id>/archive` | Archive upload target returned by `get_upload_url` |
| `/app-api/*` | JSON endpoints for the UI only (session cookie + CSRF) |

### Site sign-in handoff

The app session cookie is host-only on `dash.example.com`; site hosts never
receive it.

1. A request to `https://<id>.sites.example.com/<path>` without a valid site
   cookie redirects to `https://dash.example.com/auth/site?site=<id>&return=<path>`.
2. The app requires a session (redirecting to `/login?return=<this URL>` if
   needed) and the viewer role, then redirects to
   `https://<id>.sites.example.com/_auth/callback?ticket=<t>`. The ticket is a
   random token bound to the site id, user, and return path, valid 60 s,
   single use (stored hashed and consumed atomically on redemption; a ticket
   presented on another site's host is consumed and refused). A signed-in user
   without a role is redirected to `/site?id=<id>`, which says the site does
   not exist or was not shared with them: the answer is the same whether or
   not the site exists, and no ticket is issued.
3. The site host redeems the ticket, sets `__Host-site` (Secure, HttpOnly,
   SameSite=Lax, Path=/, host-only, 12 h), and redirects to `<path>` with
   `Referrer-Policy: no-referrer`. The cookie is short because the handoff is
   invisible while the app session lives; the per-request check below is what
   enforces revocation.

Return paths must start with a single `/` (not `//`) and contain no
backslashes or control characters; anything else is rejected with 400.

Each site request loads the site's access state (current version, membership,
public-link enabled flag and generation) and re-checks the cookie against it,
so revocation, disabling, and rotation take effect on the next request.

### Public links

An owner can enable "anyone with the link" per site. The link is
`https://<id>.sites.example.com/_auth/share#token=<token>`: the token is in the
fragment, like invite links, so it never reaches server, proxy, or CDN logs.
`GET /_auth/share` answers a fixed page whose only script (allowed by its
SHA-256 in a `default-src 'none'` CSP with `form-action 'self'`) clears the
fragment from the address bar and history and posts the token to `POST
/_auth/share` (form-encoded, 1 KiB cap). That sets a site cookie and redirects
(303) to `/`. The POST checks no `Origin`: forcing a browser to redeem a link
only gives it the anonymous access the link grants. Without JavaScript the
page says to turn it on. Rotating the token invalidates the old link and
every cookie issued from it. Disabling it reverts the site to member-only.

- The cookie carries no user and the link's generation; it lasts as long as a
  member cookie (12 h), after which the visitor opens the link again. An
  unknown token, another site's token, or a disabled link gets the same
  neutral 404 page and no cookie.
- `enable` on a link that is on is refused (its token cannot be shown again;
  `rotate` issues a new one), `rotate` on a link that is off is refused, and
  `disable` on one that is off is a no-op. Re-enabling a disabled link issues
  a new token and generation, so cookies from before the disable stay dead.
- Managers see whether the link is on; only the owner changes it.

### CSRF and CORS

Site hosts are same-site with the app (`*.example.com`), so `SameSite` cookies
do not stop an artifact from sending credentialed requests to the app. Required:

- Every state-changing request authenticated by the session cookie (including
  `/oauth/authorize` consent) checks `Origin` exactly equals `APP_ORIGIN` and
  validates a CSRF token.
- App cookies use the `__Host-` prefix so site hosts cannot set or overwrite them.
- The CSRF token is an HMAC (`SECRET_KEY`) of a random `__Host-csrf` cookie
  (HttpOnly), returned by `GET /app-api/me` and sent as `X-CSRF-Token`; it
  applies to public endpoints too (sign-up, sign-in, sign-out), so a site
  cannot start ceremonies or sign the viewer out. `appapi` also requires
  `Content-Type: application/json` for request bodies.
- Non-cookie endpoints are exempt from the Origin/CSRF check and never accept
  the session cookie: `/mcp` (bearer token), `/oauth/token` and `/oauth/revoke`
  (PKCE/client authentication), and `/api/deployments/<id>/archive` (signed
  short-lived URL).
- Site responses send `X-Content-Type-Options: nosniff`, no CORS headers, and
  `Referrer-Policy: no-referrer` on `/_auth/*`.
- App pages (the static export) send `X-Frame-Options: DENY` and a CSP built
  per page when the export is indexed: `script-src 'self'` plus the SHA-256 of
  each of that page's inline scripts (Next's theme bootstrap and RSC payload),
  `style-src 'self' 'unsafe-inline'` (React renders style attributes),
  `default-src 'self'`, `object-src 'none'`, `base-uri 'none'`, `form-action
  'self'`, `frame-ancestors 'none'`. With an `https` `APP_ORIGIN`, every
  app-host and site-host response sends `Strict-Transport-Security:
  max-age=31536000` (no `includeSubDomains`: the app does not own its parent
  domain).

## Tokens and secrets

No bearer secret is stored in plaintext. The plaintext exists only in the
response that issues it.

| Secret | Generated as | Stored as |
|---|---|---|
| Session IDs, site tickets, OAuth authorization request handles, OAuth authorization codes, access and refresh tokens, invite and ownership-transfer tokens, public-link tokens, email-verification tokens | 32 random bytes, base64url | SHA-256 of the token; looked up by hash. Single-use tokens (tickets, authorization request handles, authorization codes, invites, transfers, verification) are consumed atomically. |
| Recovery codes | 16 base32 characters (80 bits), shown grouped | argon2id, plus a unique lookup hash: HMAC-SHA256 of the normalized code under a key derived from `SECRET_KEY`. A sign-in finds the one code by lookup hash and runs argon2id once (on a dummy hash when none matches); consumed atomically. Rotating `SECRET_KEY` invalidates every code. |
| Site cookies, signed upload URLs | HMAC-SHA256 with `SECRET_KEY`, carrying subject, site, expiry (site cookies from a public link also carry the link generation; upload URLs carry the deployment and the requesting user) | Not stored; validity also requires the current database state (role or link generation for cookies; for upload URLs an open deployment created by the URL's user, who can still publish to the site) |
| Google client secret, SMTP password, storage credentials | Configuration / Kubernetes Secret | Not in the database |

Consequences:

- Invite, ownership-transfer, and public links are shown only when issued.
  `get_site` and `list_members` report their state, never the link. To share
  again, re-issue the invite or `rotate` the public link.
- Rotating a public link bumps its generation. The per-request site lookup
  compares the cookie's generation with the current one, so cookies from the
  old link stop working immediately.

## Accounts

- Anyone can sign up and create sites.
- Sign-in methods: passkey (WebAuthn) or Google OIDC. No passwords.
- Sign-up is one screen: an email and "Create with passkey", or "Continue
  with Google" (shown only when Google is configured; `GET /app-api/me`
  reports `googleEnabled` and `mailEnabled`). Passkeys are registered as
  discoverable (resident key preferred, user verification preferred), so
  sign-in is usernameless; an email-first fallback limits the prompt to the
  passkeys of the accounts claiming that address (its verified holder, else
  every unverified claim). An address without passkeys (no account, or
  Google only) gets the same answer with a decoy instead: one 16-byte
  credential ID derived from HMAC(`SECRET_KEY`, address), stable across
  attempts, transports `internal` and `hybrid`; no authenticator holds it,
  and finishing that ceremony fails like an unknown passkey. Still
  distinguishable: a real account's credential count, ID lengths, and
  transports. The WebAuthn RP ID is `APP_ORIGIN`'s host and the
  only accepted origin is `APP_ORIGIN`; the user handle is the user id.
  Sign counts are stored; a counter that goes backwards sets
  `clone_warning` and refuses that sign-in.
- Ceremony state (WebAuthn challenges; Google state, nonce, and PKCE
  verifier) lives in `auth_flows`, hashed, single use, 10-minute expiry. The
  Google round trip is also bound to the browser by a `__Host-google-flow`
  cookie that must equal `state`, and starts with a CSRF-protected POST.
- Email verification:
  - Google: verified when the ID token has `email_verified: true`.
  - Passkey sign-up: the typed email is unverified unless SMTP is configured and
    the user confirms a verification link (`/verify-email#token=`, single use,
    24 hours; resend from settings). Verifying displaces other accounts'
    unverified claims; an address another account holds verified cannot be
    verified again.
  - At most 5 accounts may claim one address unverified. Past that, a
    passkey sign-up with it is refused (`email_unavailable`, naming nobody),
    and a new Google account whose unverified email it is starts without an
    email; it gains the address once Google reports it verified.
- Google identities are linked to an existing account only explicitly, from
  `/settings/security` while signed in. A Google sign-in with a new `sub` creates
  a new account; if another account holds the same email unverified, that
  account loses the email claim. If another account holds it verified, no
  account is created and the sign-in page says to sign in with a passkey and
  link Google from settings.
- An account always keeps one sign-in method: removing the last passkey or
  unlinking Google when it is the only method is refused.
- Recovery codes: 10 single-use codes issued right after sign-up (on
  `/signup/codes`) and shown once, stored hashed (see Tokens and secrets).
  An email plus a code opens a restricted session (15 minutes, never
  renewed, not visible to other packages as `identity.User`) whose only
  power is registering a new passkey; doing so replaces it with a normal
  session. The code must belong to an account claiming the address
  (verified or not); a wrong code and an unknown address get the same
  answer at the same cost. Regenerating invalidates all previous codes.
  Codes issued before the lookup hash existed were deleted (migration
  `20261114100000_recovery_lookup`); those accounts regenerate them from
  settings, which shows none remaining.
- Sessions: `__Host-session` cookie (Secure, HttpOnly, SameSite=Lax, Path=/)
  holding a random token whose SHA-256 is stored. A session expires after 30
  days without use and at most 90 days after sign-in; use renews the idle
  deadline at most once an hour. Every sign-in replaces the browser's
  previous session. Settings lists sessions and signs out the others.
- Return paths (`?return=`) must start with a single `/`, with no `//`
  prefix, backslash, or control characters; anything else falls back to `/`.

## Sharing

- `share_site` (or the UI) creates an invite for an email and role and always
  returns an invite link: `https://dash.example.com/invite#token=<token>`. With SMTP
  configured, `notify` set, and the inviter's primary email verified, the
  link is also emailed; otherwise the result says why not
  (`emailNotSentReason` in the app API, `email_not_sent_reason` in MCP:
  `mail_disabled`, `sender_unverified`, or `send_failed`). Transfer and
  update mail follow the same rule: handbill never mails in the name of an
  unverified address.
- The invite page keeps the token in `sessionStorage` across the sign-in or
  sign-up redirect (Google round trip), then accepts it with a CSRF-protected
  POST and clears it.
- The grant binds to whichever account accepts the invite link. It is applied
  automatically, without the link, only to an existing account whose matching
  email is verified.
- Invites are single use, expire after `INVITE_TTL` (default 14 days), and can
  be revoked while pending.
- Rules: inviting one of your own addresses is refused; an address whose
  verified holder already has a role is refused (roles change with
  `set_member_role`); re-inviting an address with a pending invite revokes
  the old link, so only the newest works. That needs the right to revoke
  it: a manager re-inviting an address with a pending manager invite is
  refused (input error) and nothing changes.
- Member lists show each member's primary email and whether it is verified
  (`emailVerified` in the app API, `email_verified` in MCP).
- Auto-binding uses up the invite: the returned link then only leads its
  holder to the site (accepting it again is a no-op) and works for no other
  account.
- Accepting never lowers a role (a manager accepting a viewer invite stays a
  manager; the owner stays owner). Re-accepting a link the same account
  already used is a no-op that leads to the site.
- The invite page shows the site title, inviter, and role only to a holder of
  a pending link, or to the account that accepted it. Signed out, it offers
  sign-up first and sign-in second; on return it accepts without another
  click and lands on the site page with an "Open site" call to action.
- Removing a member or changing a role takes effect on their next request,
  in the app and on the site host. The owner cannot be removed or demoted
  except by a transfer.

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
  (same binding rules and `INVITE_TTL` expiry as invites). The previous owner
  becomes a manager. With `notify`, the link is emailed only when the
  owner's primary email is verified (see Sharing).
- A site has at most one pending transfer: starting another replaces it, the
  owner can cancel it, and completing it revokes any other. Its link has the
  invite form (`/invite#token=<token>`); the same page accepts both. The owner
  cannot offer the site to one of their own addresses, and accepting while
  signed in as the current owner is refused and leaves the transfer pending.
  The swap (old owner demoted, new owner promoted, `sites.owner_id` moved) is
  one transaction.

## Versions

Every publish creates an immutable version; the site serves its current
version. All previous versions are kept (no retention limit) and any can be
restored with `rollback_site`, which repoints the site (no copy). Publishing is
atomic: viewers see either the old version or the new one.

## Publishing

Publishing goes through staged deployments (`create_deployment`,
`upload_files`, `get_upload_url`, `finalize_deployment`); `publish_site` is
the one-shot form of the same pipeline.

- Paths are relative and slash-separated. A leading `./` is dropped;
  absolute paths, `.` and `..` segments, empty segments (including a
  trailing `/`), backslashes, NUL and other C0/C1 control characters,
  invisible format characters (Unicode Cf: bidi overrides and isolates,
  zero-width characters, BOM), invalid UTF-8, paths over 1024 bytes, and the
  reserved top-level `_auth/` are rejected. Paths are case-sensitive.
- Content type comes from the extension (a fixed table of web types, then
  Go's `mime` package, else `application/octet-stream`); textual types get
  `charset=utf-8`. An explicit content type on a file overrides it.
- Blobs are stored once per site under their SHA-256; a blob the site
  already has is not uploaded again. A blob whose site is deleted while it
  uploads is removed again (the upload fails as not found), so a delete
  racing an upload leaves no object behind.
- A deployment belongs to its creator: only they can add files to it, get
  an upload URL for it, upload into it, or finalize it; other accounts, the
  owner included, get a "created by another account" error. The version
  records the creator as its publisher.
- `replace` publishes exactly the deployment's files and must contain at
  least one. `merge` starts from the version that is current when the
  deployment is finalized (the site row is locked, so concurrent merges
  apply in turn and never drop each other's files), applies added and
  overwritten files, and removes deleted paths; deleted paths the version
  lacks are ignored. `delete_paths` is rejected in replace mode. A result
  with no files is rejected. Within a deployment the last operation on a
  path wins.
- Finalizing creates the immutable version and its file list and repoints
  the site in one transaction; a deployment finalizes once.
- Archives (`.tar.gz`, `.zip`) are streamed entry by entry. Symlinks, hard
  links, devices, FIFOs, and invalid paths reject the whole archive;
  directory entries are skipped. When every file shares one top-level
  directory (`site/index.html`, `site/css/app.css`, as `tar czf site.tar.gz
  site/` produces), that directory is stripped; otherwise paths are kept as
  they are. Limits (`internal/sites/limits.go`): 10 000 entries, 64 MiB per
  file, 512 MiB uncompressed per archive (also the cap on compressed bytes
  read). A zip's entry count is checked before its central directory is
  read, both as declared (zip64 included) and as headers present, so a
  directory holding more headers than it declares is rejected. A tar.gz's
  decompressed stream, tar metadata (PAX, GNU long names) included, is
  capped at 512 MiB plus 4 KiB per allowed entry.

## MCP authorization

- OAuth 2.1 per the MCP authorization spec (2025-11-25), plus the 2026-07-28
  server requirements: `iss` on every authorization response (RFC 9207) with
  `authorization_response_iss_parameter_supported: true`, and refresh tokens
  rotated on every use. CIMD per draft-ietf-oauth-client-id-metadata-document-03.
  PKCE (S256) is required, and resource indicators bind tokens to
  `APP_ORIGIN/mcp`. handbill defines no scopes (`offline_access` is accepted
  and recorded, with no effect).
- Client registration via Client ID Metadata Documents (CIMD): the
  `client_id` is an HTTPS URL to the client's metadata JSON.
- Dynamic Client Registration (RFC 7591), which MCP 2026-07-28 deprecates in
  favor of CIMD, is off by default. With `OAUTH_DCR_ENABLED=true`,
  `/oauth/register` is served and advertised as `registration_endpoint` in AS
  metadata. Registration is unauthenticated; the consent screen labels DCR
  clients as unverified. Rate limiting it (and `/oauth/authorize`, which
  triggers CIMD fetches, and `/oauth/token`) is W9's. Turning DCR off also
  stops clients registered earlier: authorize, token, and their access
  tokens at `/mcp` are refused.
- Redirect URIs, for CIMD and DCR alike, are absolute, without fragment or
  userinfo, and either `https` or loopback `http` (`127.0.0.1`, `[::1]`,
  `localhost`). A loopback registration matches any port (RFC 8252 §7.3):
  Claude Code registers `http://localhost/callback` and sends a random port.
  Everything else must match byte for byte.
- `/oauth/authorize` never redirects to the client by itself: a client can
  register any `https` redirect URI (RFC 9700 §4.11.1). Every error before
  the consent decision, signed in or not, goes to `/consent?error=<code>`
  with the OAuth error code (`invalid_request`, `unsupported_response_type`,
  `invalid_target`, `unauthorized_client`, …). Only the decision redirects:
  the code, or `access_denied`.
- A CIMD `client_id` host must not be the app host or `SITES_DOMAIN` or a
  subdomain, on any port, and must not end in a dot.
- CIMD fetching is an SSRF surface: HTTPS only, short timeout, 5 KiB
  response cap, no redirects (any 3xx is an error; CIMD-03 §6), cache per
  HTTP headers (clamped to 5 min–24 h, revalidated with `If-None-Match`).
  Resolved addresses are checked before and at dial time, default-deny: only
  global unicast (IPv6 within `2000::/3`) outside every IANA special-purpose
  block that is not globally reachable. Mapped, SIIT, IPv4-compatible, and
  NAT64 `64:ff9b::/96` addresses are judged by their embedded IPv4 address
  (IPv6-only hosts with DNS64 need NAT64); Teredo, 6to4, and
  `64:ff9b:1::/48` are refused. Verify the document's `client_id` equals its
  URL and that the requested `redirect_uri` is listed.
- The consent screen is always shown. It shows the client name (control and
  format characters such as bidi overrides removed), the client host, and
  the redirect URI's host; it warns when the redirect goes to a program on
  this computer (loopback) and labels DCR clients as unverified. The client
  host is the `client_id` host for CIMD and, for DCR, the host of the
  redirect URI this request uses (the grants list shows the first
  registered one).
- Exchanging a code consumes it and issues its tokens in one transaction. A
  replayed code revokes everything it issued; the replay locks the code row
  first, so it waits for a concurrent exchange and revokes its tokens too.
- Users review and revoke OAuth grants in `/settings/security`; clients
  revoke tokens at `/oauth/revoke` (RFC 7009).

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
| `transfer_ownership` | Start a transfer to an email (`notify` emails it with SMTP when the owner's address is verified; `emailed` and `email_not_sent_reason` report it); returns the acceptance link, replacing any pending transfer | owner |
| `publish_site` | One-shot atomic publish: `files[{path, content, encoding: utf8\|base64}]`, `mode: replace\|merge`, `delete_paths[]`, `notify` (with SMTP and a verified publisher address, emails the other members that the site was updated; `notify_result` reports what was sent, or why not: mail not configured, publisher's address not verified, or the mail server refused) | manager |
| `create_deployment` | Start a staged publish (`mode: replace\|merge`); returns a deployment id | manager |
| `upload_files` | Add files to a staged deployment | manager, creator only |
| `get_upload_url` | Short-lived signed URL, bound to the caller, to upload a `.tar.gz`/`.zip` into a staged deployment | manager, creator only |
| `finalize_deployment` | Atomically publish a staged deployment as a new version | manager, creator only |
| `list_files` | Paths, sizes, content types in the current version | manager |
| `read_file` | Read one file from the current version | manager |
| `list_versions` | Versions with timestamps and publisher | manager |
| `rollback_site` | Make a previous version current | manager |
| `list_members` | Members (user id, email, `email_verified`, role), pending invites (invite id, email, role, expiry); never links | manager |
| `share_site` | Invite an email as viewer or manager; always returns the invite link, plus `granted` (a verified account got access now), `emailed`, and `email_not_sent_reason` (`notify` set but not emailed: `mail_disabled`, `sender_unverified`, `send_failed`) | manager (viewer), owner (manager) |
| `set_member_role` | Change a member between viewer and manager | owner |
| `revoke_access` | Remove a member or pending invite, by user id or invite id | manager (viewers), owner (managers) |
| `set_public_link` | `enable`, `disable`, or `rotate`; enable and rotate return the link, shown only in that result | owner |

Tool annotations: `list_members` is read-only; `get_upload_url` is not (it
mints a bearer URL); `revoke_access`, `set_public_link`, and
`transfer_ownership` are destructive; `set_member_role` is idempotent.

## Storage

One adapter interface (`Store`) with `local`, `s3`, and `gcs` implementations.

Blobs are content-addressed and immutable:
`sites/<site-id>/blobs/<sha256-hex>`

Versions and deployments do not duplicate blobs; their file lists (path, blob
SHA-256, size, content type) are maintained in PostgreSQL (`version_files` and
`deployment_files`). Deleting a site deletes the entire storage prefix
`sites/<site-id>/` via `DeletePrefix`.

- `local`: Atomic writes via same-directory temp files, fsync, and rename.
  Content type stored in a `.meta` sidecar JSON. Enforces strict root isolation
  without following symlinks outside root via Go's `os.OpenRoot`.
- `s3`: Standard AWS SDK v2 client supporting AWS S3 and S3-compatible endpoints
  (e.g. MinIO). Handles pagination and batch deletions of up to 1000 objects.
- `gcs`: Google Cloud Storage client supporting Application Default Credentials
  and `STORAGE_EMULATOR_HOST` for testing.
- Key validation: all keys must be relative paths with no empty, `.`, or `..`
  segments, no backslashes, no leading slash, and no NUL bytes.

## Configuration

The server reads only environment variables. Invalid configuration fails
startup with one message listing every offending variable. The Helm chart
sets exactly these.

| Variable | Default | Meaning |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `APP_ORIGIN` | required | App origin, e.g. `https://dash.example.com`: scheme and host (optional port), no path |
| `SITES_DOMAIN` | required | Parent domain of site hosts, e.g. `sites.example.com`; must not contain the app host |
| `SECRET_KEY` | required | HMAC key for site cookies and upload URLs, at least 32 bytes |
| `DATABASE_URL` | empty | PostgreSQL URL or libpq key/value DSN. When empty, the libpq variables `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`, `PGDATABASE`, `PGSSLMODE` are used |
| `STORAGE_TYPE` | `local` | `local`, `s3`, or `gcs` |
| `STORAGE_LOCAL_DIR` | `/data` | Root directory of the `local` adapter |
| `S3_BUCKET`, `S3_REGION` | required for `s3` | Bucket and region |
| `S3_ENDPOINT` | empty | Custom endpoint URL (e.g. MinIO) |
| `S3_PREFIX` | empty | Key prefix |
| `S3_FORCE_PATH_STYLE` | `false` | Path-style addressing (MinIO) |
| `GCS_BUCKET` | required for `gcs` | Bucket |
| `GCS_PREFIX` | empty | Key prefix |
| `SMTP_HOST` | empty | Mail server; notifications are disabled when empty |
| `SMTP_PORT` | by `SMTP_TLS`: 587, 465, 25 | Mail server port |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | empty | Set both or neither |
| `SMTP_FROM` | required with `SMTP_HOST` | Sender, e.g. `handbill <dash@example.com>` |
| `SMTP_TLS` | `starttls` | `starttls`, `tls` (implicit TLS), or `none` |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | empty | Google sign-in; set both or neither (disabled when unset) |
| `OAUTH_DCR_ENABLED` | `false` | Serve `/oauth/register` (Dynamic Client Registration) |
| `INVITE_TTL` | `336h` | Lifetime of invites and ownership transfers (Go duration) |

- S3 credentials come from the AWS SDK default chain (environment, shared
  config, web identity / IRSA, instance role); GCS credentials from
  Application Default Credentials (including workload identity).
- Booleans accept Go's `strconv.ParseBool` forms (`true`, `false`, `1`, `0`).

Runtime contract:

- The container listens on port 8080 and runs as UID/GID 65532 (distroless
  `nonroot`); the `local` adapter writes under `/data`, which the image
  creates owned by that user.
- At startup the server applies pending migrations (serialized across
  replicas by an advisory lock) before listening, and exits non-zero if the
  database is unreachable; the orchestrator's restart policy retries it. The
  schema targets PostgreSQL 17 and later.
- `GET /healthz` (liveness: the process serves HTTP) and `GET /readyz`
  (readiness: the database and storage answer within 3 s) respond on every
  host except site hosts, because Kubernetes probes use the pod IP. On
  `<id>.SITES_DOMAIN` those paths are site content.
- Requests are routed by `Host` (port ignored): `APP_ORIGIN`'s host serves the
  app; `<id>.SITES_DOMAIN`, with `<id>` a valid site id, serves that site; any
  other host gets 404.
- Logs are JSON lines on stdout; query strings and `/_auth/` path tails are
  never logged.

Local development (`mise.toml` `[env]`, `compose.yaml`): the app runs on
`http://dash.localhost:8080`, sites on `http://<id>.sites.localhost:8080`
(browsers resolve `*.localhost` to loopback), and PostgreSQL on host port 55432.

## Deployment

### Image

One image containing the static Go binary with the embedded frontend, on a
distroless base, running as non-root.

### Helm chart

| Values | Behavior |
|---|---|
| `postgresql.enabled` (default `true`) | Bundled single-instance PostgreSQL StatefulSet (official `postgres` image) with a PVC. Intended for small installs. |
| `externalDatabase.*` | Used when `postgresql.enabled=false`: `host`, `port`, `database`, `user`, `sslmode` (default `verify-full`), and `existingSecret`/`existingSecretPasswordKey`, or a full DSN via `existingSecret`. The bundled database stays `sslmode=disable` (in-cluster traffic). |
| `ingress.enabled` (default `false`) | Creates an Ingress with two rules: the app host and `*.<sitesDomain>`. `ingress.className`, `annotations`, and `tls` are configurable. |
| `storage.type` | `local`, `s3`, or `gcs`, with per-adapter settings; credentials from an existing Secret or workload identity. |
| `persistence.*` | PVC for the `local` adapter. |
| `smtp.*` | Optional; notifications are disabled when unset. |
| `google.*` | OAuth client ID and secret (from an existing Secret). |
| `oauth.dcrEnabled` (default `false`) | Maps to `OAUTH_DCR_ENABLED`. |

- Without the chart's Ingress, the operator routes both hostnames to the
  Service (their own Ingress, Gateway API, or a tunnel).
- The sites wildcard needs a wildcard TLS certificate; with cert-manager this
  requires a DNS-01 issuer.
- The `local` adapter with more than one replica requires a `ReadWriteMany`
  volume; otherwise use S3 or GCS.
- `SITES_DOMAIN` should be a separate registrable domain (for example
  `example-sites.com`), or be listed in the Public Suffix List, on any
  instance where people outside your organization can publish. Under the
  app's registrable domain (`sites.example.com` next to `dash.example.com`)
  site content is same-site with the app: its scripts can set cookies on
  `example.com` to fill the browser's per-domain cookie jar (evicting the app
  session) or make every app request's `Cookie` header too large for the
  ingress, signing visitors out or locking them out of the app until they
  clear cookies. `__Host-` cookies, Origin checks, and CSRF tokens keep this
  to denial of service.
