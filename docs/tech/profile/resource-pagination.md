# Agent resource pagination

Agent Profile skill and MCP lists use server pagination with capacity measured from the Profile scroll region. Cards have a consistent row height; the container width chooses one or two columns. Resize updates preserve the first visible item's position. Capacity changes pause while a resource detail dialog is open. Each resource has independent page state, reset when the selected agent changes.

## Existing HTTP endpoints

`GET /api/v1/agents/{id}/skill-summaries` and `GET /api/v1/agents/{id}/mcp-servers` preserve their existing complete responses when `pagination` is omitted. MCP editing and candidate membership checks continue to use complete configuration. The skill installation dialog loads complete membership only when opened.

With `pagination=page`, parameters are `page` (default 1), `per` (default 20, maximum 100), optional `search`, `enabled`, and `list_revision`. Responses contain `items`, `page`, `per`, `total`, `list_revision`, and `has_more`. MCP items contain `name` and `config`; skill items retain their existing fields. Both response modes provide the Agent ETag used by enablement mutations.

With `pagination=cursor`, parameters are `limit` (default 20, maximum 100), optional `cursor`, `search`, `enabled`, and repeated `exclude`. Responses contain `items`, `total`, `list_revision`, `next_cursor` when more results exist, and `has_more`. Page and cursor parameters cannot be mixed. Invalid requests return `invalid_resource_query` with HTTP 400.

Filtering precedes deterministic ascending resource-name ordering and selection. Search uses case-insensitive name subsequence matching. The revision identifies the matching resource membership and query scope, independently of descriptions and MCP configuration contents. A mismatched revision or cursor returns `resource_list_changed` with HTTP 409. Profile refreshes the current page and clamps it to the new total; chat restarts the cursor sequence. Descriptions and configuration always reflect the current query, rather than a historical snapshot.

`internal/resourcequery` owns filtering, ordering, range selection, and cursor validation. HTTP handlers own query parsing and response envelopes. Skill queries go through Agent Engine and WorkspaceService; the local skill reader enumerates valid membership before reading metadata only for selected entries. Directory enumeration remains proportional to the total number of skills. MCP configuration remains a complete stored map; pagination bounds its response and per-page source status requests.

## Chat selection

The main composer, thread composer, and floating chat use the same infinite-query hook and picker. Opening the picker loads enabled skills in batches of 20. Search is debounced, sent to the backend, and replaces the previous cursor sequence. A sentinel near the bottom loads another batch. Keyboard navigation loads the next batch at the last skill, before entering suggested commands. Suggested-command selection survives appended skills.

The picker renders a bounded window of rows. Loading another batch keeps existing options available; failures expose a retry action. Built-in command names are shared with the controller and excluded by the backend before pagination.

Paged, complete, and infinite queries have distinct cache keys under each Agent resource prefix. Existing add, delete, and enablement invalidations refresh all active views. Source synchronization also refreshes paged MCP queries. Complete MCP draft serialization remains independent of visible pages.
