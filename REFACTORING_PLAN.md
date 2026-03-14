# Akmatori Refactoring Plan

**Date**: 2026-03-14
**Status**: Proposed

---

## Executive Summary

This plan addresses code quality issues identified across the Akmatori codebase: duplicated alert processing logic, oversized files, tight handler coupling, a global database instance, and low test coverage in critical packages. The refactoring is organized into 6 phases, ordered by impact and risk.

---

## Phase 1: Extract Duplicate Alert Processing (Critical)

**Problem**: `internal/handlers/alert.go` (1,272 lines) contains two near-identical alert processing paths — `processAlert()` (webhook) and `ProcessAlertFromSlackChannel()` (Slack) — sharing ~90% of their logic: aggregation decision, incident creation, alert attachment, Slack notification, and agent dispatch.

**Changes**:

1. **Create `internal/services/incident_creator.go`** — Extract shared incident-creation logic into an `IncidentCreator` service:
   - `CreateOrAttachAlert(ctx, alert, source) (*Incident, error)` — aggregation check → create or attach → return incident
   - `NotifySlack(ctx, incident, alert) error` — post/update Slack thread
   - `DispatchAgent(ctx, incident) error` — trigger agent investigation

2. **Slim down `alert.go`** — Both `processAlert()` and `ProcessAlertFromSlackChannel()` become thin adapters that parse input, call `IncidentCreator`, and return HTTP/Slack responses. Target: reduce to ~500 lines.

3. **Extract alert-attachment logic** (lines 234–274 and 735–777) into `AggregationService.AttachAlertToIncident()` to eliminate the second copy.

**Files touched**:
- `internal/handlers/alert.go` (edit)
- `internal/services/incident_creator.go` (new)
- `internal/services/aggregation_service.go` (edit)

**Tests**: Add unit tests for `IncidentCreator` with mocked DB and Slack. Existing handler integration tests must still pass.

**Risk**: Medium — core alert flow. Run `make verify` + manual webhook test after.

---

## Phase 2: Reduce AlertHandler Coupling (High)

**Problem**: `AlertHandler` struct depends on 8 injected services (config, slackManager, codexExecutor, agentWSHandler, skillService, alertService, channelResolver, aggregationService), making it hard to test and modify.

**Changes**:

1. **Introduce `AlertDeps` facade**:
   ```go
   type AlertDeps struct {
       IncidentCreator  *services.IncidentCreator  // from Phase 1
       AlertService     *services.AlertService
       AgentDispatcher  AgentDispatcher             // interface over codex/agent WS
   }
   ```

2. **Define `AgentDispatcher` interface**:
   ```go
   type AgentDispatcher interface {
       StartInvestigation(ctx context.Context, incident *database.Incident) error
   }
   ```
   Both `CodexWSHandler` and `AgentWSHandler` implement this, removing direct handler-to-handler coupling.

3. **Reduce AlertHandler fields** from 8 to 3–4 by routing through `AlertDeps` and `IncidentCreator`.

**Files touched**:
- `internal/handlers/alert.go` (edit)
- `internal/handlers/agent_dispatcher.go` (new — interface + adapter)
- `cmd/akmatori/main.go` (edit — update wiring)

**Tests**: Existing integration tests pass. Add unit tests using mock `AgentDispatcher`.

**Risk**: Low — internal restructuring only, no API changes.

---

## Phase 3: Split Oversized Files (Medium)

**Problem**: Several files exceed 500 lines with multiple responsibilities.

### 3a. Split `internal/handlers/slack.go` (909 lines)

Split into:
| New File | Responsibility | ~Lines |
|----------|---------------|--------|
| `slack_events.go` | Socket Mode event routing, message classification | ~300 |
| `slack_alerts.go` | Alert-channel message handling, extraction, incident creation | ~350 |
| `slack_threads.go` | Thread reply handling, @mention processing | ~250 |

### 3b. Split `internal/services/skill_service.go` (1,129 lines)

Split into:
| New File | Responsibility | ~Lines |
|----------|---------------|--------|
| `skill_service.go` | Skill CRUD, lifecycle, validation | ~400 |
| `skill_workspace.go` | Filesystem I/O, SKILL.md generation, workspace setup | ~400 |
| `incident_manager.go` | Incident directory management, context preparation | ~300 |

### 3c. Split `internal/handlers/api_settings.go` (584 lines)

Split into:
| New File | Responsibility | ~Lines |
|----------|---------------|--------|
| `api_settings_llm.go` | LLM provider settings endpoints | ~200 |
| `api_settings_slack.go` | Slack settings endpoints | ~150 |
| `api_settings_general.go` | Proxy, aggregation, API key settings | ~230 |

**Risk**: Low — file reorganization only, no logic changes. Run `make verify` after each split.

---

