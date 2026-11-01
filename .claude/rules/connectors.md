---
paths:
  - "**/internal/services/connector_*.go"
  - "**/internal/services/mcp_*.go"
  - "**/internal/handlers/api_connectors*.go"
  - "**/internal/handlers/api_mcp_servers*.go"
  - "**/internal/database/models_connectors.go"
  - "**/web/src/pages/Connector*.tsx"
  - "**/web/src/components/connectors/**"
---

# Connectors

The Connectors page (`/connectors`) replaces Tools, Settings → Integrations / Channels / Alert
sources. A `connectors` row groups the parts of one system; the parts keep their own tables and
point at it through a nullable `connector_id`: `tool_instances`, `alert_source_instances`,
`integrations`, `mcp_server_configs`.

- ownership is explicit, never inferred from a type or name. `ConnectorService.Reconcile()` (every
  start) gives each unlinked part its own connector (1:1 upgrade backfill), links each MCP server's
  managed tool instance to the MCP connector, and prunes empty connectors. Parts move with
  `Attach` / `Detach`; the UI "Merge" moves every part of a same-catalog connector
- the Go catalog (`connector_catalog.go`) maps a catalog id to accepted tool types, alert source
  types and messaging provider; `accepts()` is the only compatibility check. HTTP connector
  definitions are `http:<tool_type_name>` entries; unknown tool/alert types get a generic entry;
  `incidents`/`proposals`/`events` are never connectors
- create endpoints (`/api/tools`, `/api/alert-sources`, `/api/integrations`, `/api/mcp-servers`)
  accept an optional `connector_uuid`; without it the handler creates a connector for the new part
  (`linkNewPart`). Part deletes call `PruneEmpty`. All old `/api/*` paths and payloads are
  unchanged (akm does not follow redirects)
- list/detail DTOs carry no credentials. Status: `ok`, `connected`/`error` (MCP, from the gateway's
  `/mcp-servers/status`), `disabled`, `needs_setup` (no parts, or a messaging integration without
  channels), `unknown`. Webhook silence is not an error; `last_alert_at` is shown instead
- SQLite returns `MAX(datetime)` as text: scan such aggregates into `database.FlexTime`

# Remote MCP servers (custom connectors)

- only remote MCP servers: `transport` `http` (legacy stored value `sse` means the same). stdio is
  gone: the API rejects command/args/env_vars, the migration disables stored stdio rows, the
  gateway skips them
- every MCP server owns a managed `tool_types` row (`kind = "mcp"`, name = namespace prefix) and
  one tool instance (`settings.mcp_server_config_id`). Skills (`skill_tools`), cron jobs
  (`cron_job_tools`) and the per-run allowlist bind that instance, so the gateway authorizes MCP
  tools like any tool. `MCPServerService` keeps them in sync on create/update/delete (+
  `SyncAllManagedTools` at start); `/api/tools` PUT/DELETE on a managed instance → 409
- `auth_config` types: `bearer` (token), `header` (header_name + value), `oauth` (tokens from the
  OAuth flow). GET masks secrets as `********`; PUT: absent = keep, null = clear, object = replace,
  and a missing/empty/masked secret keeps the stored one when the type does not change
- OAuth (`mcp_oauth_service.go`): RFC 9728 resource metadata (WWW-Authenticate hint first) → RFC
  8414 / OIDC discovery → stored client or RFC 7591 registration → PKCE S256 + `resource` →
  public callback `/api/mcp-servers/oauth/callback` (in the JWT skip list; one-time state, 15 min,
  in memory) → tokens in auth_config, gateway reload. `RefreshDue` runs every minute (5 min lead);
  `invalid_grant` clears tokens and sets `oauth_error`. The redirect URL needs
  `GeneralSettings.BaseURL`. Outbound OAuth HTTP refuses loopback/link-local/metadata and redirects
- connection tests and status go through the gateway (`MCPGatewayClient`), never from the API, so
  the same network policy applies
