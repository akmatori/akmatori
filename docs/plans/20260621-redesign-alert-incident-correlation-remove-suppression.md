# Redesign Alert→Incident Correlation; Remove Suppression

## Overview

Replace the two-gate (correlation + suppression) alert processing flow with a single simplified
model. Alerts become a first-class `alerts` table. Correlation uses one LLM call over open +
recently-completed (monitor-mode) incidents. Monitor mode is set when an investigation completes
(`monitor_until = CompletedAt + window`, default 60 min). Suppression backend,
suppression/signature UI, recurrence stats panel, and all correlation/suppression tuning knobs are
removed.

## Context

- Files involved: `internal/handlers/alert_processor.go`, `internal/handlers/alert.go`,
  `internal/services/alert_correlator.go`, `internal/services/alert_suppressor.go`,
  `internal/services/incident_service.go`, `internal/services/memory_service.go`,
  `internal/services/interfaces.go`, `internal/database/models_incidents.go`,
  `internal/database/models_settings.go`, `internal/database/models_context.go`,
  `internal/database/db.go`, `internal/api/types.go`,
  `internal/handlers/api_settings_general.go`, `internal/handlers/api_memories.go`,
  `internal/handlers/api_recurrence_stats.go`, `cmd/akmatori/main.go`,
  `web/src/` (types, api client, settings components)
- Related patterns: singleflight burst dedup, fail-open gates, live-config reads from
  GeneralSettings, interfaces.go dependency injection, partial-unique indexes via raw SQL
- Dependencies: PostgreSQL partial-unique index cannot be expressed via GORM struct tags — raw SQL
  in db.go

## Development Approach

- Regular (code first, tests after each task)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: New Alert model + Incident schema changes + migration

**Files:**
- Create: `internal/database/models_alerts.go`
- Modify: `internal/database/models_incidents.go`
- Modify: `internal/database/db.go`

- [x] Create `models_alerts.go` with `Alert` struct (UUID, IncidentUUID, Status, Fingerprint,
      SourceUUID, SourceFingerprint, AlertName, TargetHost, FiredAt, ResolvedAt, RawPayload,
      CreatedAt, UpdatedAt), `AlertStatus` string type with constants `AlertStatusFiring = "firing"`
      and `AlertStatusResolved = "resolved"`, and `TableName() string` returning `"alerts"`
- [x] Add `IncidentStatusMonitor IncidentStatus = "monitor"` to the incident status constants in
      `models_incidents.go`
- [x] Add `MonitorUntil *time.Time` and `ResolvedAt *time.Time` to Incident struct; remove
      `CorrelatedCount int` field
- [x] Add `&Alert{}` to the `AutoMigrate` call in `db.go`; remove `&AlertCorrelationLog{}` and
      `&AlertSuppressionLog{}` from the list (keep physical tables — non-destructive)
- [x] Remove the `ensureMemoriesSuppressScopeIndex(db)` call from `runMigrations` in `db.go`
- [x] Add post-migrate raw SQL in `db.go`: composite indexes on `(incident_uuid, status)`,
      `(source_uuid, source_fingerprint, status)`, `(source_uuid, fingerprint, status, fired_at)`
      on the `alerts` table (all `IF NOT EXISTS`)
- [x] Add post-migrate raw SQL: `CREATE UNIQUE INDEX IF NOT EXISTS uniq_firing_alert ON alerts
      (source_uuid, source_fingerprint) WHERE status='firing' AND source_fingerprint <> ''`
- [x] Add `migrateBackfillAlerts(db)` function: for each `source_kind='alert'` incident that has
      no rows in `alerts` yet, insert one `alerts` row extracting `fingerprint`, `source_uuid`,
      `source_fingerprint`, `alert_name`, `target_host`, `fired_at=started_at`, `raw_payload` from
      Context JSONB; skip incidents whose UUID appears in `alert_suppression_logs`; for
      already-completed/failed incidents also set
      `monitor_until = completed_at + 60 * interval '1 minute'` on the incident row; idempotent
      via `INSERT ... ON CONFLICT DO NOTHING`
- [x] Write tests: `Alert` model constants, `IncidentStatusMonitor` constant exists
- [x] Run `make test` — must pass before task 2

### Task 2: Settings simplification

**Files:**
- Modify: `internal/database/models_settings.go`
- Modify: `internal/api/types.go`
- Modify: `internal/handlers/api_settings_general.go`
- Modify: `web/src/types/index.ts`

