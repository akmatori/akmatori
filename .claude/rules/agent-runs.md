---
paths:
  - "**/internal/services/incident_service.go"
  - "**/internal/services/interfaces.go"
  - "**/internal/services/retention_service.go"
  - "**/internal/handlers/agent_ws.go"
  - "**/internal/handlers/api_requests.go"
  - "**/internal/handlers/slack_thread.go"
  - "**/internal/database/models_agent_runs.go"
  - "**/internal/database/agent_runs_migration.go"
  - "**/web/src/components/requests/**"
  - "**/web/src/components/runs/**"
---

# Agent runs, incidents and request work

An `incidents` row is a production problem and nothing else: only alerts open incidents (see the
`events` rule). The agent work lives on `agent_runs`, one row per agent invocation.

| Work | Scope (`agent_runs.scope_uuid`) | Record of its own |
|---|---|---|
| Incident (alert, reported alert, unlinked alert) | the incident UUID | `incidents` row |
| Request (Slack mention, API task, cron tick, proposal chat) | the request event's UUID | none: the runs and the event |

- the scope is what the worker and the gateway key a run by: the wire field `incident_id`, the work
  directory `<incidents dir>/<scope>`, the gateway allowlist key (`X-Incident-ID`). Follow-up turns
  (Slack replies, chat turns) reuse the scope
- run lifecycle: `SpawnIncidentManager` (incidents) or `PrepareRequestScope(rootSkill, eventUUID)`
  (requests; follow-ups call `EnsureScopeDir`) → `BeginRun(RunStart)` creates the run row BEFORE
  dispatch and returns its `run_id` → `StartIncident(scope, runID, ...)` → `UpdateRunLog(runID)` while
  streaming → `FinishRun(runID, RunOutcome)` stores log/answer/session and, for incident work, runs
  `completeIncident` (monitor transition, merge pass). Every write targets the run by `run_id`
- `BeginRun` on incident work sets the incident `running`; a follow-up run with no `SourceKind`
  copies the incident's source kind/uuid so formatting rules match it as the incident's trigger.
  `BuildFormatFlow(runID, channel)` reads the run row
- worker frames: `CompleteAgentRun`/`FailAgentRun` record metrics; `last_skill_used` is written on
  the run by `run_id` before `OnCompleted`; `result_status` stays on the incident (guarded by
  `isCurrentRun`). Frames with no live callback are dropped — there is no legacy DB fallback
- `RecordAgentRunDispatch` stamps the model at dispatch and creates the row when a caller made none
- Slack thread → work: `lookupSlackThread` checks `agent_runs.slack_thread_ts` (request work) first,
  then `incidents.slack_message_ts`. Used by the mention flow and the feedback classifier
- API: `GET /api/incidents/{uuid}` fills transient `response` (newest answer), `full_log` (all runs,
  oldest first, `=== Run i of n ===` headers), token/time sums and `runs`;
  `GET /api/requests/{uuid}` (+ `/response`) is request work (status = newest run). `POST
  /api/incidents` returns the request scope as `uuid` for a request (`event_kind: request`)
- retention: an expired incident is deleted and its runs' payload (`full_log`, `response`,
  `session_id`) is cleared; expired request scopes (newest run finished before the cutoff, none
  running) get the same clear and lose their directory. Run rows and metrics are never deleted
- migration `migrateSplitAgentRuns` (runs while `incidents.full_log` exists): copies the work onto
  each incident's newest run (a model-less synthetic run when it had none), turns non-alert
  incidents into request work with the old incident UUID as scope, then drops the columns
- web: `/requests/:uuid` (`RequestPanel`) reuses the incident Summary/Timeline tabs (typed on
  `AgentWork`); both pages have a Runs tab (`RunsTab`)

Key files: `internal/services/incident_service.go`, `internal/database/models_agent_runs.go`,
`internal/database/agent_runs_migration.go`, `internal/handlers/api_requests.go`,
`internal/handlers/slack_thread.go`, `docs/plans/2026-10-26-events-store-and-classification.md`
