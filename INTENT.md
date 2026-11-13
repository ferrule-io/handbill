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
- **Security**: artifacts are not public by default. Viewers must be authorized.
  The access model is undecided (see open questions).
- **Self-hostable**: shipped as Docker image(s) and a Helm chart.

## Non-goals

- Dynamic/server-side apps or hosting arbitrary backends.
- Depending on any third-party hosting of artifact content.

## Open questions

- Access model: per-artifact share links/tokens, viewer accounts, full RBAC, or
  a mix (e.g. publisher-only admin + shareable viewer links)?
- Viewer identity: are viewers (e.g. spouse) logged-in users, or link holders?
- Artifact lifecycle: ephemeral with expiry, versioned/updatable (dashboard),
  or both?
- Publishing interface: HTTP API, CLI, MCP server, or several?
- Storage backend: local volume, S3-compatible object storage, or pluggable?
