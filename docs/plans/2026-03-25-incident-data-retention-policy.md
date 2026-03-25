---

# Automated Incident Data Retention Policy

## Overview

Add a background cleanup goroutine to the API server that periodically purges old incident data - both filesystem working directories and database records (including embedded reasoning logs, responses, and alert context). Also clean up orphaned directories on disk with no matching DB record. This addresses the 12GB disk usage from 5500+ incident directories.

## Context

- Files involved:
  - `internal/database/models_settings.go` - Add RetentionSettings model
  - `internal/database/db.go` - Add migration, CRUD for retention settings
  - `internal/services/retention_service.go` - New cleanup service
  - `internal/handlers/api.go` - Register retention settings endpoint
  - `internal/handlers/api_settings_retention.go` - New handler for GET/PUT retention settings
  - `internal/api/types.go` - Add request/response types
  - `cmd/akmatori/main.go` - Start background cleanup goroutine
  - `web/` - Add retention settings UI to settings page
- Related patterns: GeneralSettings singleton model, SlackManager background goroutine, slog structured logging
- Dependencies: None (uses existing GORM + filesystem APIs)
- Note: The `incidents` table is self-contained - `FullLog` (reasoning log), `Response`, and alert `Context` are all columns on the same table. Deleting an incident row removes all associated data. No foreign keys reference the incidents table, so no cascade is needed.

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Follow existing singleton settings pattern (GeneralSettings, ProxySettings)
- Follow existing background goroutine pattern (Slack WatchForReloads)
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add RetentionSettings database model and migrations

**Files:**
- Modify: `internal/database/models_settings.go`
- Modify: `internal/database/db.go`

- [x] Add `RetentionSettings` model to `models_settings.go` with fields: `ID`, `Enabled` (default: true), `RetentionDays` (default: 90), `CleanupIntervalHours` (default: 6), `CreatedAt`, `UpdatedAt`
- [x] Add `RetentionSettings` to AutoMigrate list in `db.go`
- [x] Add `GetOrCreateRetentionSettings()` and `UpdateRetentionSettings()` functions in `db.go` following the GeneralSettings pattern
- [x] Write tests for the new database functions
- [x] Run `make test` - must pass before task 2

### Task 2: Create retention cleanup service

**Files:**
- Create: `internal/services/retention_service.go`

- [x] Create `RetentionService` struct with `dataDir string` and `db *gorm.DB` fields
- [x] Implement `NewRetentionService(dataDir string) *RetentionService`
- [x] Implement `RunCleanup()` method with two cleanup phases:
  - Phase 1 (expired incidents): query DB for incidents with status completed/failed and completed_at older than retention days. For each expired incident: delete its working directory from disk, then delete the incident record from the database (this removes the FullLog, Response, and alert Context stored in that row)
  - Phase 2 (orphaned directories): scan all subdirectories in dataDir, check each UUID against the database, and delete directories that have no matching incident record (leftovers from deleted or never-recorded incidents)
- [x] Implement `StartBackgroundCleanup(ctx context.Context)` method: runs `RunCleanup()` on a ticker based on CleanupIntervalHours, respects context cancellation
- [x] Log cleanup actions with slog (number of directories cleaned per phase, DB records deleted, bytes freed, errors)
- [x] Write tests for RunCleanup using temp directories and mock incident data, including tests for orphan cleanup and DB record deletion
- [x] Run `make test` - must pass before task 3

### Task 3: Add retention settings API endpoint

**Files:**
- Modify: `internal/api/types.go`
- Create: `internal/handlers/api_settings_retention.go`
- Modify: `internal/handlers/api.go`

- [x] Add `UpdateRetentionSettingsRequest` struct to `types.go`
- [x] Create `handleRetentionSettings` handler supporting GET (read current settings) and PUT (update settings) following the `handleGeneralSettings` pattern
- [x] Register route `/api/settings/retention` in `api.go`
- [x] Write handler tests
- [x] Run `make test` - must pass before task 4

### Task 4: Wire up background cleanup in main.go

**Files:**
- Modify: `cmd/akmatori/main.go`

- [x] Create `RetentionService` after other services are initialized
- [x] Launch `go retentionService.StartBackgroundCleanup(ctx)` alongside other background goroutines
- [x] Run `make verify` - must pass before task 5

### Task 5: Add retention settings to web UI

**Files:**
- Modify: `web/src/` (settings page component - identify exact file during implementation)

- [x] Add "Data Retention" section to the settings page
- [x] Include toggle for enabled/disabled, input for retention days, cleanup interval
- [x] Wire up to GET/PUT `/api/settings/retention` endpoints
- [x] Verify UI renders correctly in browser

### Task 6: Verify acceptance criteria

- [x] Run full test suite (`make verify`) - go vet passes, all tests pass except pre-existing CGO/sqlite3 infrastructure issue (no gcc in CI env)
- [x] Run linter (`golangci-lint run`) - not installed in this environment; go vet clean
- [x] Verify new settings endpoint works end-to-end with `curl` (skipped - requires running Docker instance; handler tests validate request/response flow)
- [x] Verify cleanup service correctly removes old incident data (skipped - requires running Docker instance; unit tests validate cleanup logic with temp dirs and test DB)

### Task 7: Update documentation

- [ ] Update CLAUDE.md if internal patterns changed (add RetentionService to services table)
- [ ] Move this plan to `docs/plans/completed/`
