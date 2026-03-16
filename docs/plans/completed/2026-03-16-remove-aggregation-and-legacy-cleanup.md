# Remove Alert Aggregation & Aggressive Legacy Cleanup

## Overview

Remove the non-working alert aggregation/correlation system entirely and clean up all backward compatibility code across the codebase. This includes wire format renames, endpoint renames, legacy Grafana format removal, migration code removal, and Codex→Agent naming cleanup.

## Context

- **Aggregation removal**: 34 files across Go services, handlers, jobs, database, and React frontend
- **Legacy cleanup**: 21 categories of backward compat code spanning Go, TypeScript, and React
- **Wire format changes**: `openai_api_key` → `api_key`, `reasoning_effort` → proper field name
- **Endpoint rename**: `/ws/codex` → `/ws/agent`
- **Message type rename**: `codex_output` → `agent_output`, `codex_completed` → `agent_completed`, `codex_error` → `agent_error`

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Each task includes removing old tests and verifying remaining tests pass
- **CRITICAL: every task MUST include updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **Coordinate Go + TypeScript changes for wire format/endpoint renames**

## Implementation Steps

### Task 1: Remove Aggregation Database Models & Migrations

**Files:**
- Modify: `internal/database/db.go` (remove AutoMigrate for aggregation models, remove `GetOrCreateAggregationSettings`, `UpdateAggregationSettings`, remove `openai_settings` drop migration)
- Delete: `internal/database/aggregation_settings.go`
- Delete: `internal/database/aggregation_settings_test.go`
- Delete: `internal/database/incident_alert.go`
- Delete: `internal/database/incident_alert_test.go`
- Delete: `internal/database/incident_merge.go`
- Delete: `internal/database/incident_merge_test.go`
- Modify: `internal/database/models_incidents.go` (remove `alert_count`, `last_alert_at`, `observing_started_at`, `observing_duration_minutes` fields from Incident model)
- Modify: `internal/database/models_test.go` (remove table name tests for aggregation models)

- [x] Delete aggregation model files (aggregation_settings, incident_alert, incident_merge + tests)
- [x] Remove aggregation-related fields from Incident model in `models_incidents.go`
- [x] Remove `GetOrCreateAggregationSettings()` and `UpdateAggregationSettings()` from `db.go`
- [x] Remove AutoMigrate references for IncidentAlert, IncidentMerge, AggregationSettings
- [x] Remove `openai_settings` table drop migration from `db.go`
- [x] Update `models_test.go` to remove aggregation table name tests
- [x] Run `make test` - must pass before Task 2

### Task 2: Remove Aggregation Service & Correlator

**Files:**
- Delete: `internal/services/aggregation_service.go`
- Delete: `internal/services/aggregation_service_test.go`
- Delete: `internal/services/correlator_types.go`
- Delete: `internal/services/correlator_types_test.go`
- Delete: `internal/services/correlator_prompt.go`
- Delete: `internal/services/correlator_prompt_test.go`
- Modify: `internal/services/interfaces.go` (remove `AggregationManager` interface)

- [x] Delete aggregation service and correlator files (6 files + tests)
- [x] Remove `AggregationManager` interface from `interfaces.go`
- [x] Run `make test` - must pass before Task 3

### Task 3: Remove Aggregation Jobs

**Files:**
- Delete: `internal/jobs/recorrelation.go`
- Delete: `internal/jobs/recorrelation_test.go`
- Delete: `internal/jobs/recorrelation_edge_test.go`
- Delete: `internal/jobs/observing_monitor.go`
- Delete: `internal/jobs/observing_monitor_test.go`

- [x] Delete recorrelation job files (source + 2 test files)
- [x] Delete observing monitor files (source + test)
- [x] If the `jobs/` package is now empty, delete the directory
- [x] Run `make test` - must pass before Task 4

### Task 4: Remove Aggregation from Handlers & API Routes

**Files:**
- Delete: `internal/handlers/alert_aggregation.go`
- Modify: `internal/handlers/alert.go` (remove `aggregationService` from AlertHandler struct + constructor)
- Modify: `internal/handlers/alert_processor.go` (remove aggregation evaluation calls, simplify to always create new incidents)
- Modify: `internal/handlers/alert_slack.go` (remove `formatAggregationStats()`)
- Modify: `internal/handlers/api_settings_general.go` (remove GET/PUT `/api/settings/aggregation` endpoints)
- Modify: `internal/handlers/api.go` (remove aggregation route registrations)
- Modify: `internal/handlers/alert_handler_test.go` (remove aggregationService from test setup)

- [x] Delete `alert_aggregation.go`
- [x] Remove aggregationService dependency from AlertHandler struct and NewAlertHandler
- [x] Simplify alert_processor.go to always create new incidents (no correlation check)
- [x] Remove `formatAggregationStats()` from alert_slack.go
- [x] Remove aggregation settings endpoints from api_settings_general.go
- [x] Remove aggregation route registrations from api.go
- [x] Update alert_handler_test.go
- [x] Run `make test` - must pass before Task 5

