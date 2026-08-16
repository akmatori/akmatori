---
paths:
  - "**/internal/alerts/**"
  - "**/internal/handlers/alert_processor.go"
  - "**/internal/handlers/api_alert_source*"
  - "**/internal/handlers/api_incidents.go"
  - "**/internal/services/alert_*"
  - "**/internal/services/incident_merger.go"
  - "**/internal/services/monitor_sweep_service.go"
  - "**/internal/services/stale_close_service.go"
  - "**/internal/services/incident_activity.go"
  - "**/internal/database/models_alerts.go"
---

# Alert correlation gate

Before spawning a new incident, `AlertHandler` runs `AlertCorrelator.Correlate`, which tries a
deterministic fingerprint match first and falls back to asking the LLM whether the incoming alert
belongs to a recent open or monitor-mode incident. On a confident match, `LinkAlertToIncident` is
called instead of `SpawnIncidentManager`.

- **fingerprint fast path runs first**: `matchByFingerprint` looks for a live incident carrying the
  same fingerprint with activity inside `fingerprintFastPathWindow` (48h); a hit returns
  `Correlated=true, Confidence=1.0` and no LLM call is made
- it matches BOTH `incidents.alert_fingerprint` (the spawning alert) AND `alerts.fingerprint` of
  every linked alert — once the LLM decides alert Y belongs to incident A, later fires of Y follow
  that decision deterministically instead of being re-judged (and possibly answered differently)
- linked alerts match regardless of alert status; restricting to firing alerts would break monitor
  mode, whose incidents have resolved alerts by definition
- **alerts with no `target_host` skip the fast path entirely** and go to the LLM: the fingerprint is
  only `hash(source, alertName, targetHost)`, so a host-less alert collapses fleet-wide onto one
  fingerprint — too coarse to link on deterministically
- the window is 48h, NOT 24h, deliberately: installs carry alerts re-firing on a near-exact daily
  cadence, and a 24h window lands on that period (observed gaps of 22h46m vs 24h00m31s for the same
  alert on the same host), making collapse-vs-spawn depend on seconds of jitter. Keep the window
  clearly wider than the target install's re-fire interval, and under the stale-close window
- it sits BEFORE the `caller == nil` check on purpose — deterministic dedup must keep working with
  no LLM configured or the worker disconnected; it is still gated on `AlertCorrelationEnabled`
- a fast-path DB error is logged and falls through to the LLM path, never out of the gate
- `idx_alerts_incident_fingerprint` on `alerts (incident_uuid, fingerprint)` serves the linked-alert
  EXISTS lookup
- `liveTargetCond` is the single "viable recurrence target" predicate, shared by `fetchCandidates`
  and the fast path — never duplicate that status logic, or the two routes will disagree about
  where a recurrence belongs

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

# Incident activity clock and stale close

`monitor` is not the only way out of an open incident. An alert-sourced incident whose source never
sends a matching resolve is held at `completed` by its still-firing alert, so the monitor sweep can
never reach it and it stays open forever. `StaleCloseService` is that missing lifecycle exit.

- **one activity definition**, in `incident_activity.go`: last activity = max of
  `MAX(COALESCE(alerts.last_seen_at, alerts.fired_at))`, `completed_at`, `started_at`. Consumed by
  the sweep AND the correlator fast path — they must never diverge
- `staleIncidentCond` / `liveIncidentCond` are conjunction/disjunction forms, NOT `GREATEST(...)`:
  prod is PostgreSQL, tests are SQLite, and the two have no common multi-arg max
- `Alert.LastSeenAt` is the load-bearing piece. Sources with a stable `source_fingerprint`
  (Alertmanager) hit `uniq_firing_alert` on every re-send, so the insert is dropped and the row's
  timestamps freeze. BOTH insert paths call `bumpAlertLastSeen` on their `RowsAffected == 0` branch;
  drop that and the sweep closes incidents whose alert is firing right now
- scope is `source_kind='alert'` and status `completed`/`monitor` only. NOT pending/running/diagnosed
  (a stuck run is an orphaned-agent bug; closing it hides the bug), NOT `failed` (already terminal
  and already out of the open view), NOT merged, NOT cron
- gate is `IncidentAutoCloseEnabled` — nil means **enabled**, unlike every other gate; window is
  `IncidentAutoCloseMinutes` (default 4320 = 3 days, API bounds 60..129600)
- the sweep re-applies the staleness condition to its UPDATE, so an alert landing between the
  candidate query and the write leaves its incident open
- batched at `staleCloseBatchLimit` (500) per tick; a first-run backlog drains over successive ticks
  and `Truncated` reports there is more
- `POST /api/incidents/sweep-stale?dry_run=true` previews candidates and works even when the gate is
  off — that is the point, so an operator can size the blast radius before enabling it
- every close path stamps `Incident.ClosedReason` (`manual` / `monitor_expired` / `auto_stale`) and
  the UI shows it; a close with no visible cause reads as data loss

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

Key files: `internal/services/stale_close_service.go` (sweep, dry run, batching),
`internal/services/incident_activity.go` (the shared activity predicates),
`internal/handlers/alert_processor.go` (main investigation path; sets
`source_kind`/`source_uuid`), `internal/services/alert_correlator.go` (`CorrelationConfig`,
`CorrelationVerdict`, `AlertCorrelator`), `internal/services/alert_fingerprint.go`,
`internal/services/monitor_sweep_service.go`, `internal/database/models_alerts.go` (`Alert` model,
`AlertStatus` constants, unique index preventing duplicate concurrent fires),
`internal/handlers/api_incidents.go` (paginated list enriched with
`alert_count`/`first_seen`/`last_seen`/`trend`; `GET /api/incidents/{uuid}/alerts` ordered by
`fired_at ASC`; accepts `?trend_window=1h|3h`)
