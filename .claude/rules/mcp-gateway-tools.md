---
paths:
  - "**/mcp-gateway/**"
  - "**/agent-worker/src/gateway-tools.ts"
---

# MCP Gateway and tool routing

Flow: agent reads generated `SKILL.md` guidance → agent calls
`gateway_call(toolName, args, instanceHint?)` → worker sends JSON-RPC to MCP Gateway with
`X-Incident-ID` (the run's scope: incident UUID or request scope, see the `agent-runs` rule) →
gateway resolves routing, enforces allowlists, executes, returns output.

- do not teach agents to call tool implementations directly; routing is handled by logical instance
  names or instance hints
- `ToolAllowlist` on `WebSocketMessage` is intentionally NOT `omitempty` — `[]` means reject-all,
  `null` means allow-all

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

- every agent run is allowed it: the API adds a type-only `{ToolType: "events"}` entry to every
  allowlist (`withEventsTool` in `GetToolAllowlist` and `buildCronToolAllowlist`)
- `get` on a request event adds `run` (the scope's answer and log)
- `report_alert` refuses non-cron runs (it looks up the newest run of the `X-Incident-ID` scope and
  takes the job name from `cron_jobs`), caps reports
  at 20 per run, and only INSERTS an event with `pending_dispatch=true`: the gateway cannot reach the
  API, so the API's `ReportedEventDispatcher` (5 s poll, conditional-update claim) runs it through
  `ProcessReportedAlert`
- `get` cuts the raw payload to 20,000 bytes of JSON (returned as a string with
  `raw_payload_truncated: true`)
