---
paths:
  - "**/internal/alerts/**"
  - "**/internal/handlers/alert_processor.go"
  - "**/internal/handlers/api_alert_source*"
  - "**/internal/handlers/api_incidents.go"
  - "**/internal/services/alert_*"
  - "**/internal/services/incident_merger.go"
  - "**/internal/services/monitor_sweep_service.go"
  - "**/internal/database/models_alerts.go"
---

# Alert correlation gate

Before spawning a new incident, `AlertHandler` runs `AlertCorrelator.Correlate` to ask the LLM
whether the incoming alert belongs to a recent open or monitor-mode incident. On a confident match,
`LinkAlertToIncident` is called instead of `SpawnIncidentManager`.

- gate is flag-gated (`AlertCorrelationEnabled` in `GeneralSettings`, default false); when disabled,
  no LLM call and all alerts spawn normally (fail-open)
- `NewAlertCorrelator(caller, db)`, wired via `alertHandler.SetAlertCorrelator(c)`; reads config live
  (no restart needed)
- both `processAlert` and `ProcessAlertFromListenerChannel` wrap evaluate-and-spawn in
  `h.spawnGroup.Do(key, ...)`; singleflight followers are no-ops — the partial-unique index on
  `alerts` handles burst dedup
- confident match → `LinkAlertToIncident(ctx, incidentUUID, sourceUUID, alert, confidence, reasoning)`
  attaches the alert row (persisting `Correlated`, `CorrelationConfidence`, `CorrelationReasoning`),
  extends `monitor_until` for monitor incidents, spawns nothing
- no-match or error (fail-open) → `SpawnIncidentManager` then `InsertFiringAlert`; resolved alerts
  go to `processResolvedAlert`
- `fetchCandidates` single query: `source_kind='alert' AND (status IN ('pending','running','diagnosed')
  OR (status='monitor' AND monitor_until >= NOW()) OR (status='completed' AND EXISTS unresolved
  firing alert))`, `ORDER BY started_at DESC LIMIT 25`; the completed clause covers incidents held
  out of monitor mode by a still-firing alert
- `ErrWorkerNotConnected` is fail-open (alert spawns normally)
- hallucination guard: any UUID not in the fetched candidate set forces `Correlated=false`
- `CorrelationConfig` holds only `Enabled bool`; `correlationMaxCandidates=25` and
  `correlationThreshold=0.7` are package-level constants
- alert fingerprint: `ComputeAlertFingerprint(sourceUUID, lower(alertName), lower(targetHost))`
  stored as `alert_fingerprint` (32-char sha256) on each `Incident`

# Alert monitor mode

After an alert-sourced incident completes, it enters `monitor` status for a configurable window so
that recurrences are correlated rather than spawning duplicate investigations.

- `UpdateIncidentComplete` sets `status=monitor` and `monitor_until = completedAt +
  GetAlertMonitorWindow()` for all `source_kind='alert'` incidents; non-alert incidents (cron, etc.)
  are unaffected
- `AlertMonitorWindowMinutes` is configured in `GeneralSettings` (default 60, valid 1–10080); read
  via `gs.GetAlertMonitorWindow()`; exposed in `PUT /api/settings/general`
- `processResolvedAlert` (tx + row lock): finds the `alerts` row by `source_fingerprint` (then
  `fingerprint` fallback), marks it `resolved_at=now`; when no firing alerts remain on a
  completed/monitor incident, sets `monitor_until = min(monitor_until, resolved_at + window)`;
  no-match is logged and dropped
- `InsertFiringAlert` inserts the initial `alerts` row (status=firing) for a newly spawned incident
- `LinkAlertToIncident` attaches an alert to an existing incident; extends `monitor_until` if the
  incident is in monitor status
- `MonitorSweepService` runs at startup and every 15m, closes expired monitor incidents, resolves
  any lingering firing alerts first, and clears `monitor_until`

# Post-investigation incident merge

`IncidentMerger` (`internal/services/incident_merger.go`): when an alert-sourced investigation
completes, a one-shot LLM compares its diagnosed root cause against earlier investigated incidents
and merges the newer incident into the earlier survivor on a confident match.

- flag-gated (`IncidentMergeEnabled` in `GeneralSettings`, default false, read live); fail-open
  everywhere; fired as a detached goroutine from `UpdateIncidentComplete` via
  `SkillService.SetIncidentMerger`
- candidates: alert-sourced `completed`/`monitor`, non-empty `response`, `completed_at` within 24h,
  `started_at` earlier than the completing incident (newer→older only), LIMIT 25; `mergeThreshold=0.8`
  + hallucination guard
- merge tx: lock both rows in UUID order, revalidate statuses, re-point `alerts` rows (safe:
  `uniq_firing_alert` is global), extend survivor `monitor_until`, set merged row `status=merged` +
  `merged_into_uuid`; merged incidents drop out of all candidate pools
- Slack: best-effort note only in the merged incident's thread; failure never rolls back the merge
- `LinkAlertToIncident` follows `merged_into_uuid` (bounded hops, each row locked) so a correlator
  verdict targeting a just-merged incident attaches to the survivor

# Alert sources and webhook adapters

Webhook alert sources are still `AlertSourceInstance` rows, while message destinations are Channels.
Keep those responsibilities separate.

- `GET /api/alert-source-types` hides deprecated types; `slack_channel` exists only for historical rows
- creating deprecated `slack_channel` sources must fail; inbound listening belongs to
  `can_listen=true` Channels
- `notification_channel_uuid` is optional on alert sources; when set, resolve to a post-capable
  Channel before create/update
- webhook handlers: fetch instance, reject disabled rows, find adapter by source type, validate
  secret, then parse body
- adapter integration tests: real `AlertService` + real adapter for happy, bad-secret,
  malformed-payload, and persisted firing-alert-row paths

Key files: `internal/handlers/alert_processor.go` (main investigation path; sets
`source_kind`/`source_uuid`), `internal/services/alert_correlator.go` (`CorrelationConfig`,
`CorrelationVerdict`, `AlertCorrelator`), `internal/services/alert_fingerprint.go`,
`internal/services/monitor_sweep_service.go`, `internal/database/models_alerts.go` (`Alert` model,
`AlertStatus` constants, unique index preventing duplicate concurrent fires),
`internal/handlers/api_incidents.go` (paginated list enriched with
`alert_count`/`first_seen`/`last_seen`/`trend`; `GET /api/incidents/{uuid}/alerts` ordered by
`fired_at ASC`; accepts `?trend_window=1h|3h`)