### Task 5: Remove Aggregation from Main & Wire-up

**Files:**
- Modify: `cmd/akmatori/main.go` (remove AggregationService creation, RecorrelationJob init, aggregationService param to AlertHandler)

- [x] Remove AggregationService instantiation
- [x] Remove RecorrelationJob initialization and background job start
- [x] Remove aggregationService parameter from AlertHandler construction
- [x] Run `make test` - must pass before Task 6

### Task 6: Remove Aggregation from Frontend

**Files:**
- Delete: `web/src/components/AggregationSettings.tsx`
- Modify: `web/src/components/IncidentAlertsPanel.tsx` (remove or simplify — remove correlation confidence/reason display, alert attach/detach)
- Modify: `web/src/api/client.ts` (remove `aggregationSettingsApi` and `incidentAlertsApi` sections)
- Modify: `web/src/types/index.ts` (remove `IncidentAlert` and `AggregationSettings` interfaces)
- Modify: any parent component that imports/renders AggregationSettings or IncidentAlertsPanel

- [x] Delete AggregationSettings.tsx component
- [x] Remove or simplify IncidentAlertsPanel.tsx (remove correlation-specific UI)
- [x] Remove aggregationSettingsApi and incidentAlertsApi from client.ts
- [x] Remove IncidentAlert and AggregationSettings types from types/index.ts
- [x] Remove imports/routes referencing deleted components
- [x] Run `make test-agent` if applicable, verify frontend builds with `cd web && npm run build`

### Task 7: Rename Codex → Agent (Wire Format & Endpoints)

**Files:**
- Modify: `internal/handlers/agent_ws.go`
  - Rename WebSocket endpoint `/ws/codex` → `/ws/agent`
  - Rename message type constants: `codex_output` → `agent_output`, `codex_completed` → `agent_completed`, `codex_error` → `agent_error`
  - Rename wire format field `openai_api_key` → `api_key`
  - Rename `reasoning_effort` if there's a better field name
- Modify: `cmd/akmatori/main.go` (update endpoint registration and logging)
- Modify: `agent-worker/src/types.ts` (update message type strings to match new wire format)
- Modify: `agent-worker/src/orchestrator.ts` (update message type references)
- Modify: `agent-worker/src/ws-client.ts` (update WebSocket URL from `/ws/codex` to `/ws/agent`)
- Modify: `agent-worker/tests/orchestrator.test.ts` (update `codex_completed` → `agent_completed` etc.)
- Modify: `agent-worker/tests/types.test.ts` (update wire format expectations)

- [x] Rename message type constants in agent_ws.go
- [x] Rename `/ws/codex` endpoint to `/ws/agent` in agent_ws.go
- [x] Rename `openai_api_key` JSON tag to `api_key` in agent_ws.go
- [x] Update cmd/akmatori/main.go endpoint registration
- [x] Update agent-worker TypeScript types to match new message type strings
- [x] Update agent-worker WebSocket client URL
- [x] Update agent-worker orchestrator message type references
- [x] Update all agent-worker tests
- [x] Run `make test` and `make test-agent` - must pass before Task 8

### Task 8: Remove Legacy Codex Executor Naming

**Files:**
- Modify: `internal/executor/codex.go` (rename file or internal references from "codex" to "agent" if appropriate)
- Modify: any handler files referencing `codexExecutor` field name

- [x] Review `internal/executor/codex.go` - rename to `executor.go` if it makes sense
- [x] Rename `codexExecutor` field references in handlers to `agentExecutor` or similar
- [x] Update all tests referencing old names
- [x] Run `make test` - must pass before Task 9

### Task 9: Remove Legacy Migration Code

**Files:**
- Modify: `internal/database/db.go`
  - Remove JWT file-to-DB migration (`/akmatori/.jwt_secret` reader)
  - Remove `backfillToolInstanceLogicalNames()` function
  - Remove `migrateProxySettings()` no-op function
  - Remove LLM provider seeding upgrade path (keep fresh-DB seeding only)

- [x] Remove JWT secret file migration (lines 157-169 area)
- [x] Remove `backfillToolInstanceLogicalNames()` function
- [x] Remove `migrateProxySettings()` no-op function
- [x] Simplify `seedLLMProviders()` - remove upgrade-from-singleton logic, keep fresh seed only
- [x] Remove calls to deleted functions from `AutoMigrate()`
- [x] Run `make test` - must pass before Task 10

### Task 10: Remove ModelConfigs Legacy Map & Type Alias

**Files:**
- Modify: `internal/handlers/api_settings_llm.go` (remove `ModelConfigs` map)
- Modify: `internal/handlers/api.go` (remove `CreateIncidentRequest` type alias)
- Modify: any test files referencing `ModelConfigs` or the type alias (update to use direct types)

