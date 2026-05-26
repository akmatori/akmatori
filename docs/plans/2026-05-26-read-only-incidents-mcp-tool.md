---
# Read-Only Incidents MCP Tool

## Overview
Add a new `incidents` tool type to the MCP gateway that exposes two operations — `incidents.list` and `incidents.get` — for read-only access to Akmatori's own incident records. Intended for cron jobs (e.g. daily digest), but we should have possibility to connect it to usual skills as well. No operator credentials needed; the tool queries the gateway's existing DB connection directly and is assigned to cron jobs or skills via the standard tool allowlist.

## Context
- Files involved:
  - `mcp-gateway/internal/database/db.go` — Incident mirror struct (extend with missing fields)
  - `mcp-gateway/internal/tools/incidents/incidents.go` — new tool package (create)
  - `mcp-gateway/internal/tools/incidents/incidents_test.go` — new test file (create)
  - `mcp-gateway/internal/tools/registry.go` — register the new tool
  - `internal/services/tool_service.go` — seed ToolType + ToolInstance
  - `mcp-gateway/internal/auth/authorizer_test.go` — add type-only incidents allowlist test
  - `internal/services/tool_service_test.go` — verify seeding
- Related patterns:
  - Other tool packages under `mcp-gateway/internal/tools/`
  - `EnsureToolTypes` pattern in `tool_service.go` lines 163-191
  - `builtInToolNamespaces` map in `registry.go` line 222
  - `registerXxxTools` + field in Registry struct pattern throughout `registry.go`
- Dependencies: `gorm.io/driver/sqlite` already in `mcp-gateway/go.mod` (for tests)

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Extend Incident mirror struct

**Files:**
- Modify: `mcp-gateway/internal/database/db.go`

- [x] Add fields to Incident struct: `SourceKind string`, `SourceUUID string`, `FullLog string`, `Response string`, `TokensUsed int`, `ExecutionTimeMs int`, `StartedAt *time.Time`, `CompletedAt *time.Time` with appropriate gorm tags matching the main DB schema
- [x] No migration change needed — gateway does not AutoMigrate; struct extension is additive
- [x] Run `make test-mcp` — must pass before Task 2

### Task 2: Implement IncidentsTool

**Files:**
- Create: `mcp-gateway/internal/tools/incidents/incidents.go`
- Create: `mcp-gateway/internal/tools/incidents/incidents_test.go`

- [ ] Define `IncidentsTool` struct with `db *gorm.DB` and `logger *log.Logger`
- [ ] Implement `NewIncidentsTool(db *gorm.DB, logger *log.Logger) *IncidentsTool`
- [ ] Implement `List(ctx, incidentID, args)`: parse optional `from`/`to` (unix timestamps), `status`, `source_kind`, `limit` (default 50, clamp to max 200), `offset`; GORM query with `Order("started_at DESC")`; return summary fields only (UUID, Title, Status, SourceKind, SourceUUID, StartedAt, CompletedAt, TokensUsed) as `{"incidents":[…],"count":N,"limit":L,"offset":O}`; ignore `incidentID`; use parameterized queries only
- [ ] Implement `Get(ctx, incidentID, args)`: require `uuid` arg; fetch full Incident; truncate FullLog to 50,000 bytes if longer; return clean "not found" on `gorm.ErrRecordNotFound`; ignore `incidentID`
- [ ] Write `incidents_test.go` using SQLite in-memory (`gorm.io/driver/sqlite`): open DB, auto-migrate `database.Incident`, insert fixtures
  - List: empty result, status filter, source_kind filter, time range filter, limit clamped to 200, offset, returns summary fields only (not FullLog/Response)
  - Get: found with all fields populated, not-found returns error, FullLog truncated at 50KB boundary
- [ ] Run `make test-mcp` — must pass before Task 3

### Task 3: Register in registry

**Files:**
- Modify: `mcp-gateway/internal/tools/registry.go`

- [ ] Import `incidents` package
- [ ] Add `incidentsTool *incidents.IncidentsTool` field to `Registry` struct
- [ ] Add `registerIncidentsTools()` method: construct `NewIncidentsTool(database.DB, r.logger)`, register `incidents.list` (all-optional schema: from, to, status, source_kind, limit, offset) and `incidents.get` (required uuid) with the server
- [ ] Call `r.registerIncidentsTools()` in `RegisterAllTools()` (no rate limiter — local DB queries)
- [ ] Add `"incidents": true` to `builtInToolNamespaces` map
- [ ] Add tests to `registry_test.go` for `registerIncidentsTools`: two tools registered, correct tool names, correct required fields in schema
- [ ] Run `make test-mcp` — must pass before Task 4

### Task 4: Seed tool type and instance in API service

**Files:**
- Modify: `internal/services/tool_service.go`

- [ ] Add `{Name: "incidents", Description: "Read-only access to Akmatori's own incidents (list and get) for digests and reporting"}` to the `toolTypes` slice in `EnsureToolTypes()`
- [ ] After the tool type loop, add FirstOrCreate logic for a credential-less ToolInstance: logical name `"incidents"`, Name `"Incidents"`, enabled, empty Settings JSONB — so it appears immediately in both the cron tool-picker and the skill tool-picker with zero operator config
- [ ] Update or add test in `internal/services/tool_service_test.go` verifying that `EnsureToolTypes()` creates the `incidents` ToolType and that the seeded instance exists with logical name `"incidents"`
- [ ] Add a test to `mcp-gateway/internal/auth/authorizer_test.go` verifying that an allowlist containing `{ToolType:"incidents"}` with no InstanceID/LogicalName authorizes calls to the `incidents` namespace (branch 6 of `IsAuthorizedFromEntries`)
- [ ] Run `make test` — must pass before Task 5

### Task 5: Verify acceptance criteria

- [ ] Run `make test-mcp`
- [ ] Run `make test`
- [ ] Run `make verify`
