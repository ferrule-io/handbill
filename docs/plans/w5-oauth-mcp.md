# W5 plan — OAuth 2.1 authorization server, `/mcp`, site and content tools

Implementation plan for workstream W5 ([workplan](../workplan.md#w5-oauth-21-authorization-server-and-mcp),
[roadmap step 5](../roadmap.md#5-oauth-21-and-mcp)). It is written against
`development` after W3 (accounts, sessions, `appapi`) and W4 (sites service,
file server) merged. Every design choice is made here; the implementer should
not need to decide anything. Where this plan contradicts or extends
`docs/design.md` / `docs/workplan.md`, [section 2](#2-corrections-to-designmd-and-workplanmd)
lists the change and the implementer applies it to those docs in the same
branch.

## 0. Decisions at a glance

| Topic | Decision |
|---|---|
| Spec baseline | MCP authorization **2025-11-25**, plus the server-side deltas of **2026-07-28** (RFC 9207 `iss`, refresh-token guidance, DCR deprecated). CIMD per **draft-ietf-oauth-client-id-metadata-document-03**. OAuth 2.1 per **draft-ietf-oauth-v2-1-17**. |
| Clients | Public clients only (`token_endpoint_auth_method` `none`). CIMD always on; DCR behind `OAUTH_DCR_ENABLED`. No `private_key_jwt`, no client secrets. |
| Pending authorization | New table `oauth_authorization_requests` (hashed handle, bound to the signed-in user, 10 min, consumed by `DELETE … RETURNING`). |
| Tokens | Opaque `token.New()` values, SHA-256 at rest, checked against the DB on every request. Code 2 min, access 1 h, refresh 30 days sliding, rotated on every use, reuse revokes the family. |
| Scopes | None. `scope` is accepted and ignored except `offline_access` (recorded, no effect). No `scopes_supported` anywhere; no `scope` in `WWW-Authenticate`. |
| Audience | Exactly one resource: `APP_ORIGIN + "/mcp"`. Missing `resource` defaults to it; any other value is `invalid_target`. |
| Consent | Always shown (no silent re-approval). Shows client name, `client_id` host, redirect host, loopback and unverified-client warnings. |
| `/mcp` | Official Go SDK `v1.8.0`, `StreamableHTTPHandler` with `Stateless: true`, `JSONResponse: true`, `DisableLocalhostProtection: true`; our own `Origin` check and `oauth.RequireBearer` in front. |
| Revocation | `POST /oauth/revoke` (RFC 7009) — Claude Code's `claude mcp logout` uses it. |
| Archive upload | `PUT /api/deployments/{id}/archive?grant=<signed>`, 15 min grant, format sniffed (gzip/zip magic), W4's limits. |

## 1. Sources read

Primary sources, read for this plan on 2026-11-13:

- MCP authorization, 2025-11-25: <https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization>
- MCP 2026-07-28 changelog: <https://modelcontextprotocol.io/specification/2026-07-28/changelog>, and the
  split auth pages [index](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/index),
  [AS discovery](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/authorization-server-discovery),
  [client registration](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/client-registration),
  [security considerations](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/security-considerations),
  [Streamable HTTP](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http)
- CIMD: <https://datatracker.ietf.org/doc/html/draft-ietf-oauth-client-id-metadata-document-03> (the MCP spec still links `-00`)
- OAuth 2.1: <https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-17> (§4.1.2.1 errors, §4.1.3, §4.3.1 refresh rotation, §7.5.3, §8.4.2 loopback)
- [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) protected resource metadata (§3 well-known path insertion, §3.3 `resource` match, §5.1 `WWW-Authenticate`)
- [RFC 8414](https://www.rfc-editor.org/rfc/rfc8414) AS metadata; [RFC 8707](https://www.rfc-editor.org/rfc/rfc8707) resource indicators (`invalid_target`);
  [RFC 9207](https://www.rfc-editor.org/rfc/rfc9207) `iss`; [RFC 7591](https://www.rfc-editor.org/rfc/rfc7591) DCR;
  [RFC 7009](https://www.rfc-editor.org/rfc/rfc7009) revocation; [RFC 6750](https://www.rfc-editor.org/rfc/rfc6750) §3 bearer errors;
  [RFC 7636](https://www.rfc-editor.org/rfc/rfc7636) PKCE; [RFC 8252](https://www.rfc-editor.org/rfc/rfc8252) §7.3 / [RFC 9700](https://www.rfc-editor.org/rfc/rfc9700) §4.1.3 loopback ports
- Go SDK `github.com/modelcontextprotocol/go-sdk@v1.8.0` (latest, 2026-09-04), read from the module cache:
  `mcp/streamable.go`, `mcp/server.go`, `mcp/tool.go`, `auth/auth.go`, `auth/authorization_code.go`, `oauthex/`
  (<https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.8.0>)
- Real client metadata documents, fetched 2026-11-13:
  - Claude Code `https://claude.ai/oauth/claude-code-client-metadata`:
    `redirect_uris: ["http://localhost/callback", "http://127.0.0.1/callback"]`, `grant_types: ["authorization_code","refresh_token"]`,
    `token_endpoint_auth_method: "none"`, `Cache-Control: public, max-age=300`, 350 bytes.
  - Claude (claude.ai / Desktop connectors) `https://claude.ai/oauth/mcp-oauth-client-metadata`:
    `redirect_uris: ["https://claude.ai/api/mcp/auth_callback"]`, `grant_types` include `urn:ietf:params:oauth:grant-type:jwt-bearer`.
  - VS Code `https://vscode.dev/oauth/client-metadata.json`: `http://127.0.0.1:33418/`, `application_type: "native"`, 477 bytes.
- Claude Code MCP docs <https://code.claude.com/docs/en/mcp> (token endpoint must be HTTPS or `localhost`/`127.0.0.1`/`::1`;
  rejects unexpected `iss`; `claude mcp logout` revokes where supported) and Claude Help Center
  [custom connectors](https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp)
  (connectors, including Claude Desktop's, connect from Anthropic's cloud; the server must be publicly reachable).

What 2026-07-28 changes server-side (everything else in it is client- or protocol-level and handled by the SDK):

1. Authorization servers SHOULD send `iss` in every authorization response (success and error) and, if they do,
   MUST advertise `authorization_response_iss_parameter_supported: true`. CIMD-03 §7 makes `iss` a MUST for CIMD
   servers. → We always send it. (The Go SDK client rejects an `iss` that is not advertised.)
2. Refresh tokens: servers MUST rotate for public clients (unchanged); protected resources SHOULD NOT list
   `offline_access` in PRM or `WWW-Authenticate`. → We list no scopes at all.
3. DCR is deprecated in favor of CIMD. → Consistent with our default-off flag; keep it.
4. Stateless core: no `initialize` handshake, no `Mcp-Session-Id`, `server/discover`, `Mcp-Method`/`Mcp-Name` headers.
   → The SDK v1.8.0 stateless handler speaks both 2025-11-25 and 2026-07-28; nothing to build.
5. Streamable HTTP: servers MUST validate `Origin` and answer 403 for an invalid one (unchanged from 2025-11-25).

## 2. Corrections to design.md and workplan.md

Apply these doc edits in the W5 branch.

1. **design.md "MCP authorization"** cites only MCP 2025-11-25. Add: "plus the 2026-07-28 server
   requirements: `iss` on authorization responses (RFC 9207) with
   `authorization_response_iss_parameter_supported: true`; CIMD per draft-03." Note DCR is deprecated by
   2026-07-28 and stays off by default.
2. **design.md "CIMD fetching … no redirects to disallowed hosts"**: CIMD-03 §6 says the AS MUST NOT follow
   redirects at all, and only `200` is a document. Replace with "no redirects (any 3xx is an error)". `safehttp`
   already refuses every redirect.
3. **design.md consent screen** ("shows the client name and the `client_id` host"): the MCP spec also says the
   AS MUST show the redirect URI hostname and SHOULD warn for `localhost` redirect URIs. Add both, plus the
   DCR "unverified" label already there.
4. **design.md CIMD redirect URIs**: the DCR rule ("only `https` or loopback `http`") applies to CIMD too
   (MCP: "All redirect URIs MUST be either `localhost` or use HTTPS"). State it once for both, and state the
   loopback port rule (any port, RFC 8252 §7.3), which Claude Code needs: its document lists
   `http://localhost/callback` with no port.
5. **design.md route table**: add `/consent?request=<id>` (consent page),
   `/.well-known/oauth-protected-resource/mcp` (path-suffixed PRM, the one `WWW-Authenticate` points to),
   and `/oauth/revoke`. Root `/.well-known/oauth-protected-resource` stays and serves the same document.
   RFC 9728 §3.3 says root metadata should describe resource `APP_ORIGIN`. The MCP spec explicitly allows the
   root document to describe the `/mcp` server. Clients that check strictly find the path-suffixed document
   first, so they never fetch the root.
6. **design.md tokens table**: add "OAuth authorization request handles" to the lookup-token row (32 random
   bytes, SHA-256 at rest, consumed atomically).
7. **design.md MCP tools**: `publish_site` lists `notify`, but roadmap step 6 assigns `notify` to W6. W5 ships
   `publish_site` without `notify`. W6 adds it. Its schema rejects unknown fields until then.
8. **design.md DCR "rate-limited"**: rate limiting is W9 (`internal/ratelimit`). W5 ships DCR unrate-limited
   behind the default-off flag. Add a W9 line: rate-limit `/oauth/register`, `/oauth/authorize` (it triggers
   outbound CIMD fetches), `/oauth/token`. GC DCR clients that have no grants, plus expired codes, tokens, and
   requests.
9. **workplan W5 `Principal{Scopes}`**: we have no scopes. Keep the field (it carries granted scopes, today
   at most `offline_access`) and add `Email`, `EmailVerified`, `ClientName`, `GrantID`.
   `mcp.Env` gains `Signer *token.Signer`.
10. **workplan W4 `sites`** has no way to read a deployment's site or creator. W5 adds the additive
    `sites.Deployment` type and `(*Service).GetDeployment` (see [§9.1](#91-additive-change-in-internalsites)). This edits
    a done workstream's package, so the W5 implementer tells Main and keeps the change in its own commit.
11. **`internal/safehttp`** treats `304` as a redirect (`ErrRedirect`), so conditional CIMD revalidation is
    impossible. W5 changes it so `304 Not Modified` is returned as a normal `Response` (W5 is its only consumer).
12. **Dev environment**: Claude Code refuses token endpoints that are plain `http://` unless the host is
    literally `localhost`, `127.0.0.1`, or `::1`. mise's `APP_ORIGIN=http://dash.localhost:8080` therefore
    cannot be used to connect Claude Code. Manual tests override `APP_ORIGIN=http://localhost:8080`
    ([§11](#11-manual-verification-with-real-clients)). The mise default stays as it is, because W3's passkey
    RP ID and the e2e suite depend on it.
13. **W8/Helm ingress**: `publish_site` bodies (up to 16 MiB) and archive uploads (up to 512 MiB) exceed the
    common ingress default (ingress-nginx: 1 MiB). Add a W8 note: set the body-size annotation (e.g.
    `nginx.ingress.kubernetes.io/proxy-body-size: 600m`) on the app host.
14. **SDK localhost protection**: the SDK's automatic DNS-rebinding check returns 403 whenever the listener
    is on loopback and `Host` is not literally `localhost`/loopback IP. That breaks `dash.localhost`, every
    local tunnel (`cloudflared`, `ngrok`), and loopback sidecars. We disable it and enforce our own checks:
    `server` routes only `APP_ORIGIN`'s host to the app mux, and `/mcp` checks `Origin`.

## 3. Data model

### 3.1 New migration

`internal/db/migrations/<goose timestamp>_oauth_requests.sql` (create with `goose create oauth_requests sql`):

```sql
-- Pending OAuth authorization requests (docs/plans/w5-oauth-mcp.md): /oauth/authorize
-- validates the request, stores it here bound to the signed-in user, and sends the
-- browser to /consent?request=<token>. The decision consumes the row
-- (DELETE ... RETURNING). The plaintext handle is a token.New() value.

-- +goose Up
CREATE TABLE oauth_authorization_requests (
    id             uuid PRIMARY KEY,
    token_hash     bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id      uuid NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    redirect_uri   text NOT NULL,
    state          text,
    code_challenge text NOT NULL,
    resource       text NOT NULL,
    scopes         text[] NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL
);
CREATE INDEX oauth_authorization_requests_user_id ON oauth_authorization_requests (user_id);
CREATE INDEX oauth_authorization_requests_expires_at ON oauth_authorization_requests (expires_at);

-- +goose Down
DROP TABLE oauth_authorization_requests;
```

Why a DB row and not signed state:

- It is single-use and revocable. A signed blob can be replayed until it expires.
- It binds the request to the user who signed in. A consent link opened in another account's session is a 404.
- `/consent?request=` stays short, and the authorize parameters never leave the server.
- `internal/token` (W2a, done) needs no new signing purpose.
- It matches W3's `auth_flows` pattern.

Every write to this table first deletes the user's expired rows
(`DELETE … WHERE user_id = $1 AND expires_at < now()`). Global GC is W9.

Existing W2a tables are used as designed:

- `oauth_clients` holds CIMD and DCR clients.
- `oauth_grants` has one row per (user, client).
- `oauth_authorization_codes`, `oauth_access_tokens`, and `oauth_refresh_tokens` share a family:
  `family_id` = the code's `id`.

### 3.2 Queries

`internal/db/queries/oauth.sql` (sqlc; regenerate with `go generate ./internal/db`). Names and the
non-obvious SQL:

| Query | Notes |
|---|---|
| `GetClientByClientID :one` | `WHERE client_id = $1` |
| `UpsertCIMDClient :one` | `INSERT … ON CONFLICT (client_id) DO UPDATE SET metadata, client_name, redirect_uris, token_endpoint_auth_method, fetched_at, cache_expires_at, etag, last_modified, updated_at = now() WHERE oauth_clients.registration = 'cimd' RETURNING *` |
| `RefreshCIMDClientCache :exec` | 304 path: `fetched_at`, `cache_expires_at`, `etag`, `last_modified` |
| `CreateDCRClient :one` | `registration = 'dcr'`, `client_id = id::text` |
| `DeleteClientGrants :execrows` | `DELETE FROM oauth_grants WHERE client_id = $1` (CIMD `client_id` mismatch) |
| `DeleteExpiredAuthorizationRequests :exec` | per user |
| `CreateAuthorizationRequest :one` | |
| `GetAuthorizationRequest :one` | `WHERE token_hash = $1 AND user_id = $2 AND expires_at > now()`, joined with the client's `client_id`, `client_name`, `registration`, `redirect_uris` |
| `TakeAuthorizationRequest :one` | `DELETE … WHERE token_hash = $1 AND user_id = $2 AND expires_at > now() RETURNING *` |
| `GrantExists :one` | `(user_id, client_id)` |
| `UpsertGrant :one` | `INSERT … ON CONFLICT (user_id, client_id) DO UPDATE SET scopes = EXCLUDED.scopes, updated_at = now() RETURNING *` |
| `CreateAuthorizationCode :one` | |
| `ConsumeAuthorizationCode :one` | `UPDATE oauth_authorization_codes SET consumed_at = now() WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now() RETURNING *` |
| `GetAuthorizationCodeByHash :one` | reuse detection after a failed consume |
| `GetGrantWithClient :one` | grant + client row by grant id |
| `CreateAccessToken :one`, `CreateRefreshToken :one` | |
| `GetRefreshTokenForUpdate :one` | `WHERE token_hash = $1 FOR UPDATE` |
| `MarkRefreshTokenRotated :exec` | `SET rotated_at = now()` |
| `RevokeAccessTokenFamily :exec`, `RevokeRefreshTokenFamily :exec` | `SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL` |
| `RevokeAccessTokenByHash :execrows` | `… FROM oauth_grants g WHERE t.grant_id = g.id AND g.client_id = $2 AND t.token_hash = $1` |
| `GetRefreshTokenByHashForClient :one` | revocation endpoint |
| `GetAccessTokenPrincipal :one` | see below |
| `TouchGrant :exec` | `SET last_used_at = now() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '5 minutes')` |
| `ListGrantsForUser :many` | joined with clients; `ORDER BY last_used_at DESC NULLS LAST, created_at DESC` |
| `DeleteGrant :execrows` | `WHERE id = $1 AND user_id = $2` |

```sql
-- name: GetAccessTokenPrincipal :one
SELECT t.id, t.grant_id, t.family_id, t.scopes, t.expires_at,
       g.user_id, c.client_id AS client_key, c.client_name,
       e.email, coalesce(e.verified, false) AS email_verified
FROM oauth_access_tokens t
JOIN oauth_grants g ON g.id = t.grant_id
JOIN oauth_clients c ON c.id = g.client_id
LEFT JOIN emails e ON e.user_id = g.user_id AND e.is_primary
WHERE t.token_hash = @token_hash AND t.resource = @resource
  AND t.revoked_at IS NULL AND t.expires_at > now();
```

`internal/db/queries/mcp.sql` (read-only, for `get_site`):

```sql
-- name: GetSiteSharingSummary :one
SELECT (SELECT count(*) FROM memberships m WHERE m.site_id = @site_id)::int AS member_count,
       coalesce((SELECT p.enabled FROM public_links p WHERE p.site_id = @site_id), false) AS public_link_enabled;
```

## 4. OAuth endpoints

All endpoints are on the app host and registered by `internal/oauth`. Values below use
`APP_ORIGIN = https://dash.example.com`. Define once in `internal/oauth/resource.go`:

```go
func Issuer(c *config.Config) string              { return c.AppOrigin.String() }        // "https://dash.example.com"
func Resource(c *config.Config) string            { return Issuer(c) + "/mcp" }
func ResourceMetadataURL(c *config.Config) string { return Issuer(c) + "/.well-known/oauth-protected-resource/mcp" }
```

Common rules:

- Responses carrying codes, tokens, or request handles set `Cache-Control: no-store`.
- Every JSON response sets `Content-Type: application/json`.
- No endpoint sends CORS headers. Browser-based MCP clients are out of scope, and the design forbids
  credentialed CORS except for `APP_ORIGIN`.
- None of these endpoints ever redirects except `/oauth/authorize`. Claude Desktop does not follow redirects
  from an MCP server or its AS.
- None reads the session cookie except `/oauth/authorize` and the `/app-api/oauth/*` endpoints.
  `/oauth/token`, `/oauth/revoke`, and `/oauth/register` are outside `appapi` and so outside its CSRF
  checks. They authenticate by PKCE, `client_id`, and token possession.
- The server never logs query strings (`server.logRequests`), so `state`, `code`, `request`, and `grant`
  in URLs are safe. Never log request bodies or token values.

### 4.1 `GET /.well-known/oauth-protected-resource/mcp` and `GET /.well-known/oauth-protected-resource`

Both serve the same document, `200`, `Cache-Control: public, max-age=3600`:

```json
{
  "resource": "https://dash.example.com/mcp",
  "authorization_servers": ["https://dash.example.com"],
  "bearer_methods_supported": ["header"],
  "resource_name": "agent-dash"
}
```

No `scopes_supported`. `resource` must byte-equal the MCP URL the client connected to. The Go SDK client
compares it to the request URL (`oauthex.GetProtectedResourceMetadata`). Build it with `Resource(cfg)`
and never hand-write it.

### 4.2 `GET /.well-known/oauth-authorization-server`

`200`, `Cache-Control: public, max-age=3600`:

```json
{
  "issuer": "https://dash.example.com",
  "authorization_endpoint": "https://dash.example.com/oauth/authorize",
  "token_endpoint": "https://dash.example.com/oauth/token",
  "revocation_endpoint": "https://dash.example.com/oauth/revoke",
  "response_types_supported": ["code"],
  "response_modes_supported": ["query"],
  "grant_types_supported": ["authorization_code", "refresh_token"],
  "token_endpoint_auth_methods_supported": ["none"],
  "revocation_endpoint_auth_methods_supported": ["none"],
  "code_challenge_methods_supported": ["S256"],
  "client_id_metadata_document_supported": true,
  "authorization_response_iss_parameter_supported": true
}
```

With `OAUTH_DCR_ENABLED=true`, add `"registration_endpoint": "https://dash.example.com/oauth/register"`.

Use our own struct with `omitempty` only on `registration_endpoint`. The SDK's `oauthex.AuthServerMeta`
always emits `jwks_uri` and so cannot be reused. We do not serve `/.well-known/openid-configuration`: we are
not an OpenID provider, and MCP clients try the RFC 8414 URL first.

### 4.3 `GET /oauth/authorize`

Only GET (OAuth 2.1 MUST). Each step either passes or produces exactly one outcome.

**Phase A — errors that must not redirect** (OAuth 2.1 §4.1.2.1). These answer
`303 See Other` to `/consent?error=<code>`, which renders a fixed message. The page never shows text from
the request.

| # | Check | `error` code |
|---|---|---|
| A1 | `client_id` present exactly once, ≤ 2048 bytes | `invalid_client` |
| A2 | Resolve the client: `https://…` → CIMD ([§5](#5-cimd-handling)). Otherwise, with DCR enabled, a `dcr` row with that `client_id`; else fail | `invalid_client` |
| A3 | CIMD fetch or validation failed terminally | `client_metadata_invalid` |
| A4 | CIMD fetch failed transiently and no cached row exists | `client_metadata_unavailable` |
| A5 | `redirect_uri` present exactly once and ≤ 2048 bytes. It matches a registered URI ([§5.5](#55-redirect-uri-rules-and-loopback-matching)) | `invalid_redirect_uri` |

Every redirect URI is required, even when the client registered just one. MCP clients always send one,
and loopback port matching needs it.

**Phase B — errors that redirect.** These answer `302 Found` to the redirect URI. Keep its existing query
and add `error`, `error_description` (fixed ASCII strings), `state` (if one was sent and valid), and
`iss=<Issuer>`.

| # | Check | `error` |
|---|---|---|
| B1 | Any of `response_type`, `code_challenge`, `code_challenge_method`, `resource`, `scope`, `state`, `response_mode` appears more than once | `invalid_request` |
| B2 | `state` ≤ 1024 bytes. If longer: `invalid_request`, and `state` is not echoed | `invalid_request` |
| B3 | `request` or `request_uri` present (JAR/PAR unsupported) | `request_not_supported` |
| B4 | `response_type == "code"` | `unsupported_response_type` |
| B5 | `response_mode` absent or `query` | `invalid_request` |
| B6 | `code_challenge_method == "S256"`. Absent means `plain` (RFC 7636), so reject it. | `invalid_request` ("code_challenge_method must be S256") |
| B7 | `code_challenge` matches `^[A-Za-z0-9_-]{43}$` | `invalid_request` |
| B8 | `resource` absent, or equal to `Resource(cfg)` after lowercasing scheme and host | `invalid_target` |
| B9 | DCR client while `OAUTH_DCR_ENABLED=false` | `unauthorized_client` |
| B10 | Client's effective `grant_types` include `authorization_code` (checked at fetch for CIMD, at registration for DCR; re-checked here) | `unauthorized_client` |

`scope`: split on spaces. Keep `offline_access` if present and drop everything else. Storing an empty list
is fine. `prompt`, `login_hint`, `nonce`, and unknown parameters are ignored.

**Phase C — the user.** If `identity.FromContext` has no user (signed out, or a restricted recovery
session), answer `auth.RedirectToLogin(w, r)`. That is a `303` to `/login?return=/oauth/authorize?<original
query>`. The login page does a full load of `return` after sign-in, so authorize runs again with the CIMD
row now cached.

**Phase D — create the request.** In one transaction: `DeleteExpiredAuthorizationRequests(user)`, then
`CreateAuthorizationRequest` with `token.New()`, `expires_at = now + 10 min`, the exact `redirect_uri` string
as sent, `state`, `code_challenge`, `resource = Resource(cfg)`, and the kept scopes. Answer `303` to
`/consent?request=<plaintext>` with `Referrer-Policy: no-referrer` and `Cache-Control: no-store`.

Failures inside Phase D are `server_error` redirects.

### 4.4 Consent page `/consent` (web) and `/app-api/oauth/consent`

**Page** `web/app/consent/page.tsx`: a fixed route, `AuthShell` layout, `useSearchParams` inside
`Suspense`. The page is outside `(app)`, so it handles sign-in itself.

- `?error=<code>` shows an error card with a fixed message per code (`invalid_client`,
  `client_metadata_invalid`, `client_metadata_unavailable`, `invalid_redirect_uri`, `unknown`):
  "This connection request can't continue. Go back to your app and try connecting again."
  `client_metadata_unavailable` adds "The app's registration couldn't be fetched; try again in a minute."
- `?request=<t>` calls `GET /app-api/oauth/consent?request=<t>`:
  - `401` → `window.location.assign(loginHref("/consent?request=" + t))`.
  - `404` → "This request expired or belongs to another account. Start the connection again from your app."
  - `200` → the consent card.

**Consent card** (`components/consent/consent-card.tsx`):

- Title: "**{client name}** wants to publish sites as **{email}**". If the name is empty, use the client host.
- Under the title, in monospace: the client host (`client_id` host for CIMD, e.g. `claude.ai`). Below it:
  "Sends you back to: {redirect host}{:port}".
- "It will be able to" list: "create sites you own; publish, update, and roll back sites you own or
  manage; read their files; change titles and descriptions; delete sites you own."
- Warnings (`Alert`, shown when they apply):
  - DCR client: "**Unverified app.** This app registered itself; its name was not checked. Only continue if
    you started this connection yourself."
  - Loopback redirect: "This connection hands access to a program on this computer ({host}:{port}). Only
    continue if you just started connecting {name} here."
  - Non-loopback redirect host ≠ client host: "After you approve, you'll be sent to **{redirect host}**."
- If `previouslyAuthorized`, add the line: "You've connected {name} before; approving again adds a new connection."
- Buttons: **Allow** (primary) and **Cancel**. Both `POST /app-api/oauth/consent`, then
  `window.location.assign(redirectTo)` and show "Returning you to {name}…". With a loopback redirect also show
  "If nothing happens, you can close this tab."

**`GET /app-api/oauth/consent?request=<t>`**: `appapi.Handle`, session required.
`GetAuthorizationRequest(hash(t), user)`. The grant check uses `GrantExists`.

```json
{
  "request": {
    "client": {"clientId": "https://claude.ai/oauth/claude-code-client-metadata", "name": "Claude Code",
               "host": "claude.ai", "verified": true},
    "redirect": {"uri": "http://localhost:54321/callback", "host": "localhost:54321", "loopback": true},
    "account": {"email": "me@example.com"},
    "previouslyAuthorized": false,
    "expiresAt": "2026-11-13T21:40:00Z"
  }
}
```

`verified` = `registration == "cimd"`. For DCR, `host` is the first redirect URI's host and `clientId` is
the opaque id. `404 {"error":{"code":"request_not_found"}}` when the row is missing, expired, or another
user's.

**`POST /app-api/oauth/consent`** `{"request": "<t>", "approve": true|false}`: `appapi.Handle` enforces
session, `Origin == APP_ORIGIN`, and `X-CSRF-Token`. In one transaction:

1. `TakeAuthorizationRequest(hash, user)`. Missing → `404 request_not_found` (this also covers a double
   submit).
2. Deny → `redirectTo = redirect_uri + error=access_denied&state&iss`.
3. Approve → `UpsertGrant(user, client, scopes)`, then `CreateAuthorizationCode(token.New(), grant,
   redirect_uri, code_challenge, 'S256', resource, scopes, expires = now + 2 min)`. The code's `id` (uuid v7)
   becomes the token family id. `redirectTo = redirect_uri + code&state&iss`.

Response `200 {"redirectTo": "…"}` with `Cache-Control: no-store`. Build URLs with `url.Parse` and set query
values on the existing query. Never concatenate strings.

### 4.5 `POST /oauth/token`

Content type must be `application/x-www-form-urlencoded` (parameters are ignored). The body is capped at
16 KiB (`http.MaxBytesReader`). A parameter that appears more than once → `invalid_request`. Errors are
`{"error": …, "error_description": …}` with status 400, except `invalid_client`, which is 401 (plus
`WWW-Authenticate: Basic realm="agent-dash"` when the request carried an `Authorization` header).

**Step 0, before touching any code or token: client authentication.** All our clients use `none`. If the
request carries `Authorization`, `client_secret`, `client_assertion`, or `client_assertion_type`, answer
`invalid_client` (CIMD-03 §5 MUST). Doing this first matters. With `AuthStyleAutoDetect`, Go's
`x/oauth2` (the MCP Go SDK's client) first sends `Authorization: Basic base64(client_id:)` and retries with
the form after an error. That retry must still find the code unconsumed.

Then `client_id` is required. Look it up with `GetClientByClientID`; unknown → `invalid_client`. A DCR
client while DCR is disabled → `invalid_client`. The token endpoint never refetches CIMD documents: it
needs only the stored client row.

**`grant_type=authorization_code`** (`code`, `redirect_uri`, `code_verifier`, optional `resource`):

1. `code_verifier` matches `^[A-Za-z0-9._~-]{43,128}$` → else `invalid_request`.
2. `ConsumeAuthorizationCode(hash(code))`. No row → `GetAuthorizationCodeByHash`. If a consumed code
   exists, run `RevokeAccessTokenFamily(code.id)` and `RevokeRefreshTokenFamily(code.id)`, log
   `oauth code reuse` with the grant id, and answer `invalid_grant`. Otherwise (unknown or expired),
   `invalid_grant`.
3. Load the grant and client. `client.client_id != client_id` → `invalid_grant`.
4. `redirect_uri` must byte-equal the stored one, including the port the client used → else `invalid_grant`.
5. `base64url_nopad(sha256(code_verifier)) == code_challenge` (constant-time compare) → else `invalid_grant`.
6. `resource` present and `!= stored resource` → `invalid_target`.
7. Issue the pair (below) with `family_id = code.id`, then `TouchGrant`.

**`grant_type=refresh_token`** (`refresh_token`, optional `resource`, optional `scope`). One transaction:

1. `GetRefreshTokenForUpdate(hash)`. Missing → `invalid_grant`.
2. `revoked_at` set → `invalid_grant`.
3. `rotated_at` set → reuse. Revoke the family, log `oauth refresh reuse`, `invalid_grant`. There is no
   grace window: a client that double-submits loses the family and must re-authorize. This is the
   OAuth 2.1 §4.3.1 / RFC 9700 behavior.
4. `expires_at <= now()` → `invalid_grant`.
5. The grant's client `client_id != client_id` → `invalid_grant`.
6. The client's effective `grant_types` lack `refresh_token` → `unauthorized_client`.
7. `resource` present and `!=` the token's → `invalid_target`. `scope` present with values outside the
   token's scopes → `invalid_scope`.
8. `MarkRefreshTokenRotated`, then issue the pair with the same `family_id` and `parent_id = old.id`, then
   `TouchGrant`.

Any other `grant_type` → `unsupported_grant_type`.

**Issuing a pair**:

- An access token (`token.New()`, 1 h, `resource`, `scopes`).
- A refresh token (`token.New()`, 30 days from now, same family), unless the client's `grant_types`
  exclude `refresh_token`. Effective `grant_types`: as declared; if not declared,
  `["authorization_code","refresh_token"]`. We deliberately don't apply RFC 7591's
  `authorization_code`-only default, because MCP clients rarely declare `grant_types` and need refresh tokens.

The response is `200`, `Cache-Control: no-store`, `Pragma: no-cache`:

```json
{"access_token": "…", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "…"}
```

Add `"scope": "offline_access"` only when the granted scopes are non-empty.

### 4.6 `POST /oauth/revoke` (RFC 7009)

Form-encoded, 16 KiB cap. Parameters: `token`, optional `token_type_hint`, and required `client_id` (public
clients identify themselves). Client-auth parameters get the same `invalid_client` rule as the token endpoint.

Look the token up in the hinted table first, then the other:

- Refresh token of this client → revoke its whole family.
- Access token of this client → `RevokeAccessTokenByHash`.

Unknown tokens, other clients' tokens, and already revoked ones all return `200` with an empty body.
Unknown `client_id` → `invalid_client` (401). Revocation never deletes the grant; the settings page does that.

### 4.7 `POST /oauth/register` (RFC 7591), only with `OAUTH_DCR_ENABLED=true`

Always register the route. When the flag is off, answer `404` with
`{"error":"invalid_request","error_description":"dynamic client registration is disabled"}`. That is
clearer than the mux's 405 and keeps one code path.

The request is `application/json`, ≤ 16 KiB, a JSON object. Unknown members are kept in `metadata` but
otherwise ignored. Validation (`400 {"error": "invalid_redirect_uri" | "invalid_client_metadata", "error_description": …}`):

- `redirect_uris`: 1–20 strings, each passing [§5.5](#55-redirect-uri-rules-and-loopback-matching) → else
  `invalid_redirect_uri`.
- `token_endpoint_auth_method`: absent or `"none"` → else `invalid_client_metadata` ("only public clients
  (none) are supported").
- `grant_types`: absent, or containing `authorization_code` and only values from
  `{authorization_code, refresh_token}`.
- `response_types`: absent or exactly `["code"]`.
- `client_name`: optional string. Trim it, drop control characters, cap at 100 runes. `client_secret` is
  never issued.

`CreateDCRClient` with `client_id = <new uuid string>`. Response `201`, `Cache-Control: no-store`:

```json
{"client_id": "0193…", "client_id_issued_at": 1794604876, "client_name": "…",
 "redirect_uris": ["http://127.0.0.1/callback"], "grant_types": ["authorization_code","refresh_token"],
 "response_types": ["code"], "token_endpoint_auth_method": "none"}
```

### 4.8 Grants in settings: `/app-api/oauth/grants`

- `GET /app-api/oauth/grants` returns
  `{"grants":[{"id","client":{"clientId","name","host","verified"},"createdAt","lastUsedAt"}]}`
  (`lastUsedAt` null when never used).
- `DELETE /app-api/oauth/grants/{id}` → `DeleteGrant(id, user)`. `0` rows → `404 not_found`, else `204`.
  Codes and tokens cascade, so the next `/mcp` call and the next refresh fail immediately.

`components/settings/oauth-grants.tsx` replaces the "Connected agents" placeholder `Section` in W3's
`components/settings/security-settings.tsx`. That one-line mount is the only edit to W3's file.

- Rows: plug icon, client name with an "Unverified" `Badge` for DCR clients, host in monospace,
  "Connected {date} · Last used {relative}", and a **Disconnect** button with an `AlertDialog` ("{name} will
  lose access immediately…").
- Empty state: the placeholder's current copy.

## 5. CIMD handling

### 5.1 Client identifier URL rules

A `client_id` beginning with `https://` is a CIMD URL. Reject (`invalid_client`) unless all of these hold
(CIMD-03 §3, §10.1):

- `url.Parse` succeeds, and `u.String()` byte-equals the input. We never normalize; the client must send
  its canonical form.
- Scheme is exactly `https`. No userinfo. No fragment (and no `#` in the raw string). No query (the draft
  says SHOULD NOT; we refuse queries, which also keeps cache keys simple).
- The host is non-empty. A port is allowed.
- The path is not empty, not `/`, and has no `.` or `..` segment.
- The origin (scheme://host[:port]) is not `APP_ORIGIN`. The host is not `SITES_DOMAIN` or under it
  (`*.SITES_DOMAIN` serves user content).
- ≤ 2048 bytes.

IP-literal hosts are not refused here; `safehttp` refuses special-use addresses at dial time.

### 5.2 Fetch

One `*safehttp.Client` per module: `safehttp.New(safehttp.Options{Timeout: 5 * time.Second,
MaxBodyBytes: 5 << 10, UserAgent: "agent-dash (+" + APP_ORIGIN + ")"})`. The 5 KiB cap is CIMD-03
§10.11's recommendation. Real documents are 350–500 bytes.

Request headers: `Accept: application/json`, plus `If-None-Match` and `If-Modified-Since` when
revalidating a stored row.

| Outcome | Class |
|---|---|
| `200` with `Content-Type` `application/json` or `application/*+json` (parameters ignored), valid document ([§5.3](#53-document-validation)) | success |
| `304` (after the safehttp change) | success: refresh cache fields from the new headers, keep the document |
| `safehttp.ErrRedirect` (any 3xx other than 304), `ErrBlockedAddress`, `ErrScheme`, `ErrTooLarge`, wrong content type, any 4xx except 408/429, invalid document | terminal |
| network error, timeout, TLS error, 408, 429, 5xx | transient |

Errors are never cached (CIMD-03 §6.2).

- On a terminal failure the stored row stays unchanged. If the failure was a `client_id` mismatch, also
  `DeleteClientGrants(row.id)` (§6.1 SHOULD invalidate refresh tokens).
- On a transient failure with a stored row, use the stored row (§6.1 SHOULD NOT discard) and log a warning.

**Change to `internal/safehttp`**: in `Get`, exclude 304 from the 3xx-is-redirect check. The test asserts
that a 304 returns `StatusCode: 304` and an empty body.

When to fetch, in `/oauth/authorize` only:

- No row → fetch.
- Row with `cache_expires_at > now()` → use it.
- Otherwise revalidate.

Concurrent authorizations may both fetch; `UpsertCIMDClient` makes that harmless.

### 5.3 Document validation

Parse into `map[string]json.RawMessage` (keep the raw bytes for `metadata`), then:

1. A JSON object whose `client_id` string byte-equals the fetched URL (§4 MUST).
2. `client_name`: a non-empty string after trimming (MCP requires it). Display value: control characters
   removed, capped at 100 runes. Stored in `client_name`.
3. `redirect_uris`: an array of strings with at most 20 entries. Entries that fail [§5.5](#55-redirect-uri-rules-and-loopback-matching)
   are dropped (e.g. private-use schemes for other ASes). At least one must remain. Stored in `redirect_uris`.
4. `client_secret` or `client_secret_expires_at` present → invalid (§4.1).
5. `jwks` and `jwks_uri` both present → invalid. Keys are otherwise ignored: we support no key-based auth.
6. Auth method:
   - `methods` = `token_endpoint_auth_methods_supported` if present (array of strings). Else
     `[token_endpoint_auth_method]` if present. Else `["none"]` (§5 MUST NOT apply RFC 7591's default).
   - Both present with the singular not in the array → invalid.
   - Any of `client_secret_basic`, `client_secret_post`, or `client_secret_jwt` in `methods` → invalid (§4.1).
   - `"none"` not in `methods` → `invalid_client` (we support only `none`). This includes
     `private_key_jwt`-only clients.
   - Store `token_endpoint_auth_method = 'none'`.
7. `grant_types`, if present: an array of strings. Its intersection with
   `{authorization_code, refresh_token}` must contain `authorization_code` (§6 MUST intersect).
   Other values are fine, e.g. claude.ai's `jwt-bearer`.
8. `response_types`, if present: must contain `code`.
9. `client_uri`, `logo_uri`, `policy_uri`, and `tos_uri` are stored but never displayed or fetched.
   §10.9 requires prefetch-and-proxy for logos; we show no logos.

### 5.4 Caching

From the 200/304 response's `Cache-Control`. `Expires` is ignored, which §6.2 permits.

- `no-store`, `no-cache`, or `max-age=0` → `cache_expires_at = now()`: the next authorize revalidates.
  The row is still stored, because it is the client's identity record for grants.
- `max-age=N` → `now + clamp(N, 5 min, 24 h)`.
- Neither → `now + 1 h`.

Store `ETag` and `Last-Modified` verbatim. Metadata changes (redirect URIs, name) apply on the next
authorize. Existing grants are kept (§10.5 leaves this to the AS).

### 5.5 Redirect URI rules and loopback matching

A registrable redirect URI (CIMD and DCR):

- is absolute, has no fragment, has no userinfo, and is ≤ 2048 bytes;
- either uses scheme `https` with a host, or is a **loopback** URI: scheme `http` and host exactly
  `127.0.0.1`, `[::1]`, or `localhost`.

Port is optional. Query is allowed.

Matching a requested `redirect_uri` Q against a registered R:

- If R is loopback: Q is `http` with the same hostname string (`localhost` ≠ `127.0.0.1`), the same
  `EscapedPath`, and the same `RawQuery`. Q's port may be anything, or absent.
  This is OAuth 2.1 §8.4.2 / RFC 8252 §7.3, extended to `localhost` because Claude Code registers
  `http://localhost/callback` and sends `http://localhost:<random>/callback`.
- Else: byte equality.

The code stores Q exactly as sent, and the token request must repeat it byte for byte.

The loopback impersonation risk (MCP security considerations) is why the consent screen always shows the
loopback warning, why we never skip consent, and why redirect URIs are never auto-approved.

## 6. Token model

- **Opaque, not JWT.** One process is both AS and RS. Every token is a `token.New()` lookup token stored as
  SHA-256 (design "tokens and secrets"). Each `/mcp` request does one indexed lookup, which makes
  revocation immediate. JWTs would need signing keys, `aud`/`exp` checks, and a denylist to revoke.
- **Lifetimes** (constants in `internal/oauth/lifetimes.go`):

  | Item | Lifetime |
  |---|---|
  | authorization request | 10 min |
  | code | 2 min |
  | access token | 1 h |
  | refresh token | 30 days, renewed by rotation |

- **Scopes**: none. A token grants everything the user's roles allow through the MCP tools. `offline_access`
  is accepted and recorded but has no effect. We send no `scope` in `WWW-Authenticate` and never answer
  `403 insufficient_scope`. If scopes are added later, PRM gains `scopes_supported`, the tools check
  `Principal.Scopes`, and `RequireBearer` emits `scope=`.
- **Audience**: every code and token stores `resource = Resource(cfg)`. `RequireBearer` matches on it in
  SQL. A token minted for another resource can't exist today, but the check makes the binding explicit
  (MCP MUST validate audience).
- **Grant lifecycle**:
  1. Consent upserts the grant `(user, client)`. Repeated approvals (e.g. Claude Code on a second machine)
     add token families under the same grant.
  2. Every code exchange and refresh touches `last_used_at`. So does `RequireBearer`, at most once per 5 min.
  3. Code reuse and refresh reuse revoke only the affected family.
  4. RFC 7009 revocation revokes one family or one access token.
  5. Disconnecting in settings deletes the grant, and with it every family.
  6. Deleting a user cascades.

## 7. `/mcp`

### 7.1 Wiring (`internal/mcp/routes.go`)

```go
srv := NewServer(&Env{Deps: d, Sites: sites.NewService(d), Signer: token.NewSigner(d.Config.SecretKey)})
h := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return srv },
    &mcpsdk.StreamableHTTPOptions{
        Stateless:                    true,
        JSONResponse:                 true,
        DisableLocalhostProtection:   true, // §2 item 14; we check Host routing and Origin ourselves
        MaxRequestBodyBytes:          16 << 20,
        PropagateRequestCancellation: true,
        Logger:                       d.Logger,
    })
handler := checkOrigin(d.Config, oauth.RequireBearer(d, h))
r.App.Handle("POST /mcp", handler)
r.App.Handle("GET /mcp", handler)    // SDK answers 405 (stateless has no GET stream)
r.App.Handle("DELETE /mcp", handler) // SDK answers 405
```

A bare `"/mcp"` pattern conflicts with webui's `"GET /"`: neither is more specific, so ServeMux panics.
That is why each method is registered separately.

`NewServer`:

```go
mcpsdk.NewServer(&mcpsdk.Implementation{Name: "agent-dash", Title: "agent-dash", Version: "0.1"},
    &mcpsdk.ServerOptions{Instructions: instructions})
```

Then apply every entry of `toolsets`. `ServerOptions.Logger` stays nil, because message logging could
include file contents.

**Why stateless:**

- MCP 2026-07-28 removed sessions. SDK v1.8.0's stateless handler serves both 2025-11-25 clients (it
  synthesizes the initialized session per request) and 2026-07-28 clients.
- Any replica can serve any request. The Helm chart supports several replicas with S3/GCS, and there is no
  sticky-session or session-store requirement.
- Each request's principal comes from that request's bearer token. A stateful session would keep the first
  request's context, which matters across token rotation and revocation.
- We use no server-to-client requests (sampling, elicitation, roots) and no notifications, so nothing is lost.

`JSONResponse` returns `application/json` instead of an SSE body. Clients must accept both. JSON is simpler
behind proxies and needs no flushing.

### 7.2 Context and identity

The SDK derives every handler's context from the `Connect` context, which in stateless mode is the HTTP
request's context. See `mcp/streamable.go` `connectStreamable(req.Context(), …)` and jsonrpc2's `notDone`
wrapper, which keeps values. Values that `RequireBearer` sets therefore reach the tools.

`RequireBearer` stores `oauth.WithPrincipal(ctx, p)`. It also **overwrites** `identity.WithUser(ctx,
identity.User{ID: p.UserID, Email: p.Email, EmailVerified: p.EmailVerified})`. W3's session middleware
(`UseApp`) runs on every app-host request, including `/mcp`, and would otherwise leave a cookie user in
the context. After the overwrite, no code reached through `/mcp` can act as the cookie's user.

Tools must use `oauth.PrincipalFrom` only. Tests inject it with `srv.Connect(oauth.WithPrincipal(ctx, p), st, nil)`.

We don't use the SDK's `auth.RequireBearerToken`:

- Its `WWW-Authenticate` omits RFC 6750's `error="invalid_token"`.
- Its error bodies are plain text.
- Its `TokenInfo` key is unexported, so it adds nothing for us: `req.Extra.TokenInfo` stays nil, which is
  fine because stateless mode does not use it for session binding.

### 7.3 `oauth.RequireBearer`

```go
type Principal struct {
    UserID        uuid.UUID
    Email         string // primary; "" if none
    EmailVerified bool
    ClientID      string // CIMD URL or DCR id
    ClientName    string
    GrantID       uuid.UUID
    Scopes        []string
}
func RequireBearer(d *server.Deps, next http.Handler) http.Handler
func PrincipalFrom(ctx context.Context) (Principal, bool)
func WithPrincipal(ctx context.Context, p Principal) context.Context
```

1. `Authorization` absent, or its scheme is not `Bearer` (case-insensitive) →
   ```
   401, WWW-Authenticate: Bearer resource_metadata="https://dash.example.com/.well-known/oauth-protected-resource/mcp"
   {"error":"invalid_token","error_description":"Authentication required."}
   ```
2. A `Bearer` value that is not exactly one 43-character base64url token, or `GetAccessTokenPrincipal(hash,
   Resource(cfg))` finds no row →
   ```
   401, WWW-Authenticate: Bearer error="invalid_token", error_description="The access token is invalid, expired, or revoked.", resource_metadata="…/mcp"
   ```
3. Success → `TouchGrant`, set context, call `next`.

Tokens in the query string or form body are never read. Cookies are never read. All 401s set
`Cache-Control: no-store`.

### 7.4 Origin (DNS rebinding), `internal/mcp/origin.go`

The MCP transport rule is: if `Origin` is present and invalid, answer 403.

- No `Origin` header: allowed. Native clients and server-side clients (claude.ai) send none.
- `Origin` equal to `APP_ORIGIN` (`Config.AppOrigin.String()`): allowed.
- Anything else, including `null`, any `*.SITES_DOMAIN` origin, and `http://localhost:*` →
  `403 {"jsonrpc":"2.0","error":{"code":-32600,"message":"Forbidden: Origin not allowed"}}`.

This runs before authentication. No CORS headers are ever sent, so preflights fail closed.

## 8. MCP tools

### 8.1 Conventions

- Arguments and results use snake_case. `site` is a site id with schema
  `{"type":"string","pattern":"^[a-z2-7]{12}$"}`.
- `deployment_id` and `version_id` are UUID strings
  (`pattern: ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).
- Times are RFC 3339 UTC strings.
- Register with `mcpsdk.AddTool[In, Out]`. The SDK validates input against the schema, puts `Out` in
  `structuredContent`, and mirrors it as JSON text content.
- Schemas come from `jsonschema.For[In]`, patched by `schemaFor[In](func(*jsonschema.Schema))` in
  `schema.go`. The patch adds `enum`, `pattern`, `minLength`/`maxLength`, `maxItems`, and
  `minimum`/`maximum`, none of which struct tags can express. Field descriptions come from the
  `jsonschema:"…"` tag.
- Structs disallow additional properties, so unknown arguments are rejected.
- Every tool sets `Annotations.Title` and `OpenWorldHint: ptr(false)`. It also sets `ReadOnlyHint`, plus
  `DestructiveHint` and `IdempotentHint` for tools that are not read-only, as listed below.
- **Access helper** (`access.go`): `env.site(ctx, siteID, action)`:
  1. Get the principal.
  2. Call `Sites.Role`. `""` → error `site "<id>" was not found or is not shared with you`. Missing and
     unshared sites give the same answer, like `/app-api`.
  3. If `!rbac.Can(role, action)` → error `your role on site <id> is <role>; <tool> needs <manager|owner>`.
  4. Return `Sites.Get`.
- Role mapping: the design table says "manager" for these tools. Viewers use the browser.

  | Tools | `rbac` action |
  |---|---|
  | `whoami`, `list_sites`, `create_site` | none |
  | `get_site`, `list_files`, `read_file`, `list_versions`, `publish_site`, `create_deployment`, `upload_files`, `get_upload_url`, `finalize_deployment`, `rollback_site` | `rbac.Publish` |
  | `update_site` | `rbac.EditMeta` |
  | `delete_site` | `rbac.Delete` |

- **Errors**: handlers return Go errors, which the SDK turns into `isError: true` results the model can read.

  | Cause | Message |
  |---|---|
  | `*sites.InputError` | its `Msg` |
  | `sites.ErrDeploymentClosed` | `deployment <id> is already finalized or abandoned; start a new one with create_deployment` |
  | Deployment not found, or not on this site | `deployment <id> was not found on site <site>` |
  | Version not found | `version <id> is not a version of site <site>; see list_versions` |
  | Anything else | logged with the tool name; returns `internal error, try again` |

- **File objects** (`publish_site`, `upload_files`):

  ```json
  {"type":"object","required":["path","content"],"additionalProperties":false,"properties":{
    "path":{"type":"string","minLength":1,"maxLength":1024,"description":"Relative path from the site root, e.g. \"index.html\", \"css/app.css\"."},
    "content":{"type":"string","description":"File contents: the text itself for encoding utf8, or base64 for encoding base64."},
    "encoding":{"type":"string","enum":["utf8","base64"],"default":"utf8"},
    "content_type":{"type":"string","maxLength":200,"description":"Optional; overrides the type inferred from the extension."}}}
  ```

  - `utf8` bytes are the string as given.
  - `base64` drops whitespace, then tries `base64.StdEncoding`, then `RawStdEncoding`. On failure:
    `files[<i>].content is not valid base64`.
  - Paths go to the service unchanged; W4's `CleanPath` validates them.
  - Arrays: `maxItems: 1000` for `files` and for `delete_paths`.

- **Limits**:

  | Limit | Value |
  |---|---|
  | MCP request body | 16 MiB (`MaxRequestBodyBytes`) |
  | Files per call | 1000 |
  | `read_file` | 1 MiB (`maxReadBytes`) |
  | `list_versions` limit | 50 by default, 500 maximum |

  W9 may make these configurable.

### 8.2 Server instructions (`ServerOptions.Instructions`)

> agent-dash hosts static websites (HTML, CSS, JavaScript, images) that you publish for a person to view at a
> stable, private URL; no server-side code runs. Viewers sign in to see a site, and only people the owner shares
> it with can open it. Typical flow: list_sites (reuse an existing site to keep its URL) or create_site →
> publish_site for small sites (under ~10 MB per call), or create_deployment → upload_files / get_upload_url →
> finalize_deployment for large ones → give the user the site's URL. Every publish is kept as a version;
> rollback_site undoes a bad one.

### 8.3 Tool reference

Descriptions are the exact text agents see.

#### `whoami` — any signed-in user

- Description: "Show which agent-dash account this connection acts as (email and user id) and which app is
  connected. Call it when you need to know whose sites you are working with."
- Input: `{}`.
- Output: `{user_id, email, email_verified, client_id, client_name}`.
- Annotations: readOnly.

#### `list_sites` — any

- Description: "List the sites you can publish to: sites you own or manage, most recently updated first.
  Each entry has the site id (pass it as `site` to the other tools), title, your role, the site's URL, and
  whether anything is published yet. Sites shared with you only as a viewer are not listed. To update
  recurring content such as a daily dashboard, reuse the existing site so its URL stays the same; use
  create_site only for something new."
- Input: `{}`.
- Output: `{sites: [{id, title, description, url, role: "owner"|"manager", published: bool, current_version_id: string|null, updated_at}]}`.
  `url` = `Config.SiteOrigin(id) + "/"`. Filter `ListForUser` to owner and manager.
- Annotations: readOnly.

#### `create_site` — any

- Description: "Create a new, empty site that you own and return its id and URL. The URL is permanent and
  private: viewers must sign in, and only people the owner shares the site with can open it. Nothing is
  served until you publish with publish_site or a staged deployment. Check list_sites first: updating an
  existing site keeps its URL, which is usually what the user wants."
- Input: `{title: string (1..200, required), description?: string (≤2000)}`.
- Output: `{id, title, description, url, role: "owner"}`.
- Errors: `InputError` for an empty or over-long title.
- Annotations: readOnly false, destructive false, idempotent false.

#### `get_site` — manager

- Description: "Get one site's details: title, description, URL, your role, the current version (id,
  publish time, file count, total size, publisher), whether an 'anyone with the link' public link is
  enabled, and how many accounts have access."
- Input: `{site}`.
- Output: `{id, title, description, url, role, current_version: {id, created_at, published_by: string|null, file_count, size}|null, public_link_enabled, member_count, created_at, updated_at}`.
  `member_count` and `public_link_enabled` come from `GetSiteSharingSummary`. The current version comes
  from `Sites.GetVersion(site, CurrentVersionID)`.
- Annotations: readOnly.

#### `update_site` — manager (`EditMeta`)

- Description: "Change a site's title and/or description. Only the fields you pass change; published
  content and the URL are not affected."
- Input: `{site, title?: string (1..200), description?: string (≤2000)}`. At least one field is required:
  an empty update errors with `pass title, description, or both`.
- Output: same as `get_site` without the sharing fields: `{id, title, description, url, role, updated_at}`.
- Annotations: destructive false, idempotent true.

#### `delete_site` — owner (`Delete`)

- Description: "Permanently delete a site: every version, every file, all sharing, and its URL, which stops
  working immediately. This cannot be undone; rollback_site cannot recover a deleted site. Only the owner
  can delete. Confirm with the user first. To take content down temporarily, publish a placeholder page
  instead."
- Input: `{site}`.
- Output: `{deleted: true, site}`.
- A storage cleanup error from `Sites.Delete` after the DB delete still reports success, because the site
  is gone. Log it; the orphaned blobs are W9's GC.
- Annotations: destructive true, idempotent false.

#### `publish_site` — manager

- Description:
  > Publish files to a site as a new version in one call; the site's URL serves them immediately. Use it for
  > sites up to about 10 MB in total (the request limit is 16 MB after encoding). For larger sites, many
  > binary assets, or files produced over several steps, use create_deployment with upload_files or
  > get_upload_url, then finalize_deployment.
  >
  > Paths are relative to the site root, slash-separated and case-sensitive: "index.html", "css/site.css",
  > "img/logo.png" — no leading "/", no "..", nothing under "_auth/". "/" serves index.html, a directory serves
  > its index.html, "/about" also finds "about.html", and a 404.html, if present, is shown for missing pages.
  > Use encoding "utf8" (the default) for text files — HTML, CSS, JavaScript, SVG, JSON, Markdown — and pass the
  > text as-is; use "base64" for binary files such as images, fonts, PDFs, and audio. The content type comes
  > from the extension; set content_type only to override it.
  >
  > mode is required. "replace" publishes exactly the files you pass: anything not listed disappears from
  > the site. "merge" starts from the current version, adds or overwrites the files you pass, and removes
  > delete_paths — use it to change a few files of an existing site. delete_paths is only allowed with merge.
  >
  > Publishing is atomic: viewers see the old version or the new one, never a mix. Every publish is kept;
  > list_versions and rollback_site undo a bad publish. No server-side code runs, so build interactivity with
  > client-side JavaScript and embed or bundle any data the pages need.
- Input: `{site, mode: "replace"|"merge" (required), files: [file] (required, 0..1000), delete_paths?: [string] (≤1000)}`.
  The service enforces the rest: an empty result → `InputError`, and `delete_paths` in replace mode → `InputError`.
- Output: `{site, url, version: {id, created_at, file_count, size}}`.
- Call: `Sites.Publish(site, principal.UserID, mode, files, deletePaths)`.
- Annotations: destructive true, idempotent false.

#### `create_deployment` — manager

- Description: "Start a staged publish, for large sites or files produced over several steps; returns a
  deployment_id. Add files with upload_files (inline, up to 16 MB per call, repeatable) and/or upload a
  whole .tar.gz or .zip through get_upload_url, then call finalize_deployment to publish everything
  atomically as one new version. The site does not change until finalize. mode 'replace' publishes exactly
  the deployment's files; 'merge' applies them on top of the version that is current when you finalize, and
  removes any delete_paths. A deployment you never finalize has no effect."
- Input: `{site, mode: "replace"|"merge"}`.
- Output: `{deployment_id, site, mode}`.
- Annotations: destructive false, idempotent false.

#### `upload_files` — manager

- Description: "Add files to an open staged deployment from create_deployment. Files use the same format as
  publish_site: relative path, content, encoding 'utf8' for text or 'base64' for binary, optional
  content_type. Uploading a path again replaces the earlier upload. In merge deployments, delete_paths marks
  files of the current version to remove at finalize. Keep each call under 16 MB and call repeatedly for
  more. Nothing is visible until finalize_deployment."
- Input: `{site, deployment_id, files: [file] (0..1000), delete_paths?: [string] (≤1000)}`. Both empty →
  `pass files, delete_paths, or both`.
- Behavior: check that `GetDeployment(id).SiteID == site` (else not found), then `AddFiles` and, if given,
  `DeletePaths`.
- Output: `{deployment_id, files_added, paths_deleted}`.
- Annotations: destructive false, idempotent true.

#### `get_upload_url` — manager

- Description:
  > Get a short-lived URL for uploading a whole site as one archive into an open staged deployment — the best
  > option for big sites, many files, or binary assets when you can run shell commands. Build the archive from
  > the site's root directory, for example `tar -czf site.tar.gz -C dist .` or `(cd dist && zip -r ../site.zip .)`,
  > then upload the raw file with an HTTP PUT: `curl --fail-with-body -T site.tar.gz '<upload_url>'`. Send no
  > Authorization header: the URL itself is the credential, so do not share or log it. It expires after 15
  > minutes; get a new one if needed. .tar.gz and .zip are accepted (detected from the content). If every file
  > is inside one top-level folder (as `tar -czf site.tar.gz dist/` produces), that folder is removed from the
  > paths. Symlinks and special files are rejected. Limits: 512 MB uncompressed, 64 MB per file, 10,000 entries.
  > You can upload more than once; later uploads overwrite earlier paths. Then call finalize_deployment.
- Input: `{site, deployment_id}`. The deployment must be open and on this site.
- Output:
  ```json
  {"upload_url": "https://dash.example.com/api/deployments/<id>/archive?grant=<signed>",
   "method": "PUT", "expires_at": "…", "max_bytes": 536870912, "formats": ["tar.gz", "zip"],
   "example": "curl --fail-with-body -T site.tar.gz '<upload_url>'"}
  ```
- Annotations: readOnly true. The call mints a credential but changes no state.

#### `finalize_deployment` — manager

- Description: "Publish an open staged deployment as the site's new current version, atomically. In merge
  mode its files are applied on top of whatever version is current right now. If the deployment has no
  files, or a merge would leave the site empty, nothing changes and the deployment stays open so you can
  add files and retry. A deployment can be finalized once. Returns the new version and the site URL."
- Input: `{site, deployment_id}`.
- Output: `{site, url, version: {id, created_at, file_count, size}}`.
- Annotations: destructive true, idempotent false.

#### `list_files` — manager

- Description: "List the files of the site's current version with path, size in bytes, content type, and
  SHA-256. Use it before a merge publish to see what exists, or to check what viewers are served. Empty
  when nothing is published."
- Input: `{site}`.
- Output: `{site, version_id: string|null, files: [{path, size, content_type, sha256}]}`.
- Annotations: readOnly.

#### `read_file` — manager

- Description: "Read one file of the site's current version, by the path list_files shows (e.g.
  'index.html'). Text files come back with encoding 'utf8', anything else as 'base64'. Files over 1 MB
  can't be read with this tool."
- Input: `{site, path: string (1..1024)}`.
- Behavior: `Sites.OpenFile`. If `size > 1 MiB`, error
  `<path> is <n> bytes; read_file returns files up to 1 MB`. Read the whole file.
  - `utf8` when the type is `text/*`, `application/json`, `application/javascript`, `application/xml`,
    `image/svg+xml`, or ends in `+json` or `+xml`, **and** the bytes are valid UTF-8.
  - `base64` (`StdEncoding`) otherwise.
- Output: `{path, content_type, size, sha256, encoding, content}`.
- Annotations: readOnly.

#### `list_versions` — manager

- Description: "List the site's published versions, newest first: version id, publish time, publisher's
  email, file count, total size, and which one is current. Every publish is kept indefinitely; pass a
  version id to rollback_site to restore it."
- Input: `{site, limit?: integer 1..500 (default 50)}`.
- Output: `{site, current_version_id: string|null, total, versions: [{id, created_at, published_by: string|null, file_count, size, current}]}`.
- Annotations: readOnly.

#### `rollback_site` — manager

- Description: "Make an earlier version from list_versions the site's current version again. The URL serves
  it immediately. Nothing is deleted, so you can roll forward later by passing a newer version id."
- Input: `{site, version_id}`.
- Output: `{site, url, current_version_id}`.
- Annotations: destructive false, idempotent true.

Registration files:

| File | Tools |
|---|---|
| `tools_account.go` | `whoami` |
| `tools_sites.go` | `list_sites`, `create_site`, `get_site`, `update_site`, `delete_site` |
| `tools_publish.go` | `publish_site`, `create_deployment`, `upload_files`, `get_upload_url`, `finalize_deployment` |
| `tools_content.go` | `list_files`, `read_file`, `list_versions`, `rollback_site` |

Each file's `init` appends to `toolsets`. W6 later adds `tools_sharing.go`.

## 9. Archive upload: `PUT /api/deployments/{id}/archive`

`internal/upload` (`routes.go`, `upload.go`):

```go
const GrantTTL = 15 * time.Minute
// URL returns the upload URL for a deployment; get_upload_url calls it.
func URL(c *config.Config, s *token.Signer, g token.UploadGrant) string
// → c.AppOrigin.String() + "/api/deployments/" + g.DeploymentID.String() + "/archive?grant=" + url.QueryEscape(s.UploadGrant(g))
```

`get_upload_url` builds `token.UploadGrant{DeploymentID, SiteID, Expires: now + GrantTTL}`.

Handler `PUT /api/deployments/{id}/archive`. No session, no CSRF, no CORS. Never read cookies.

1. `ParseUploadGrant(r.URL.Query().Get("grant"), now)`:
   - `ErrInvalid` → `403 {"error":"invalid_grant","message":"The upload URL is invalid."}`.
   - `ErrExpired` → `403 {"error":"expired_grant","message":"The upload URL expired; call get_upload_url again."}`.
2. Path `{id}` must parse as a UUID equal to `grant.DeploymentID`, else `403 invalid_grant`.
3. `Sites.GetDeployment(id)`:
   - Missing, or `SiteID != grant.SiteID` → `404 not_found`.
   - State not `open` → `409 {"error":"deployment_closed",…}`.
   - `CreatedBy` null, or the creator's current role cannot `rbac.Publish` → `403 forbidden`. This re-checks
     RBAC at upload time, so a manager removed within the 15 minutes is refused.
4. `body := http.MaxBytesReader(w, r.Body, sites.MaxArchiveSize)`. Peek 4 bytes:
   - `1f 8b` → `"tar.gz"`.
   - `50 4b 03 04` → `"zip"`.
   - Anything else → `415 {"error":"unsupported_format","message":"Upload a .tar.gz or .zip archive."}`.
   - `Content-Type` is ignored, because `curl -T` sends none.
5. `Sites.AddArchive(ctx, id, io.MultiReader(peeked, body), format)`:
   - `*InputError` → `400 {"error":"invalid_archive","message": Msg}`.
   - `*http.MaxBytesError` → `413 {"error":"too_large"}`.
   - `ErrDeploymentClosed` → `409`.
6. `200 {"deployment_id": "…", "status": "uploaded", "next": "Call finalize_deployment to publish."}`.

Other methods get the mux's 405.

### 9.1 Additive change in `internal/sites`

`upload_files` and `finalize_deployment` check that the deployment belongs to `site`, and the upload
handler re-checks the creator's role. Both need:

```go
type Deployment struct {
    ID        uuid.UUID
    SiteID    string
    Mode      Mode
    State     string        // "open" | "finalized" | "abandoned"
    CreatedBy uuid.NullUUID
    CreatedAt time.Time
}
func (s *Service) GetDeployment(ctx context.Context, id uuid.UUID) (Deployment, error) // ErrNotFound
```

The implementation is a thin wrapper over the existing `db.GetDeployment`. Put it in its own commit and tell
Main (W4's package).

## 10. Test plan

All Go tests use `sitestest.Deps(t)` (dbtest database plus local store) unless noted. They run in CI's
existing Go job with PostgreSQL.

### 10.1 `internal/oauth`

A harness (`harness_test.go`) builds `server.New(d, auth.Register, appapi.Register, oauth.Module(oauth.Options{CIMD: fake}))`
behind `httptest.NewTLSServer`. It sets `d.Config.AppOrigin`/`AppHost` to the TLS server's URL and
`127.0.0.1`, so `__Host-` cookies work with a `cookiejar`. Helpers:

- `signIn(t, email)`: `sitestest.User`, `db.CreateSession` with `token.New()`, and the cookie
  `auth.SessionCookie` in the jar.
- `csrf(t)`: `GET /app-api/me` → `csrfToken`.
- `authorize(t, params)`: GET without following redirects.
- `consent(t, request, approve)`: POST with `Origin` and `X-CSRF-Token`.
- `pkce()`: a verifier and its S256 challenge.

The fake CIMD host is a second `httptest.NewTLSServer` serving per-test documents and headers and counting
hits. The module takes `safehttp.New(safehttp.Options{AllowLoopback: true, TLSClientConfig: &tls.Config{RootCAs: pool(cimdSrv.Certificate())}, Timeout: time.Second, MaxBodyBytes: 5 << 10})`.
Both servers share `127.0.0.1` but have different ports, so the origins differ and §5.1 passes.

`oauth.Options` (exported for tests):

```go
type Options struct {
    CIMD *safehttp.Client
    Now  func() time.Time
}
func Module(o Options) server.Module
// Register = Module(Options{})
```

Cases:

1. **Metadata**
   - Both PRM URLs return the §4.1 document. Its `resource` equals `ts.URL + "/mcp"`.
   - The AS document matches §4.2 key for key. `registration_endpoint` appears only with DCR on.
   - No `Access-Control-*` headers.
2. **401 discovery** (with `internal/mcp` registered):
   - `POST /mcp` with no token → exact header `Bearer resource_metadata="<ts>/.well-known/oauth-protected-resource/mcp"`.
   - A garbage token, an expired token (insert with `expires_at` in the past), a revoked one, or one stored
     with `resource = "https://other.example/mcp"` → `error="invalid_token"` plus `resource_metadata`.
   - A session cookie and no bearer → 401.
   - `?access_token=` → 401.
   - `oauthex.ParseWWWAuthenticate` parses every header.
3. **CIMD URL table** (§5.1): about 15 cases. Among them: `http://`, userinfo, `/`, empty path, `/a/../b`,
   a query, a fragment, the same origin as `APP_ORIGIN`, a host under `SITES_DOMAIN`, and an `https://Host`
   case difference (byte mismatch after parse).
4. **CIMD fetch/validation**, each checked through `/oauth/authorize` → `/consent?error=…` (or success):
   - client_id mismatch; 302 from the host; 404; 500 with no cache → `client_metadata_unavailable`.
   - 500 with a cached row → success, and a warning is logged.
   - A 5121-byte body; `text/html`; invalid JSON; missing `client_name`; no usable `redirect_uris` (only
     `myapp://cb`); `client_secret` present; `client_secret_basic` in methods.
   - `private_key_jwt`-only → `invalid_client`. `["none","private_key_jwt"]` → OK.
   - `grant_types: ["client_credentials"]` → invalid.
   - The client_id mismatch case also deletes the client's existing grants.
   - `safehttp` with default options (no `AllowLoopback`) → `client_metadata_invalid` (blocked address).
5. **CIMD caching**:
   - `max-age=300` → a second authorize within the TTL doesn't hit the host.
   - After the clock (`Options.Now`) passes it, the request carries `If-None-Match`. A `304` updates
     `cache_expires_at`, and the document is unchanged.
   - `no-store` → every authorize fetches.
   - `max-age=999999` → clamped to 24 h. `max-age=1` → 5 min.
6. **Redirect matching table** (unit, `redirect_test.go`):
   - `http://localhost/callback` registered: matches `http://localhost:5555/callback`; rejects
     `http://127.0.0.1:5555/callback`, `http://localhost:5555/other`, and `https://localhost/callback`.
   - `http://[::1]/cb` with any port.
   - `https://claude.ai/api/mcp/auth_callback`: exact only (a trailing slash or a different query fails).
   - Fragments rejected.
   - DCR registration rules: `http://example.com/cb` and `myapp:/cb` rejected.
7. **Authorize validation order**:
   - A bad client and a bad redirect both give `/consent?error` and never reach the client.
   - These redirect with `error`, `state`, and `iss == Issuer`: no `code_challenge`, `plain`, a 42-character
     challenge, `response_type=token`, `resource=https://evil/mcp` → `invalid_target`, duplicate `scope`,
     a 1025-byte `state` (no `state` echoed), `request_uri`.
   - Signed out → `303 /login?return=%2Foauth%2Fauthorize%3F…`.
   - A restricted recovery session behaves as signed out.
   - Success → `303 /consent?request=`.
8. **Consent**:
   - GET details (`verified`, `loopback`, `previouslyAuthorized`).
   - Another user's session → 404. Expired (clock) → 404.
   - Approve → a redirect with `code`, `state`, and `iss`. Deny → `access_denied`.
   - A second POST → 404.
   - POST without CSRF or with the wrong `Origin` → 403 (appapi).
9. **Token, authorization_code**:
   - Happy path. The response has `Cache-Control: no-store`, and `/mcp` `tools/list` works.
   - Wrong verifier, missing verifier, wrong `redirect_uri` port, wrong `client_id`, a `resource` mismatch
     (`invalid_target`), and an expired code.
   - Reuse: exchange, exchange again → `invalid_grant`, and the first pair now gets 401 on `/mcp` and
     `invalid_grant` on refresh.
   - Basic auth first → 401 `invalid_client`, then the same code as form parameters → success (the
     `x/oauth2` auto-detect case).
   - Wrong content type → 400.
10. **Token, refresh_token**:
    - Rotation returns a new pair, and the old refresh token is unusable.
    - Reusing the old one revokes the family: the new access token gets 401 and the new refresh token is
      `invalid_grant`.
    - An expired refresh token, another client's, `scope=foo` → `invalid_scope`, and a `resource` mismatch.
    - A client with `grant_types: ["authorization_code"]` gets no `refresh_token`.
11. **Revocation**:
    - Revoking a refresh token revokes its family.
    - Revoking an access token revokes only it.
    - Unknown token → 200. Another client's token → 200 with no effect.
12. **DCR**:
    - Off: 404, and no `registration_endpoint` in metadata.
    - On: register, then the full flow. Consent shows `verified: false`.
    - `invalid_redirect_uri` and `client_secret_basic` → `invalid_client_metadata`.
    - Turning DCR off afterwards: authorize gives `unauthorized_client`, and the token endpoint gives
      `invalid_client`.
13. **Grants API**:
    - The list shows the client. `DELETE` → 204, and its tokens die.
    - Deleting another user's grant → 404.
14. **`internal/safehttp`**: a 304 is returned as a response, not `ErrRedirect`.

### 10.2 `internal/mcp`

- **In-memory tool tests** (`tools_test.go`). `srv := mcp.NewServer(env)`, then
  `ct, st := mcpsdk.NewInMemoryTransports()`,
  `srv.Connect(oauth.WithPrincipal(ctx, p), st, nil)`, and
  `mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)`.
  - `ListTools` returns exactly the 15 names, sorted. Annotations match §8.3. Every input schema has
    `additionalProperties: false`, and `mode` and `encoding` carry their enums.
  - For each tool, check the happy path and a run as owner, manager, viewer, and stranger
    (`sitestest.Member`). The stranger and viewer messages match §8.1.
  - `publish_site`:
    - replace, then merge with `delete_paths`;
    - `delete_paths` in replace mode → `InputError`;
    - invalid base64 → `files[0].content is not valid base64`;
    - a binary round trip through `read_file` as base64; `../x` rejected.
  - Staged flow:
    - `create_deployment` → `upload_files` ×2 → `finalize_deployment`;
    - `upload_files` with another site's deployment → not found;
    - finalize twice → closed.
  - `get_upload_url`: parse the URL, and check that `ParseUploadGrant` yields the deployment and site.
  - `read_file`: text vs binary vs over 1 MiB.
  - `list_versions`: `limit`.
  - `rollback_site`: an unknown version, and a version of another site.
  - `whoami`: principal fields.
- **HTTP end to end with the real SDK client** (`e2e_test.go`). Same TLS harness as §10.1 plus
  `mcp.Register`.
  - The client is `mcpsdk.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: ts.Client(), OAuthHandler: h}`.
  - `h, _ := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{ClientIDMetadataDocumentConfig: &auth.ClientIDMetadataDocumentConfig{URL: cimdURL}, RedirectURL: "http://127.0.0.1:43123/callback", AuthorizationCodeFetcher: f, Client: ts.Client()})`.
  - `f` plays the browser: GET `args.URL` with the signed-in jar, follow to the consent request, POST
    approve, parse `redirectTo`, and return `{Code, State, Iss}`.
  - The CIMD document registers `http://127.0.0.1/callback`, which exercises port flexibility.
  - The SDK client checks PRM `resource`, AS `issuer`, `code_challenge_methods_supported`, and `iss`, so
    this test proves interoperability.
  - Then `ListTools`, `create_site`, `publish_site`, `list_files`, `read_file`.
  - Also:
    - `Origin: https://evil.example` → 403; `Origin` of a site host → 403; `Origin == APP_ORIGIN` → OK.
    - `GET /mcp` with a valid token → 405.
    - The request is served correctly when `Host` is the app host and the listener is loopback (localhost
      protection is off).

### 10.3 `internal/upload`

Fixtures are tar.gz and zip archives built in-test, including one with a common top-level directory.

- Success → files are in the deployment, and finalize publishes them.
- Expired grant → 403 `expired_grant`. A tampered grant → 403. A path id ≠ grant → 403.
- Finalized deployment → 409. A deployment on another site → 404.
- Creator demoted to viewer → 403.
- Plain text body → 415.
- Over the limit → 413. The test lowers an unexported `maxArchiveBytes` var through `export_test.go`.

### 10.4 Playwright: `web/e2e/oauth.spec.ts`

`internal/oauth/e2eseed` is a test-only Go command, like W4's. It reads the server env and inserts a CIMD
client row (`client_id https://client.e2e.test/cimd.json`, name "E2E Agent",
`redirect_uris ["http://127.0.0.1/callback"]`, `cache_expires_at` +1 day), so no fetch happens. It prints the
JSON. `web/e2e/support/oauth.ts` runs it.

1. Seed a user and session (W4 `seed.ts`) and the client.
2. Build a PKCE pair in Node. `page.goto("/oauth/authorize?…&redirect_uri=http://127.0.0.1:9/callback&state=s1")`.
3. Expect `/consent`: "E2E Agent", `client.e2e.test`, the loopback warning, the account email.
4. `page.route("http://127.0.0.1:9/**")` captures the URL. Click **Allow**, and assert `code`,
   `state=s1`, `iss`.
5. `request.post("/oauth/token", form)` → tokens. `request.post("/mcp")` with `initialize` and
   `tools/list` (`Accept: application/json, text/event-stream`) → 15 tools.
6. `/settings/security` shows "E2E Agent". Disconnect, then `/mcp` → 401.
7. Deny path → `error=access_denied`.
8. `/consent?error=client_metadata_invalid` renders the error card (axe check like the other specs).

## 11. Manual verification with real clients

**Prerequisites**: `docker compose up -d` (PostgreSQL), `make web`, and a fresh account per origin.
Passkeys are bound to the RP ID, so an account made on `dash.localhost` can't sign in on `localhost`.

### A. Claude Code against a local instance (no tunnel)

Claude Code sends credentials only to HTTPS token endpoints or to `localhost`, `127.0.0.1`, or `::1`
([docs](https://code.claude.com/docs/en/mcp)). So use `localhost` as the app host, not `dash.localhost`:

```sh
APP_ORIGIN=http://localhost:8080 SITES_DOMAIN=sites.localhost go run ./cmd/agent-dash
# browser: http://localhost:8080/signup → create an account with a passkey
claude mcp add --transport http agent-dash http://localhost:8080/mcp
claude   # in the session: /mcp → agent-dash → Authenticate
```

The server fetches the CIMD document from `claude.ai` (public, so `safehttp` allows it).

Expect:

1. The browser opens `/consent` showing **Claude Code**, host `claude.ai`, "Sends you back to:
   localhost:<port>", and the loopback warning. Allow it, and Claude Code reports connected with 15 tools.
2. Prompt: "With agent-dash, create a site called 'Smoke test' with an index.html that says hello and links
   style.css, publish it, and give me the URL." Open `http://<id>.sites.localhost:8080/`. The handoff signs
   you in, and the page renders with CSS.
3. "Change the heading to 'Hello again' using a merge publish." Reload: changed, same URL.
   "Roll back to the previous version." Reload: the old heading.
4. "Build a 3-page site with an image in a temp directory and publish it with get_upload_url and curl."
   Claude runs `tar`/`curl`, finalizes, and the pages and image load.
5. `/settings/security` lists Claude Code with a recent "last used".
   `claude mcp logout agent-dash` → the server logs `POST /oauth/revoke 200`. The next tool call
   re-prompts authentication.
6. Re-authenticate, then **Disconnect** in settings. The next tool call fails with 401, and Claude Code
   shows the server as needing authentication.

### B. Claude Desktop (claude.ai connector) — needs public HTTPS

Claude Desktop's custom connectors run from Anthropic's cloud (redirect URI
`https://claude.ai/api/mcp/auth_callback`). The app host must be publicly reachable over HTTPS. The site
host only needs to be reachable from your own browser.

1. Run a tunnel for the app host: `cloudflared tunnel --url http://localhost:8080` (or `ngrok http 8080`).
   Note `https://<random>.trycloudflare.com`.
2. Serve sites over local HTTPS on port 443. `SiteOrigin` takes its scheme and port from `APP_ORIGIN`, so
   site URLs become `https://<id>.sites.localhost`. `Caddyfile`:
   ```
   *.sites.localhost {
       tls internal
       reverse_proxy localhost:8080
   }
   ```
   Run `caddy trust` once, then `sudo caddy run`.
3. `APP_ORIGIN=https://<random>.trycloudflare.com SITES_DOMAIN=sites.localhost go run ./cmd/agent-dash`.
   Check that the tunnel forwards the public `Host`:
   `curl -s https://<random>.trycloudflare.com/.well-known/oauth-protected-resource/mcp` must show
   `"resource":"https://<random>.trycloudflare.com/mcp"`. If it 404s, the tunnel rewrote `Host`; set it
   to preserve the host.
4. Sign up at the tunnel URL. Claude Desktop → Settings → Connectors → Add custom connector → URL
   `https://<random>.trycloudflare.com/mcp` (exactly; no trailing slash) → Connect.
5. Expect consent for **Claude**, host `claude.ai`, "Sends you back to: claude.ai", and no loopback warning.
   Allow it; the connector lists the tools.
6. Repeat A.2–A.3 in a Claude Desktop chat. Open the returned `https://<id>.sites.localhost/` URL in the
   same browser: the handoff goes through the tunnel origin and lands on the local Caddy site.
7. Disconnect in `/settings/security`; Claude Desktop's next tool call asks to reconnect.

Record the client versions and results in the PR description. These manual runs are the roadmap's
done-when.

## 12. Implementation order and file layout

Commits in this order. Each step builds and passes its tests.

1. **Dependencies** (separate mechanical commit): `go get github.com/modelcontextprotocol/go-sdk@v1.8.0`
   (pulls `github.com/google/jsonschema-go`; `golang.org/x/oauth2` is already present).
2. **Prerequisites**:
   - `internal/safehttp` 304 change and test;
   - `internal/sites` `Deployment` and `GetDeployment` (own commit; tell Main);
   - the migration ([§3.1](#31-new-migration)), `queries/oauth.sql`, `queries/mcp.sql`, and `go generate ./internal/db`.
3. **`internal/oauth` core**: `resource.go`, `lifetimes.go`, `errors.go` (OAuth JSON and redirect errors),
   `redirect.go` and `pkce.go` with unit tests, then `cimd.go` (URL rules, fetch, validation, caching) with
   the fake-host tests.
4. **Authorize and consent**: `metadata.go`, `authorize.go`, `consent.go` (both `/app-api/oauth/consent`
   endpoints), `grants.go`, `routes.go` (`Module`, `Register`), and `cmd/agent-dash/module_oauth.go`.
5. **Token**: `token.go`, `revoke.go`, `register.go`, and their tests.
6. **Bearer**: `bearer.go` (`Principal`, `RequireBearer`, `PrincipalFrom`, `WithPrincipal`).
7. **`internal/upload`**: `routes.go` and `upload.go` (`GrantTTL`, `URL`, the handler), tests, and
   `cmd/agent-dash/module_upload.go`.
8. **`internal/mcp`**:
   - `server.go` (`Env`, `toolsets`, `NewServer`, instructions), `schema.go`, `access.go`, `origin.go`,
     `routes.go`;
   - `tools_account.go`, `tools_sites.go`, `tools_publish.go`, `tools_content.go`;
   - `tools_test.go`, `e2e_test.go`;
   - `cmd/agent-dash/module_mcp.go`.
9. **Web**:
   - `web/app/consent/page.tsx`;
   - `web/components/consent/{consent-card.tsx,consent-error.tsx,data.ts}` (API types and calls);
   - `web/components/settings/oauth-grants.tsx` and the one-line mount in `security-settings.tsx`;
   - `internal/oauth/e2eseed/main.go`, `web/e2e/support/oauth.ts`, `web/e2e/oauth.spec.ts`.
10. **Docs**:
    - design.md edits from [§2](#2-corrections-to-designmd-and-workplanmd) (route table, MCP authorization,
      tokens table, consent, `notify` note);
    - workplan W5 → "done" with the as-built additions (`Principal` fields, `oauth.Module`/`Options`,
      `WithPrincipal`, `Resource`, `mcp.Env.Signer`, `mcp.NewServer`, `upload.URL`/`GrantTTL`,
      `sites.GetDeployment`, the migration);
    - the W8 ingress body-size note and the W9 rate-limit/GC notes.
11. **Manual verification** ([§11](#11-manual-verification-with-real-clients)) with Claude Code and Claude Desktop.

Final layout:

```
internal/oauth/     resource.go lifetimes.go errors.go redirect.go pkce.go cimd.go metadata.go
                    authorize.go consent.go token.go revoke.go register.go grants.go bearer.go routes.go
                    *_test.go e2eseed/main.go
internal/mcp/       server.go schema.go access.go origin.go routes.go
                    tools_account.go tools_sites.go tools_publish.go tools_content.go *_test.go
internal/upload/    routes.go upload.go upload_test.go export_test.go
internal/db/        migrations/<ts>_oauth_requests.sql queries/oauth.sql queries/mcp.sql (+ generated)
cmd/agent-dash/     module_oauth.go module_mcp.go module_upload.go
web/app/consent/page.tsx
web/components/consent/   consent-card.tsx consent-error.tsx data.ts
web/components/settings/oauth-grants.tsx
web/e2e/oauth.spec.ts web/e2e/support/oauth.ts
```