- [x] Add `AlertMonitorWindowMinutes *int` field (`gorm:"default:null"`) to `GeneralSettings`
      struct; add helper `GetAlertMonitorWindow() time.Duration` that returns 60 min when nil
- [x] Remove the five correlation knob fields from `GeneralSettings` struct:
      `AlertCorrelationWindowMinutes`, `AlertCorrelationThreshold`,
      `AlertCorrelationMaxCandidates`, `AlertCorrelationLongWindowDays`,
      `AlertCorrelationFingerprintWindowMinutes` (do NOT add DDL to drop the columns —
      non-destructive)
- [x] Remove both suppression fields from `GeneralSettings` struct: `AlertSuppressionEnabled`,
      `AlertSuppressionThreshold`
- [x] Trim `UpdateGeneralSettingsRequest` in `api/types.go` to three fields: `BaseURL`,
      `AlertCorrelationEnabled`, `AlertMonitorWindowMinutes`
- [x] Update `applyGeneralSettingsDefaults` and GET/PUT handler in `api_settings_general.go`:
      hydrate `AlertMonitorWindowMinutes` with effective default 60 in GET; apply it on PUT
- [x] In `web/src/types/index.ts`: remove the five correlation knob fields + two suppression fields
      from `GeneralSettings` and `GeneralSettingsUpdate`; add `alert_monitor_window_minutes: number`
      to both; remove `RecurrenceStats`, `FingerprintGroup`, `GateHitRates`, `GateRate` types
- [x] Write tests: GET /api/settings/general returns `alert_monitor_window_minutes`, PUT updates it
- [x] Run `make test` + `make test-web` — must pass before task 3

### Task 3: Alert correlator simplification

**Files:**
- Modify: `internal/services/alert_correlator.go`

- [x] Replace `CorrelationConfig` struct: keep `Enabled bool` only; remove all other fields
- [x] Add package-level consts: `correlationMaxCandidates = 25`, `correlationThreshold = 0.7`
- [x] Collapse `fetchCandidates` to a single DB query: `source_kind='alert' AND (status IN
      ('pending','running','diagnosed') OR (status='monitor' AND monitor_until >= NOW()))`,
      `ORDER BY started_at DESC LIMIT correlationMaxCandidates`
- [x] Update `loadConfig` to read only `AlertCorrelationEnabled` from `GeneralSettings`
- [x] Remove `IsLongWindowMatch` from `CorrelationVerdict` struct; keep `Correlated`,
      `IncidentUUID`, `Confidence`, `Reasoning`
- [x] Remove the `[KNOWN OPEN ISSUE]` long-window labeling from prompt construction; keep
      hallucination guard and JSON parse unchanged
- [x] Update `alert_correlator_test.go`: remove long-window test cases and multi-query tests; add
      tests for single-predicate (monitor incident within window → candidate; expired monitor_until
      → not a candidate); remove any reference to `IsLongWindowMatch`
- [x] Run `make test` — must pass before task 4

### Task 4: Incident service refactor

**Files:**
- Modify: `internal/services/incident_service.go`
- Modify: `internal/services/interfaces.go`
- Modify: `internal/handlers/api_incidents.go`

- [x] Add `LinkAlertToIncident(ctx, incidentUUID string, sourceUUID string, alert NormalizedAlert)
      error`: inserts an `alerts` row (status=firing) linked to the incident; if incident is in
      `monitor` status, extends `monitor_until = now + window`
- [x] Add `InsertFiringAlert(ctx, incidentUUID string, sourceUUID string, alert NormalizedAlert)
      error`: inserts the initial `alerts` row (status=firing) for a newly spawned incident
- [x] In `UpdateIncidentComplete`: after writing `status=completed/failed`, if
      `incident.SourceKind == "alert"`, set `MonitorUntil = &completedAt + GetAlertMonitorWindow()`
      and `status = monitor`; read window live from `GeneralSettings` via `GetAlertMonitorWindow()`
- [x] Remove `AppendCorrelatedAlert` method (replaced by `LinkAlertToIncident`; `alert.go` updated)
- [x] Remove `RecordSuppressedIncident` method (kept in interface/impl until Task 5 cleans callers)
- [x] Update `SkillIncidentManager` interface in `interfaces.go`: add `LinkAlertToIncident` and
      `InsertFiringAlert`; remove `AppendCorrelatedAlert`
