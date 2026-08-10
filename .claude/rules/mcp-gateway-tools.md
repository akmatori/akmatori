---
paths:
  - "**/mcp-gateway/**"
  - "**/agent-worker/src/gateway-tools.ts"
---

# MCP Gateway and tool routing

Flow: agent reads generated `SKILL.md` guidance → agent calls
`gateway_call(toolName, args, instanceHint?)` → worker sends JSON-RPC to MCP Gateway with
`X-Incident-ID` → gateway resolves routing, enforces allowlists, executes, returns output.

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
- `List` returns summary fields only (no `full_log`/`response`); `Get` returns the full record with
  `full_log` truncated to 50,000 bytes
- when adding another credential-less built-in tool, follow the same seed pattern in
  `EnsureToolTypes()` and the same `registerXxxTools()` pattern in `registry.go`

`docs/TOOL_ARCHITECTURE.md` still describes the retired Python tool path; live routing is
gateway-first via `gateway_call`.
