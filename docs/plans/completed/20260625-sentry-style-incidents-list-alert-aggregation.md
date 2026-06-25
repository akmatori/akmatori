# Sentry-style incident issues list with alert aggregation

## Overview
Replace the flat incidents table with a Sentry-style issues list showing alert aggregation metrics (trend sparkline, Age, Last seen, alert count, status remap). Add a secondary Alerts tab on incident detail with persisted correlation reasoning.

## Context
- Files involved: `internal/database/models_incidents.go`, `internal/database/models_alerts.go`, `internal/handlers/api_incidents.go`, `internal/handlers/api.go`, `internal/services/interfaces.go`, `internal/services/incident_service.go`, `internal/handlers/alert_processor.go`, `internal/handlers/alert_correlation_gate_test.go`, `internal/handlers/api_memories_test.go`, `web/src/pages/Incidents.tsx`, `web/src/components/IncidentDetailView.tsx`, `web/src/components/TrendSparkline.tsx` (new), `web/src/types/index.ts`, `web/src/api/client.ts`
- Related patterns: `AlertCount` transient field (`gorm:"-"`) pattern in `models_incidents.go:64`; batch COUNT query in `api_incidents.go:66-89`; `incidentsApi.list` signature with URLSearchParams; 3-tab IncidentDetailView with `TabType` union
- Dependencies: no new npm dependencies; pure SVG sparkline

## Development Approach
- Testing approach: Regular (code first, then tests)
- Complete each task fully before moving to the next
- CRITICAL: every task MUST include new/updated tests
- CRITICAL: all tests must pass before starting next task

## Implementation Steps

### Task 1: Extend DB models — transient fields on Incident + correlation fields on Alert

**Files:**
- Modify: `internal/database/models_incidents.go`
- Modify: `internal/database/models_alerts.go`

- [x] Add three transient fields to `Incident` after `AlertCount` (line 65): `FirstSeen *time.Time \`gorm:"-" json:"first_seen,omitempty"\``, `LastSeen *time.Time \`gorm:"-" json:"last_seen,omitempty"\``, `Trend []int \`gorm:"-" json:"trend,omitempty"\``
- [x] Add correlation fields to `Alert` after `RawPayload`: `Correlated bool \`gorm:"default:false" json:"correlated"\``, `CorrelationConfidence *float64 \`json:"correlation_confidence,omitempty"\``, `CorrelationReasoning string \`gorm:"type:text" json:"correlation_reasoning,omitempty"\``
- [x] Run `make test` to confirm compilation

### Task 2: Backend — bucketTimestamps helper + enriched incidents list endpoint

**Files:**
- Create: `internal/handlers/incident_trend.go`
- Modify: `internal/handlers/api_incidents.go`

- [x] Create `internal/handlers/incident_trend.go` with unexported `bucketTimestamps(events []time.Time, start, end time.Time, buckets int) []int` — divide the window into N equal buckets, count events falling in each, return slice of length N
- [x] In `api_incidents.go` GET list branch: read `trend_window` query param (`"1h"` default, `"3h"` accepted); parse it to a `time.Duration`
- [x] Replace the existing single COUNT batch query with two batch queries: (1) `SELECT incident_uuid, COUNT(*) AS count, MIN(fired_at) AS first_seen, MAX(fired_at) AS last_seen FROM alerts WHERE incident_uuid IN ? GROUP BY incident_uuid` — populate `AlertCount`, `FirstSeen`, `LastSeen`; (2) `SELECT incident_uuid, fired_at FROM alerts WHERE incident_uuid IN ? AND fired_at >= ?` (now - window) → collect per-incident slices of `time.Time`; call `bucketTimestamps` (12 fixed buckets) per incident; populate `Trend`; assign zero-filled `[]int{0,...,0}` (len 12) for incidents with no alerts in window
- [x] Add a struct for the windowed rows query to avoid ambiguous scan
- [x] Write unit tests for `bucketTimestamps` in `internal/handlers/incident_trend_test.go`: empty events, all events in one bucket, events spanning all buckets, window boundary edge cases
- [x] Write handler test asserting `GET /api/incidents?trend_window=1h` returns `first_seen`, `last_seen`, and 12-element `trend` alongside `alert_count`
- [x] Run `make test`

