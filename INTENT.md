# agent-dash — Intent

## Problem

Agents (Claude and others) routinely produce small static sites meant for another
person to look at: a one-page HTML summary, or a multi-page interactive site.
Today these get shared by either:

- **Claude-hosted artifacts** — content lives on Anthropic's servers (undesired),
  and producing them requires the Claude Desktop harness, which is now the only
  reason to use the desktop app.
- **ngrok tunnels** — a local server exposed for an hour while waiting for the
  other person to look. Fragile, ties up the machine, and dies when the tunnel closes.

## Goal

A self-hosted output endpoint for agent artifacts: any agent/harness can publish
a static site to it, and the intended viewer can open it at a stable URL, behind
some access control, without the publisher keeping anything running.

## Example uses

1. **Vacation planning** — present options to a spouse; once a choice is made,
   host the resulting itinerary.
2. **Daily life dashboard** — an agent-generated dashboard of the daily agenda,
   refreshed over time.
3. **Big purchases** — e.g. a car purchase site laying out trims and financing options.
4. Anything else an agent produces for a human to review.

## Requirements

- **Content**: static only — single HTML page up to multi-page interactive
  sites (HTML/CSS/JS/assets). No server-side execution of artifact content.
- **Harness-agnostic publishing**: must not depend on Claude Desktop; any agent
  that can make a request or run a CLI should be able to publish.
- **Security**: artifacts are not public by default. Viewers must be authorized
  (see decisions).
- **Self-hostable**: shipped as Docker image(s) and a Helm chart.

## Non-goals

- Dynamic/server-side apps or hosting arbitrary backends.
- Depending on any third-party hosting of artifact content.

## Decisions

- **Access control**: user accounts with per-site RBAC. Sharing means granting
  another user a role on a site. (Supersedes the earlier token/URL-only model.)
- **Accounts**: identified by email. No passwords — sign-in is passkey or SSO.
  Google is the only SSO provider to start.
- **Sign-up**: zero barrier to entry — a viewer opening a shared link should be
  able to create an account and see the site in one or two steps. Anyone can
  sign up and create sites. Accepted risk: an internet-facing instance can be
  used to host arbitrary content (including phishing) under the operator's domain.
- **Account recovery**: recovery codes are generated and shown once at sign-up.
- **Notifications**: SMTP is optional and configurable; when set, the server
  sends notification emails. Everything must work without it.
- **Sites**: each account can own and manage several sites, created and
  updated through the MCP endpoint.
- **Roles**: per-site owner, manager, and viewer. Managers publish and manage
  viewers; only the owner manages managers, the public link, deletion, and can
  transfer ownership.
- **Sharing**: invites always produce a link; a grant binds to the account that
  accepts it, and binds by email only to verified emails.
- **Public links**: optional per-site "anyone with the link" access.
- **Lifecycle**: sites are updatable in place; an update keeps the same URL so
  viewers (e.g. a daily dashboard) see the new content. Every publish is a
  retained version with rollback. No expiration for now.
- **Publishing interface**: the server exposes an MCP endpoint at `/mcp`,
  authorized with OAuth 2.1 using Client ID Metadata Documents.
- **URLs**: app on its own host; each site on a random subdomain under a
  dedicated sites domain (`<site-id>.sites.example.com`).
- **Storage**: pluggable adapter with S3, GCS, and local filesystem
  implementations.

## Open questions

- Do the target MCP clients (Claude Code, Claude Desktop, claude.ai) support
  CIMD? If not, a Dynamic Client Registration fallback is needed.
- Are other publish interfaces (HTTP API, CLI) needed besides MCP?
- Should pending invites and ownership transfers expire?
- Version retention limit, if any.

Design details: [docs/design.md](docs/design.md).
