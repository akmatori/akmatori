---
paths:
  - "**/mcp-gateway/**"
  - "**/agent-worker/src/gateway-tools.ts"
---

# MCP Gateway and tool routing

Flow: agent reads its skill's `SKILL.md` and the generated `connected-tools/SKILL.md` (every
connected tool; see the `tool-authorization` rule) → agent calls
`gateway_call(toolName, args, instanceHint?)` → worker sends JSON-RPC to MCP Gateway with
`X-Incident-ID` (the run's scope: incident UUID or request scope, see the `agent-runs` rule) and
`X-Run-ID` (the run; `mcp.RunIDFromContext` in handlers) →
gateway resolves routing, enforces allowlists, executes, returns output.

- do not teach agents to call tool implementations directly; routing is handled by logical instance
  names or instance hints
- `ToolAllowlist` on `WebSocketMessage` is intentionally NOT `omitempty` — `[]` means reject-all,
  `null` means allow-all
- MCP proxy tools (`<namespace_prefix>.<tool>`) go through the allowlist like every tool: the
  tool type is the namespace prefix, and the entry comes from the MCP server's managed tool
  instance (see the `connectors` rule). There is no proxy-namespace bypass any more; `tools/list`
  is filtered by the allowlist too. `logical_name` / `tool_instance_id` are stripped before a call
  goes upstream. Upstream names with `.` or other characters are sanitized to `_` in the
  namespaced name (`disabled_tools` and status use the original names)
- remote MCP only (no stdio). Outbound MCP HTTP uses `NetPolicy` (`netpolicy.go`): the resolved IP
  is checked at dial time; loopback, link-local (incl. 169.254.169.254), unspecified and multicast
  are refused; private ranges are allowed unless `MCP_BLOCK_PRIVATE_NETWORKS=true`;
  `MCP_ALLOW_LOOPBACK=true` for dev/tests; no redirects; no env proxy (it would hide the
  destination); 10 MiB response cap. Streamable HTTP responses may be JSON or `text/event-stream`
- reload closes and re-initializes only servers whose url/transport/auth/namespace/disabled_tools
  changed, and closes removed ones. Reload records the wanted config per server (`pool.Expect`);
  a connect that finishes with another config, or for a removed server, is dropped with
  `ErrStaleConfig` instead of replacing the current connection
- a stored-server test (`/mcp-servers/test` with `id`) starts from the stored config and applies
  each given field (url, auth_config, transport) on its own
- `/registry/tools?namespace=<ns>` (internal) lists a namespace's registered tools with input
  schemas; the API renders them into `connected-tools/SKILL.md`
- admin endpoints (`/reload/*`, `/mcp-servers/test`, `/mcp-servers/status`, `/registry/tools`) are `internalOnly`:
  requests with forwarding headers are refused. The edge proxy (`proxy/nginx.conf`) exposes only
  `/mcp/tools*` (schemas for the UI); `/mcp/mcp` and the admin paths are internal

## Incidents tool (built-in, credential-less)

The `incidents` tool exposes `incidents.list` and `incidents.get` for read-only access to Akmatori's
own incident records. It is the only built-in tool that queries the gateway's own DB connection
(`database.DB`) directly rather than proxying to an external service.

- `EnsureToolTypes()` seeds both the `ToolType` and a single `ToolInstance` (logical name
  `"incidents"`, Name `"Incidents"`, empty Settings) so the tool appears in all pickers with zero
  operator configuration
- the seeded instance never requires credentials — do not add auth fields to it
- registered in `registry.go` via `registerIncidentsTools()` with no rate limiter
- `incidents` is in `builtInToolNamespaces`; the auth allowlist entry shape is
  `{ToolType: "incidents"}` (no InstanceID/LogicalName)
- `List` returns summary fields only (tokens summed over the runs); `Get` folds the incident's
  `agent_runs` (mirror model `AgentRun`, `database.LoadRunsWork`): newest `response`, every run's log
  keeping the newest 50,000 bytes
- when adding another credential-less built-in tool, follow the same seed pattern in
  `EnsureToolTypes()` and the same `registerXxxTools()` pattern in `registry.go`

`docs/TOOL_ARCHITECTURE.md` still describes the retired Python tool path; live routing is
gateway-first via `gateway_call`.

## Events tool (built-in, credential-less)

`events.search` / `events.get` read the `events` table; `events.report_alert` lets a CRON run
report a problem. Same seed and registration pattern as `incidents`; `events` is in
`builtInToolNamespaces` and `credentiallessNamespaces`.

- incident runs always get it; cron runs get it through `builtin_tools` (default on): a type-only
  `{ToolType: "events"}` entry (`IncidentRunAllowlist`, `CronAllowlist`)
- `get` on a request event adds `run` (the scope's answer and log)
- `report_alert` refuses non-cron runs (it looks up the newest run of the `X-Incident-ID` scope and
  takes the job name from `cron_jobs`), caps reports
  at 20 per run, and only INSERTS an event with `pending_dispatch=true`: the gateway cannot reach the
  API, so the API's `ReportedEventDispatcher` (5 s poll, conditional-update claim) runs it through
  `ProcessReportedAlert`
- `get` cuts the raw payload to 20,000 bytes of JSON (returned as a string with
  `raw_payload_truncated: true`)

## Knowledge tools (built-in, credential-less): runbooks.*, memory.*

`runbooks.search` / `runbooks.get` and `memory.search` / `memory.list` / `memory.get` /
`memory.write` / `memory.delete` (`tools/knowledge`, `registerKnowledgeTools`). They replaced the
pi-subagents; behaviour and memory rules are in the `memory-and-runbooks` rule.

- `runbooks` and `memory` are in `builtInToolNamespaces`, `credentiallessNamespaces` and the API's
  `reservedToolNamespaces`; incident runs and proposal chat get both, crons via `builtin_tools`
- reads need no run id; writes need `X-Run-ID` of a running run of the `X-Incident-ID` scope that
  is not proposal chat (`requireRun`), caps are per run id (in memory, a restart resets them)
- writes go through the gateway's own `database.DB`; the API sees them at once and refreshes
  SKILL.md manifests in `FinishRun`