### Task 3: Persist correlation verdict — extend LinkAlertToIncident through the stack

**Files:**
- Modify: `internal/services/interfaces.go`
- Modify: `internal/services/incident_service.go`
- Modify: `internal/handlers/alert_processor.go`
- Modify: `internal/handlers/alert_correlation_gate_test.go`
- Modify: `internal/handlers/api_memories_test.go` (and grep for any other `LinkAlertToIncident` mocks)

- [x] In `interfaces.go` change `LinkAlertToIncident` signature to `LinkAlertToIncident(ctx context.Context, incidentUUID string, sourceUUID string, alert alerts.NormalizedAlert, confidence float64, reasoning string) error`
- [x] In `incident_service.go` `LinkAlertToIncident` implementation set `Correlated: true`, `CorrelationConfidence: &confidence`, `CorrelationReasoning: reasoning` on the alert row before save; `InsertFiringAlert` (spawning alert) stays `Correlated: false`
- [x] In `alert_processor.go` line 114: pass `verdict.Confidence, verdict.Reasoning` as the two new trailing args
- [x] In `alert_processor.go` line 277: same — pass `verdict.Confidence, verdict.Reasoning`
- [x] Update the `corrGateSkillService.LinkAlertToIncident` mock in `alert_correlation_gate_test.go` to accept the new signature (just add `confidence float64, reasoning string` params; mock body unchanged)
- [x] Grep for all other `LinkAlertToIncident` mocks (e.g. `api_memories_test.go:552`) and update each
- [x] Write or extend a test asserting that after a confident correlation, the linked `Alert` row has `Correlated=true`, the expected confidence, and the expected reasoning
- [x] Run `make test`

### Task 4: New GET /api/incidents/{uuid}/alerts endpoint

**Files:**
- Modify: `internal/handlers/api.go`
- Modify: `internal/handlers/api_incidents.go`

- [x] In `api.go` beside the existing incident routes, add `mux.HandleFunc("GET /api/incidents/{uuid}/alerts", h.handleIncidentAlerts)` — use exact-method prefix routing so it resolves before the wildcard `/api/incidents/` catch-all
- [x] Append `handleIncidentAlerts` to `api_incidents.go`: extract `uuid` via `r.PathValue("uuid")`, query `Where("incident_uuid = ?", uuid).Order("fired_at ASC").Find(&alerts)`, respond JSON; 404 if the incident doesn't exist (check incident row first)
- [x] Write handler test: fixture incident with two alert rows, GET returns them ordered by `fired_at` ASC; assert correlation fields present on the correlated row
- [x] Run `make test`

### Task 5: Frontend — extend types + API client

**Files:**
- Modify: `web/src/types/index.ts`
- Modify: `web/src/api/client.ts`

- [x] In `types/index.ts` add to `Incident`: `source_kind?: string; first_seen?: string; last_seen?: string; trend?: number[];`
- [x] In `types/index.ts` add new `Alert` interface: `uuid, incident_uuid, status: 'firing'|'resolved', fingerprint?, source_uuid?, alert_name, target_host, fired_at, resolved_at?, correlated, correlation_confidence?, correlation_reasoning, raw_payload: any, created_at, updated_at`
- [x] In `api/client.ts` extend `incidentsApi.list` to accept optional `trendWindow?: '1h' | '3h'` arg and append it as `trend_window` query param when provided
- [x] In `api/client.ts` add `incidentsApi.getAlerts(uuid: string): Promise<Alert[]>` → `fetchApi<Alert[]>(\`/api/incidents/\${uuid}/alerts\`)`
- [x] Run `make test-web` (type-check passes with new fields)

### Task 6: Frontend — TrendSparkline component

**Files:**
- Create: `web/src/components/TrendSparkline.tsx`

- [x] Create `TrendSparkline.tsx`: props `buckets: number[]`; render an inline SVG (width ~88px, height ~24px); each bar width = `width/buckets.length`, height proportional to `bucket / max(buckets, 1)` scaled to SVG height; muted color (e.g. `currentColor` with opacity); include `<title>` element listing total alerts for accessibility; return a single `<span>` with `aria-label` when buckets is empty or all-zero to show a flat line placeholder
- [x] Run `make test-web`

