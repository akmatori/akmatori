# Incidents Open/History, Events Feed, and Alert Unlink

## Overview

Three related features: (1) Incidents tab defaults to an Open view with no time filter; (2) a new Feed page shows every incoming event (alerts + cron/slack/manual triggers) with correlation reasoning; (3) correlated alerts can be unlinked from their incident, spawning a fresh investigation.

## Context

- Files involved: `internal/database/models_alerts.go`, `internal/services/incident_service.go`, `internal/services/interfaces.go`, `internal/handlers/api_incidents.go`, `internal/handlers/alert_processor.go`, `internal/handlers/api.go`, `web/src/pages/Incidents.tsx`, `web/src/pages/Feed.tsx` (new), `web/src/components/Layout.tsx`, `web/src/App.tsx`, `web/src/components/IncidentDetailView.tsx`, `web/src/api/client.ts`, `web/src/types/index.ts`
- Related patterns: alert enrichment batch-query pattern in `handleIncidents`; `api.ParsePagination`/`api.PaginatedResponse`; `InsertFiringAlert`/`LinkAlertToIncident` in `incident_service.go`; `incidentsApi.list` in `client.ts`
- Dependencies: none external

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add CorrelationDecision to Alert model and update service signatures

**Files:**
- Modify: `internal/database/models_alerts.go`
- Modify: `internal/services/incident_service.go`
- Modify: `internal/services/interfaces.go`
- Modify: `internal/handlers/alert_processor.go`

- [x] Add `CorrelationDecision string` field (`gorm:"size:16;index"`) to `database.Alert` struct with values `"linked"`, `"new_incident"`, `"not_evaluated"`
- [x] Update `InsertFiringAlert` signature to `InsertFiringAlert(ctx, incidentUUID, sourceUUID string, alert NormalizedAlert, decision, reasoning string) error`; set the two new fields on the `database.Alert` row before `Create`
- [x] Update `LinkAlertToIncident` to set `CorrelationDecision: "linked"` on the inserted row
- [x] Update `IncidentManager` interface in `interfaces.go` to match the new `InsertFiringAlert` signature
- [x] In `alert_processor.go` `processAlert`: derive `decision`/`reasoning` from `corrErr` and `verdict` then pass to `InsertFiringAlert` — `"linked"` if confident match (unreachable here, already returned), `"new_incident"` + `verdict.Reasoning` if correlator ran but no match, `"not_evaluated"` + short literal if correlator nil / `ErrWorkerNotConnected` / other error
- [x] Apply the same derivation in `ProcessAlertFromListenerChannel` (~line 299)
- [x] Update `Alert` type in `web/src/types/index.ts` to include `correlation_decision?: string`
- [x] run `make test` — must pass before Task 2

### Task 2: Incidents list status filter

**Files:**
- Modify: `internal/handlers/api_incidents.go`

- [x] In `handleIncidents` GET handler, parse a `status` query param (comma-separated); when present add `WHERE status IN (?)` to both the data query and `countQuery` before pagination
- [x] Add test in a new `internal/handlers/api_incidents_test.go` (or extend existing) covering: no filter returns all, `status=monitor` returns only monitor rows, multiple statuses work; reuse `setupCorrelatorHandlerDB` / `seedHandlerIncident` helpers from `alert_correlation_gate_test.go`
- [x] run `make test` — must pass before Task 3

### Task 3: Unified events feed endpoint

**Files:**
- Create: `internal/handlers/api_events.go`
- Modify: `internal/handlers/api.go`

- [x] Define `EventFeedItem` struct with fields: `EventType`, `EventUUID`, `Title`, `OccurredAt`, `Status`, `IncidentUUID`, `Correlated`, `CorrelationConfidence`, `CorrelationReasoning`, `CorrelationDecision`, `TargetHost`, `SourceUUID`, and enriched `IncidentTitle`/`IncidentStatus`
- [x] Implement `handleEvents` for `GET /api/events`; params: `page`/`per_page` via `api.ParsePagination`, optional `from`/`to` on `occurred_at`, optional `type` filter; execute two separate COUNT queries summed for total, then a `UNION ALL` via raw SQL (or two queries merged and re-sorted) with `LIMIT/OFFSET`: alerts projection (`event_type='alert'`, `occurred_at=fired_at`) and non-alert incidents projection (`event_type=source_kind`, `occurred_at=started_at`, correlation fields empty); batch-enrich with incident title+status for distinct `incident_uuid`s; return `api.PaginatedResponse[EventFeedItem]`
- [x] Wire `GET /api/events` in `api.go` alongside incidents routes
- [x] Add handler test covering: returns merged rows ordered by `occurred_at DESC`, type filter works, pagination works
- [x] run `make test` — must pass before Task 4

### Task 4: UnlinkAlertFromIncident service + runAgentInvestigation extraction + unlink handler