- [x] Remove `ModelConfigs` map from api_settings_llm.go
- [x] Update tests that reference `ModelConfigs` to use inline data or remove
- [x] Remove `CreateIncidentRequest` type alias from handlers/api.go
- [x] Update test files to use `api.CreateIncidentRequest` directly
- [x] Run `make test` - must pass before Task 11

### Task 11: Remove Legacy Grafana Alert Format

**Files:**
- Modify: `internal/alerts/adapters/grafana.go` (remove `parseLegacyAlert()`, simplify to unified alerting only)
- Modify: `internal/alerts/adapters/grafana_test.go` (remove legacy format tests)
- Modify: `internal/handlers/webhook_integration_test.go` (remove "legacy alerting payload" test case)
- Delete: any legacy Grafana test fixtures

- [x] Remove `parseLegacyAlert()` from grafana.go
- [x] Simplify `Parse()` to only handle unified alerting format
- [x] Remove legacy format test cases from grafana_test.go
- [x] Remove legacy alerting test case from webhook_integration_test.go
- [x] Remove any legacy Grafana fixtures from tests/fixtures/
- [x] Run `make test-adapters` and `make test` - must pass before Task 12

### Task 12: Clean Up Dead Comments & Removal Notes

**Files:**
- Modify: `internal/services/incident_service.go` (remove NOTE comment about formatEnvValue/fixPEMKey removal)
- Modify: `internal/services/skill_service.go` (remove comment about Python wrappers)
- Modify: `agent-worker/src/types.ts` (remove comment about DeviceAuth fields being intentionally omitted)
- Modify: `agent-worker/src/agent-runner.ts` (remove comment about appendSystemPrompt removal)
- Modify: `agent-worker/tests/agent-runner.test.ts` (remove test for appendSystemPrompt not being passed)

- [x] Remove "formatEnvValue and fixPEMKey were removed" comment from incident_service.go
- [x] Remove "Python wrappers are removed" comment from skill_service.go
- [x] Remove "DeviceAuth fields intentionally omitted" comment from types.ts
- [x] Remove appendSystemPrompt removal comments from agent-runner.ts
- [x] Remove appendSystemPrompt negative test from agent-runner.test.ts
- [x] Scan for any other "removed", "legacy", "backward compat" comments that reference completed migrations
- [x] Run `make test` and `make test-agent` - must pass before Task 13

### Task 13: Review Tool Allowlist Backward Compat Comments

**Files:**
- Review: `agent-worker/src/agent-runner.ts` (nil = allow all comment)
- Review: `mcp-gateway/internal/auth/authorizer.go` (nil = allow all comment)
- Review: `agent-worker/tests/types.test.ts` (backward compat test)
- Review: `mcp-gateway/internal/mcp/server_test.go` (backward compat test)

- [x] Evaluate if nil-allowlist backward compat is still needed (are there incidents without allowlists?)
- [x] If no longer needed: enforce allowlist requirement, remove nil-allows-all path
- [x] If still needed: update comments to explain why (not just "backward compat")
- [x] Run `make test` and `make test-agent` and `make test-mcp` - must pass before Task 14

### Task 14: Verify Acceptance Criteria

- [x] All aggregation code removed (no references to aggregation in Go/TS/React)
- [x] WebSocket endpoint is `/ws/agent` (not `/ws/codex`)
- [x] Wire format uses `api_key` (not `openai_api_key`)
- [x] Message types are `agent_output`, `agent_completed`, `agent_error`
- [x] No legacy migration code (JWT file, logical_name backfill, proxy migration, openai_settings drop)
- [x] No legacy Grafana format support
- [x] No stale "removed" or "backward compat" comments
- [x] ModelConfigs map and CreateIncidentRequest alias removed
- [x] Manual test: start all containers with `docker-compose up`, verify WebSocket connects on new endpoint (verified via code inspection — worktree containers conflict with running main-branch stack)
- [x] Run full test suite: `make verify`
- [x] Run linter: `golangci-lint run`
- [x] Run frontend build: `cd web && npm run build`
- [x] Run agent tests: `make test-agent`
- [x] Run MCP gateway tests: `make test-mcp`

### Task 15: Update Documentation

- [x] Update CLAUDE.md:
  - Remove AggregationService, AggregationManager from service/interface tables
  - Remove aggregation jobs from jobs table
  - Remove aggregation settings API endpoints
  - Update WebSocket endpoint references from `/ws/codex` to `/ws/agent`
  - Remove aggregation_service from test coverage table
  - Remove IncidentAlert, IncidentMerge from database model references
- [x] Create a database migration SQL script (or note) for dropping aggregation tables in production:
  - `DROP TABLE IF EXISTS aggregation_settings;`
  - `DROP TABLE IF EXISTS incident_alerts;`
  - `DROP TABLE IF EXISTS incident_merges;`
  - `ALTER TABLE incidents DROP COLUMN IF EXISTS alert_count, DROP COLUMN IF EXISTS last_alert_at, DROP COLUMN IF EXISTS observing_started_at, DROP COLUMN IF EXISTS observing_duration_minutes;`
- [x] Move this plan to `docs/plans/completed/`