## Phase 4: Eliminate Global Database Instance (Medium)

**Problem**: `var DB *gorm.DB` in `database/db.go` is a package-level global accessed via `database.GetDB()` throughout all services. This makes testing harder (no DB isolation) and hides dependencies.

**Changes**:

1. **Pass `*gorm.DB` via constructor injection** to all services:
   ```go
   // Before
   func NewAlertService() *AlertService { ... database.GetDB() ... }

   // After
   func NewAlertService(db *gorm.DB) *AlertService { ... }
   ```

2. **Update `cmd/akmatori/main.go`** — create DB once, pass to all service constructors.

3. **Keep `database.GetDB()` temporarily** with a deprecation comment for any remaining callers during transition.

4. **Update tests** — inject test DB or mock where needed.

**Services to update** (10 files):
- `AlertService`, `AggregationService`, `SkillService`, `ToolService`, `ContextService`, `RunbookService`, `TitleGenerator`, plus handler constructors that access DB directly.

**Risk**: Medium — wide-reaching but mechanical change. Each service can be migrated independently. Run tests after each.

---

## Phase 5: Implement Missing Alert Resolution (Medium)

**Problem**: Two TODO items in `alert.go` indicate incomplete functionality:
1. Line 209: "Handle resolved alerts (update incident_alerts status)" — resolved alerts are received but not processed.
2. Line 201: "Call Codex correlator" — all alerts create new incidents; no correlation.

**Changes**:

### 5a. Resolved Alert Handling
1. Add `ProcessResolvedAlert(ctx, alert)` to `IncidentCreator`:
   - Find matching `IncidentAlert` by alert fingerprint/source
   - Update status to `resolved`
   - If all alerts on an incident are resolved, update incident status to `resolved`
   - Post resolution update to Slack thread

2. Route resolved alerts from webhook handler based on alert status field.

### 5b. Alert Correlation (stub)
1. Create `internal/services/correlator.go` with interface:
   ```go
   type Correlator interface {
       ShouldAttach(ctx context.Context, alert *database.IncidentAlert) (*database.Incident, bool, error)
   }
   ```
2. Implement `SimpleCorrelator` using time-window + host matching (no LLM yet).
3. Wire into `IncidentCreator` to check before creating new incidents.

**Tests**: Unit tests for resolution state machine. Integration test for webhook → resolve flow.

**Risk**: Medium — new behavior. Feature-flag with `AggregationSettings.enabled` to allow rollback.

---

## Phase 6: Increase Test Coverage (Ongoing)

**Problem**: Critical packages have low coverage:
| Package | Current | Target |
|---------|---------|--------|
| `handlers` | 10.2% | 60% |
| `services` | 28.8% | 70% |
| `database` | 20.2% | 60% |
| `slack` | 32.3% | 60% |
| `extraction` | 36.0% | 60% |

**Approach**:

### 6a. Handler Tests
- Use `testhelpers.NewHTTPTestContext` for all handler endpoints
- Mock services via interfaces (introduced in Phase 2)
- Priority: `alert.go`, `api_incidents.go`, `api_skills.go`

### 6b. Service Tests
- Inject test `*gorm.DB` (from Phase 4) with SQLite or test PostgreSQL
- Mock external calls (LLM APIs, filesystem)
- Priority: `SkillService`, `AggregationService`, `AlertService`

### 6c. Slack Integration Tests
- Mock `slack.Client` and Socket Mode
- Test event routing, alert extraction, thread handling
- Use `testhelpers.ConcurrentTest` for race condition coverage

**Risk**: Low — additive only.

---

## Execution Order & Dependencies

```
Phase 1 (alert dedup)
  └──> Phase 2 (reduce coupling) — depends on IncidentCreator from Phase 1
         └──> Phase 5 (resolution) — depends on interfaces from Phase 2

Phase 3 (file splits) — independent, can run in parallel with Phase 1-2
Phase 4 (DB injection) — independent, can start after Phase 2
Phase 6 (tests) — ongoing, accelerates after Phases 1-4
```

**Recommended timeline**:
1. Phase 1 + Phase 3 (parallel)
2. Phase 2
3. Phase 4
4. Phase 5
5. Phase 6 (continuous)

---

## Verification Checklist

After each phase:
- [ ] `make verify` passes (go vet + all tests)
- [ ] `make test-all` passes (including agent-worker)
- [ ] No new `golangci-lint` warnings
- [ ] Docker containers build successfully
- [ ] Manual smoke test: send test alert via webhook → incident created → agent dispatched

---

## Out of Scope

- Frontend (React/web) refactoring
- Agent-worker (TypeScript) restructuring
- MCP Gateway refactoring (SSH host key validation is a separate security task)
- Dependency injection framework adoption (Wire/Fx) — evaluate after Phase 4
- Database schema changes or migrations