**Files:**
- Modify: `internal/services/incident_service.go`
- Modify: `internal/services/interfaces.go`
- Modify: `internal/handlers/api_incidents.go`
- Modify: `internal/handlers/api.go`

- [x] Add `UnlinkAlertFromIncident(ctx context.Context, alertUUID string) (newIncidentUUID string, err error)` to `incident_service.go`: load alert; reject with typed error if `Correlated == false`; reconstruct `IncidentContext` from alert fields; call `SpawnIncidentManager`; repoint alert row (`IncidentUUID=new`, `Correlated=false`, `CorrelationDecision="new_incident"`, `CorrelationReasoning="manually unlinked from <oldUUID>"`, `CorrelationConfidence=nil`); return new UUID
- [x] Add `ErrAlertNotCorrelated` error sentinel in `services` package (HTTP 409)
- [x] Add `UnlinkAlertFromIncident` to `IncidentManager` interface in `interfaces.go`
- [x] Extract the inline investigation goroutine from `POST /api/incidents` into `func (h *APIHandler) runAgentInvestigation(incidentUUID, taskHeader, task string)` on `api_incidents.go` — make it a goroutine-launching method, keeping all existing logic (WebSocket path, superseded guard, formatter, metrics, fallback)
- [x] Add `handleAlertUnlink` for `POST /api/alerts/{uuid}/unlink`: call `UnlinkAlertFromIncident`, on `ErrAlertNotCorrelated` return 409, on success call `go h.runAgentInvestigation(...)`, return `{"incident_uuid": newUUID}`
- [x] Wire `POST /api/alerts/{uuid}/unlink` in `api.go`
- [x] Add service tests in `internal/services/incident_service_test.go`: `UnlinkAlertFromIncident` happy path repoints row and clears correlation; rejects unlinking a non-correlated origin alert (ErrAlertNotCorrelated)
- [x] Add handler test for `POST /api/alerts/{uuid}/unlink`: 200 happy path, 409 on non-correlated alert
- [x] run `make test` — must pass before Task 5

### Task 5: Frontend API client and type updates

**Files:**
- Modify: `web/src/types/index.ts`
- Modify: `web/src/api/client.ts`

- [x] Add `EventFeedItem` interface to `types/index.ts` mirroring the Go struct fields
- [x] Update `incidentsApi.list` to accept optional `status?: string` param and omit `from`/`to` when undefined (don't pass them in the query string when not set)
- [x] Add `eventsApi.list(params: {from?, to?, page, perPage, type?})` to `client.ts`
- [x] Add `alertsApi = { unlink: (uuid: string) => fetchApi<{incident_uuid: string}>(..., {method: 'POST'}) }` to `client.ts`
- [x] run `make test-web` — must pass before Task 6

### Task 6: Incidents tab Open/History toggle

**Files:**
- Modify: `web/src/pages/Incidents.tsx`

- [x] Add `view` state (`'open' | 'history'`, default `'open'`) and a segmented toggle control in the page header
- [x] Open view: call `incidentsApi.list` with `status="pending,running,diagnosed,monitor"`, no `from`/`to`; hide the `TimeRangePicker`
- [x] History view: call with `status="completed,failed"`, show `TimeRangePicker` with its existing default range
- [x] Reset page to 1 and reload when view changes
- [x] run `make test-web` — must pass before Task 7

### Task 7: Feed page and navigation

**Files:**
- Create: `web/src/pages/Feed.tsx`
- Modify: `web/src/components/Layout.tsx`
- Modify: `web/src/App.tsx`

- [x] Create `Feed.tsx` modeled on `Incidents.tsx`: `PageHeader`, optional `TimeRangePicker` for `from`/`to` filter, type-filter chips (All / Alert / Cron / Slack / Manual), paginated table with columns: Time, Type chip, Title, Status badge, Linked incident link; alert rows show `correlation_decision` chip (`"Correlated NN%"` / `"New incident"` / `"Not evaluated"`); click chip expands `correlation_reasoning` inline; Unlink button on alert rows where `correlated === true`
- [x] Add `{ name: 'Feed', href: '/feed', icon: Rss }` to the `navigation` array in `Layout.tsx` (import `Rss` from lucide-react)
- [x] Add `<Route path="/feed" element={<Feed />} />` in `App.tsx` and import `Feed`
- [x] run `make test-web` — must pass before Task 8

### Task 8: Unlink action in IncidentDetailView

**Files:**
- Modify: `web/src/components/IncidentDetailView.tsx`

- [x] In the Alerts tab, next to the "Correlated" badge on each alert row, add an "Unlink" button visible only when `alert.correlated === true`
- [x] On click: call `alertsApi.unlink(alert.uuid)`; on success refresh the alerts list and show a success message with a link to the new incident UUID
- [x] run `make test-web` — must pass before Task 9

### Task 9: Verify acceptance criteria

- [x] run `make test` (Go full suite)
- [x] run `make test-web`
- [x] run `make verify` (pre-commit gate)