### Task 7: Frontend — redesign Incidents table

**Files:**
- Modify: `web/src/pages/Incidents.tsx`

- [x] Add `formatRelative(iso: string): string` helper near `formatExecutionTime`: returns "Xm" / "Xh" / "Xd" / "Xw" from ISO string to now; used for Age and Last seen
- [x] Update `getStatusConfig` status remap: `pending|running|diagnosed → { label: 'Ongoing', ... }`, `monitor → { label: 'Monitoring', ... }`, `completed → { label: 'Resolved', ... }`, `failed → { label: 'Failed', ... }`; for `monitor` status, compute a countdown string from `monitor_until` and attach it as a sub-label/title tooltip
- [x] Add `trendWindow` state (`'1h' | '3h'`, default `'1h'`); pass it to `loadIncidents` → `incidentsApi.list`; add a small segmented toggle (two buttons) in the `PageHeader` action area alongside existing controls; on toggle, refetch
- [x] Replace the 7-column table header with the new columns: Issue / Trend / Age / Last seen / Status / Alerts / Actions
- [x] Replace table row cells: Issue cell = title (bold) + source_kind chip (alert/cron/slack) + truncated uuid + source stacked; Trend cell = `<TrendSparkline buckets={incident.trend ?? []} />`; Age cell = `formatRelative(incident.first_seen ?? incident.started_at)`; Last seen = `formatRelative(incident.last_seen ?? incident.started_at)`; Status = existing badge using remapped `getStatusConfig`; Alerts = `alert_count` badge (de-emphasize / dim when 0 or 1, show count with bell icon when > 1); Actions = keep existing reasoning/response buttons
- [x] Preserve: time-range picker, pagination, auto-refresh, detail modal, create-incident modal
- [x] Run `make test-web`

### Task 8: Frontend — Incident detail Alerts tab

**Files:**
- Modify: `web/src/components/IncidentDetailView.tsx`

- [x] Add `'alerts'` to the `TabType` union
- [x] Add the Alerts tab button (after Raw Alert): show only when `incident.source_kind === 'alert'`; icon: Bell or List
- [x] Add `alerts` state (`Alert[] | null`), `alertsLoading` bool, `alertsError` string; fetch via `incidentsApi.getAlerts(incident.uuid)` on first tab open (lazy); show spinner while loading; degrade cleanly on error with an error message
- [x] Render the Alerts tab content: table of alert name / target host / status pill (firing/resolved) / fired (relative) / resolved (relative or —); rows with `correlated === true` show a "Correlated" badge with confidence percentage; clicking the badge (or a `>` expand) reveals `correlation_reasoning` text inline; the first/spawning row (correlated=false) is visually marked as "Origin"
- [x] Run `make test-web`

### Task 9: Verify acceptance criteria

- [x] Run `make test` — all Go tests pass
- [x] Run `make test-web` — all frontend tests pass, type-check clean
- [x] Rebuild API + frontend: `docker-compose -f docker-compose.yml -f docker-compose.dev.yml build akmatori-api frontend && docker-compose -f docker-compose.yml -f docker-compose.dev.yml up -d akmatori-api frontend` (manual - not automatable)
- [x] Enable correlation gate in Settings → General; fire same alert (same name + host) multiple times within the monitor window (manual test - not automatable)
- [x] DB check: `SELECT incident_uuid, alert_name, correlated, correlation_confidence FROM alerts ORDER BY fired_at;` shows recurrences with `correlated=true` (manual test - not automatable)
- [x] API check: `GET /api/incidents?trend_window=1h` returns `alert_count`, `first_seen`, `last_seen`, 12-bucket `trend`; `GET /api/incidents/{uuid}/alerts` lists rows ordered by `fired_at` (manual test - not automatable)
- [x] UI check: incidents list shows sparkline, Age/Last seen, "Monitoring" status with countdown; 1h↔3h toggle reshapes sparkline; detail Alerts tab lists alerts with correlation reasoning on recurrences (manual test - not automatable)
