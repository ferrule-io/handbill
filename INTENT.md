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

- **Access control**: token/URL-based to start. Viewers are link holders —
  possessing the artifact's tokenized URL grants view access. No viewer
  accounts or RBAC yet.
- **Lifecycle**: sites are updatable in place; an update keeps the same URL so
  viewers (e.g. a daily dashboard) see the new content. No expiration for now.
- **Publishing interface**: the server exposes an MCP endpoint at `/mcp`.
- **Storage**: pluggable adapter with S3, GCS, and local filesystem
  implementations.

## Open questions

- How publishers authenticate to `/mcp` (API key, bearer token, OAuth)?
- Do updates retain previous versions, or overwrite?
- Are other publish interfaces (HTTP API, CLI) needed besides MCP?
- Token revocation/rotation for a shared URL.
