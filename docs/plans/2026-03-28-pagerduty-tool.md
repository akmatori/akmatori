# PagerDuty MCP Gateway Tool

## Overview

Add a PagerDuty tool type to the MCP gateway, enabling the AI agent to query and manage PagerDuty incidents, services, on-call schedules, and send events. This follows the established Catchpoint/Zabbix tool pattern with caching, rate limiting, and proxy support.

## Context

- Files involved:
  - `mcp-gateway/internal/tools/pagerduty/pagerduty.go` (new - tool implementation)
  - `mcp-gateway/internal/tools/pagerduty/pagerduty_test.go` (new - tests)
  - `mcp-gateway/internal/tools/registry.go` (register tool)
  - `mcp-gateway/internal/tools/schemas.go` (settings schema)
  - `mcp-gateway/internal/database/db.go` (proxy settings field)
  - `internal/database/models_settings.go` (proxy settings field for API server)
  - `internal/handlers/api_settings_proxy.go` (proxy handler update)
  - `web/src/components/ProxySettings.tsx` (frontend toggle)
  - `web/src/types/index.ts` (frontend type)
- Related patterns: Catchpoint tool (closest match - REST API with token auth), Zabbix tool
- Dependencies: PagerDuty REST API v2, PagerDuty Events API v2

## PagerDuty API Functions

### Read-only investigation
- `get_incidents` - List incidents with filters (status, urgency, service, date range)
- `get_incident` - Get incident details by ID
- `get_incident_notes` - Get notes/timeline for an incident
- `get_incident_alerts` - Get alerts grouped under an incident
- `get_services` - List services
- `get_on_calls` - Get current on-call users (by schedule or escalation policy)
- `get_escalation_policies` - List escalation policies
- `list_recent_changes` - Recent changes across services

### Actions
- `acknowledge_incident` - Acknowledge an incident
- `resolve_incident` - Resolve an incident
- `reassign_incident` - Reassign to a different user/escalation policy
- `add_incident_note` - Add a note to an incident

### Event management
- `send_event` - Send trigger/acknowledge/resolve events via Events API v2

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Follow the Catchpoint tool pattern exactly
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Tool schema and registry scaffolding

**Files:**
- Modify: `mcp-gateway/internal/tools/schemas.go`
- Modify: `mcp-gateway/internal/tools/registry.go`

- [x] Add `getPagerDutySchema()` to schemas.go with settings: `pagerduty_api_token` (secret, required), `pagerduty_url` (default `https://api.pagerduty.com`), `pagerduty_verify_ssl`, `pagerduty_timeout`, and function list
- [x] Add `"pagerduty": getPagerDutySchema()` to `GetToolSchemas()` map
- [x] Add PagerDuty rate limiter constants (10 req/sec, burst 20)
- [x] Add `pagerdutyTool` and `pagerdutyLimit` fields to Registry struct
- [x] Add `registerPagerDutyTools()` call in `RegisterAllTools()` (stub for now)
- [x] Add Stop() cleanup for pagerdutyTool
- [x] Write tests for schema validation (settings schema has required fields, functions list is populated)
- [x] Run `make test-mcp` - must pass before task 2

### Task 2: Core PagerDuty tool - read-only operations

**Files:**
- Create: `mcp-gateway/internal/tools/pagerduty/pagerduty.go`
- Create: `mcp-gateway/internal/tools/pagerduty/pagerduty_test.go`

- [x] Create PagerDutyTool struct with configCache, responseCache, rateLimiter, logger (same pattern as Catchpoint)
- [x] Implement getConfig() with credential resolution and proxy support
- [x] Implement doRequest() with rate limiting, caching, proxy handling, and SSL verification
- [x] Implement cachedGet() helper for read paths
- [x] Implement read-only functions: GetIncidents, GetIncident, GetIncidentNotes, GetIncidentAlerts, GetServices, GetOnCalls, GetEscalationPolicies, ListRecentChanges
- [x] Each function validates required parameters and builds query params
- [x] Write tests using httptest server for all read-only operations (success, error, parameter validation)
- [x] Run `make test-mcp` - must pass before task 3

### Task 3: Action and event operations

**Files:**
- Modify: `mcp-gateway/internal/tools/pagerduty/pagerduty.go`
- Modify: `mcp-gateway/internal/tools/pagerduty/pagerduty_test.go`

- [ ] Implement AcknowledgeIncident, ResolveIncident, ReassignIncident, AddIncidentNote (PUT/POST to REST API v2, no response caching)
- [ ] Implement SendEvent for Events API v2 (POST to events.pagerduty.com, requires routing_key parameter)
- [ ] Write tests for all action/event operations (success, validation errors, API errors)
- [ ] Run `make test-mcp` - must pass before task 4

### Task 4: Register tools in the MCP gateway

**Files:**
- Modify: `mcp-gateway/internal/tools/registry.go`

- [ ] Implement `registerPagerDutyTools()` with all tool registrations (InputSchema definitions for each function)
- [ ] Register all read-only tools: pagerduty.get_incidents, pagerduty.get_incident, pagerduty.get_incident_notes, pagerduty.get_incident_alerts, pagerduty.get_services, pagerduty.get_on_calls, pagerduty.get_escalation_policies, pagerduty.list_recent_changes
- [ ] Register action tools: pagerduty.acknowledge_incident, pagerduty.resolve_incident, pagerduty.reassign_incident, pagerduty.add_incident_note
- [ ] Register event tool: pagerduty.send_event
- [ ] Write tests verifying tool registration count and names
- [ ] Run `make test-mcp` - must pass before task 5

### Task 5: Proxy settings support

**Files:**
- Modify: `mcp-gateway/internal/database/db.go`
- Modify: `internal/database/models_settings.go`
- Modify: `internal/handlers/api_settings_proxy.go`
- Modify: `web/src/components/ProxySettings.tsx`
- Modify: `web/src/types/index.ts`

- [ ] Add `PagerDutyEnabled bool` field to ProxySettings in both database model files (mcp-gateway and API server)
- [ ] Update proxy settings handler to include pagerduty_enabled in read/write
- [ ] Add PagerDuty toggle to the frontend ProxySettings component (use Bell icon from lucide-react)
- [ ] Add pagerduty_enabled to the frontend ProxySettingsUpdate type
- [ ] Write tests for proxy settings handler including pagerduty_enabled field
- [ ] Run `make test-mcp` and `make test` - must pass before task 6

### Task 6: Verify acceptance criteria

- [ ] Run full test suite: `make test-all`
- [ ] Run linter: `go vet ./...`
- [ ] Verify MCP gateway test coverage for pagerduty package meets 80%+

### Task 7: Update documentation

- [ ] Update CLAUDE.md: add PagerDuty to tool references (schemas.go tool list, proxy settings struct, tool implementation reference table)
- [ ] Move this plan to `docs/plans/completed/`