- [x] Update all in-test mock `SkillIncidentManager` implementations (in `cron_runner_test.go`,
      `api_memories_test.go`, `alert_correlation_gate_test.go`,
      `alert_suppressor_gate_test.go`) to add stub implementations of the two new methods
- [x] Update incident list and detail handlers in `api_incidents.go`: compute `alert_count` via
      `COUNT(*) FROM alerts WHERE incident_uuid = ?` and include it in the response instead of the
      removed `correlated_count` field
- [x] Update `incident_service_test.go`: remove `AppendCorrelatedAlert` tests; add tests for
      `LinkAlertToIncident` (alert row inserted, monitor window extended for monitor incident),
      `InsertFiringAlert` (alert row inserted), and monitor transition in `UpdateIncidentComplete`
      (alert-sourced incident gets `monitor_until` set, non-alert incident does not)
- [x] Run `make test` — must pass before task 5

### Task 5: Alert processor refactor

**Files:**
- Modify: `internal/handlers/alert_processor.go`
- Modify: `internal/handlers/alert.go`
- Modify: `cmd/akmatori/main.go`

- [ ] Replace two-gate body in `processAlert`: single `h.correlate()` call → confident match:
      call `skillService.LinkAlertToIncident` + send brief Slack thread note (no new investigation
      spawned); no-match or error (fail-open): `SpawnIncidentManager` + `InsertFiringAlert` +
      `go runInvestigation`; singleflight followers: no-op (the partial-unique index handles
      cross-process burst dedup)
- [ ] Apply the same replacement to `ProcessAlertFromListenerChannel`
- [ ] Remove suppression gate block (`h.suppress()`, `h.skillService.RecordSuppressedIncident`,
      `alertSpawnResult.suppressed`), `IsLongWindowMatch`/`runRecurrenceUpdate` branch,
      `recordRecurrence` follower blocks, and `alertSpawnResult.longWindow` field
- [ ] Replace both resolved-alert early returns with a new
      `processResolvedAlert(sourceUUID string, normalized NormalizedAlert)` function: in a
      transaction with `clause.Locking{Strength:"UPDATE"}` on the incident row — find matching
      firing `alerts` row (by `source_fingerprint` match first, then `fingerprint` fallback,
      newest-first, `LIMIT 1`); mark it `resolved_at = now`; if no firing alerts remain AND
      incident is completed/monitor, update `monitor_until = min(monitor_until, resolved_at +
      window)`; no match → `slog.Info` + drop; best-effort Slack resolved thread reply
- [ ] Remove `SetAlertSuppressor`, `SetOneShotCaller` wiring methods from `alert.go`; remove
      `alertSuppressor *services.AlertSuppressor` and `oneShotCaller` fields from `AlertHandler`
- [ ] In `main.go`: remove `NewAlertSuppressor`/`SetAlertSuppressor` block; remove
      `alertHandler.SetOneShotCaller` line; keep `NewAlertCorrelator`/`SetAlertCorrelator`
- [ ] Update `alert_correlation_gate_test.go`: remove long-window path tests; update mock stubs
      for new interface (add `LinkAlertToIncident`/`InsertFiringAlert`); add test that on
      correlation match with a monitor incident, `LinkAlertToIncident` is called (not
      SpawnIncidentManager)
- [ ] Run `make test` — must pass before task 6

### Task 6: Remove suppression backend and recurrence stats

**Files:**
- Delete: `internal/services/alert_suppressor.go`
- Delete: `internal/database/models_alert_suppression.go`
- Delete: `internal/database/models_alert_correlation.go`
- Delete: `internal/handlers/api_recurrence_stats.go`
- Delete: `internal/services/alert_suppressor_test.go`
- Delete: `internal/handlers/alert_suppressor_gate_test.go`
- Delete: `internal/handlers/long_window_recurrence_test.go`
- Delete: `internal/handlers/api_recurrence_stats_test.go`
- Modify: `internal/handlers/api.go`
- Modify: `internal/handlers/api_memories.go`
- Modify: `internal/services/interfaces.go`
- Modify: `internal/services/memory_service.go`
- Modify: `internal/services/memory_service_sync_test.go`
- Modify: `internal/services/memory_service_test.go`
- Modify: `internal/database/models_context.go`
- Modify: `internal/database/db.go`
- Modify: `internal/handlers/api_memories_test.go`

