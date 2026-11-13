-- Read-only queries for MCP tools (internal/mcp).

-- name: GetSiteSharingSummary :one
SELECT (SELECT count(*) FROM memberships m WHERE m.site_id = @site_id)::int AS member_count,
       coalesce((SELECT p.enabled FROM public_links p WHERE p.site_id = @site_id), false)::boolean AS public_link_enabled;