- [ ] Delete the four backend files and four test files listed above
- [ ] Remove `GET /api/stats/recurrence` route registration from `api.go`
- [ ] Remove `PATCH /api/memories/{id}/suppress` route and `handleMemorySuppress` handler from
      `api_memories.go`
- [ ] Remove `SetSuppress(id uint, suppress bool) error` from `MemoryService` interface in
      `interfaces.go`
- [ ] Remove `SetSuppress` method implementation from `memory_service.go`
- [ ] Remove `Suppress bool` field from `Memory` struct in `models_context.go`
- [ ] Simplify `limitManifestEntries` in `memory_service.go`: remove the suppress-always-show
      branch; cap all entries uniformly by `UpdatedAt` (most recent first, up to
      `manifestMaxEntriesPerScope`)
- [ ] Remove `ensureMemoriesSuppressScopeIndex` function definition from `db.go` (call was
      removed in Task 1)
- [ ] Remove `TestSyncMemoryFiles_SuppressEntryAlwaysInManifest` from
      `memory_service_sync_test.go`
- [ ] Remove `TestMemoryService_SetSuppress_*` tests from `memory_service_test.go`
- [ ] Remove all `SetSuppress` tests and `mockMemoryService.SetSuppress` method from
      `api_memories_test.go`
- [ ] Run `make test` — must pass before task 7

### Task 7: Frontend cleanup

**Files:**
- Delete: `web/src/components/settings/SuppressionSignaturesSection.tsx`
- Delete: `web/src/components/settings/RecurrenceStatsPanel.tsx`
- Modify: `web/src/pages/Settings.tsx`
- Modify: `web/src/components/settings/GeneralSettingsSection.tsx`
- Modify: `web/src/api/client.ts`

- [ ] Delete `SuppressionSignaturesSection.tsx` and `RecurrenceStatsPanel.tsx`
- [ ] In `Settings.tsx`: remove imports of both deleted components; remove the
      `suppressionRefreshKey` state and `setSuppressionRefreshKey`; remove the
      "Recurrence & Gate Effectiveness" section render block
- [ ] In `GeneralSettingsSection.tsx`: remove `recurrenceStatsApi` import; remove all
      `suppressionEnabled`/`suppressionThreshold` state + the recurrence stats fetch call; remove
      all suppression form fields; remove all correlation knob fields (window, threshold, max
      candidates, long window days, fingerprint window minutes); keep the correlation enable toggle;
      add a number input for `alert_monitor_window_minutes` (label: "Monitor window (minutes)",
      min=1)
- [ ] In `web/src/api/client.ts`: remove `recurrenceStatsApi` export; remove
      `memoriesApi.setSuppress` method
- [ ] Run `make test-web` — must pass before task 8

### Task 8: New tests for the redesigned behavior

**Files:**
- Modify or create test files in `internal/handlers/` and `internal/services/`

- [ ] Test: `processAlert` spawns a new incident and inserts one `alerts` row with correct
      `IncidentUUID`, `SourceUUID`, `Fingerprint`, and `status=firing`
- [ ] Test: `UpdateIncidentComplete` for a `source_kind='alert'` incident sets
      `MonitorUntil = CompletedAt + window` and `status = monitor`
- [ ] Test: `UpdateIncidentComplete` for a `source_kind='cron'` incident does NOT set
      `MonitorUntil`
- [ ] Test: `fetchCandidates` returns a `monitor` incident whose `monitor_until >= now`; does NOT
      return a monitor incident whose `monitor_until < now`
- [ ] Test: correlation match on a `monitor` incident → `LinkAlertToIncident` called; no new
      incident spawned; monitor window extended
- [ ] Test: correlation match on a `running` incident → `LinkAlertToIncident` called; no new
      incident spawned
- [ ] Test: `processResolvedAlert` matches by `source_fingerprint`, marks `alerts.resolved_at`
- [ ] Test: `processResolvedAlert` — last firing alert resolved + incident already completed →
      `monitor_until` pulled in to `min(monitor_until, resolved_at + window)`
- [ ] Test: `processResolvedAlert` — no matching firing alert → log + drop, no error
- [ ] Test: `ErrWorkerNotConnected` during correlation → fail-open, new incident spawned
- [ ] Run `make test` — must pass before task 9

### Task 9: Verify acceptance criteria

- [ ] Run `go test -coverprofile=coverage.out ./...` — must pass clean
- [ ] Run `make test` — full Go test suite
- [ ] Run `make test-web` — frontend tests
- [ ] Run `make verify` — pre-commit full gate
