---
paths:
  - "**/internal/services/event_service*"
  - "**/internal/database/models_events.go"
  - "**/internal/database/events_migration*"
  - "**/internal/handlers/api_events*"
  - "**/internal/handlers/alert.go"
  - "**/internal/handlers/alert_reported.go"
  - "**/internal/handlers/slack_events.go"
  - "**/internal/handlers/slack_processor.go"
  - "**/internal/alerts/extraction/**"
  - "**/web/src/pages/Events.tsx"
  - "**/web/src/components/events/**"
---

# Events: every inbound item, classified

Every inbound item writes ONE `events` row first (`EventService`), then its class decides the path.
Only `alert` events open or join incidents; everything else never touches the incident lifecycle.

| Class | Meaning | Path |
|---|---|---|
| `alert` | reports a problem | alerts row → correlation gate → link or open incident |
| `change` | deploy, config change, maintenance | stored only (for the agent and the feed) |
| `info` | no action | stored only |
| `request` | work asked of the agent | agent run (still an `incidents` row until the runs split) |

| Source (`source_kind`) | Classes allowed | How classified |
|---|---|---|
| `webhook` | alert, change, info | `RecordClassified`: one-shot LLM call; resolved items = alert by source |
| `slack_listener` | alert, change, info | `ExtractAndClassify`: class fields appended to the extraction call (ONE call) |
| `slack_mention` (new thread), `manual` (POST /api/incidents) | all four | `ExtractAndClassify`; alert → `ProcessReportedAlert`; change/info → stored + thread ack (API: `status: recorded`) |
| `slack_mention` follow-up in a known thread | request | source, no LLM call |
| `cron`, `proposal` | request | source, no LLM call |

- **fail safe**: every failure (no caller, worker down, unparseable, unknown class, non-fallback
  class below 0.7) gives the fallback class with `classification=fallback`. Webhook/listener fall back
  to `alert` (a dropped real alert is worse than an extra investigation); mentions/API fall back to
  `request` (the behaviour before events)
- **every re-send gets its own row** (the Events page shows all of them). A webhook re-send copies
  the class of the newest `llm`/`source` event with the same source + identity (`external_id`, else
  the title+host fingerprint) + status from the last 24 h: `classification=reused`, no LLM call.
  Reused and fallback rows are never a reuse source, so the class is re-checked at least daily and a
  failed call is retried on the next re-send
- recording is best-effort: an insert failure is logged and the verdict still drives the path, so
  the alert path works with no events table. The event→alert/incident link is written AFTER the
  link transaction commits (a failed statement would abort a PostgreSQL transaction)
- `NormalizedAlert.EventUUID` carries the event into `InsertFiringAlert`/`LinkAlertToIncident`,
  which stamp `alerts.event_uuid` and link the event back; on a `uniq_firing_alert` conflict the event
  links to the row that holds the slot. `IncidentContext.EventUUID` links request events to their run
- reported alerts (`ProcessReportedAlert`) use source key `<channel uuid>` / `slack:<channel id>` /
  `manual`, open incidents with `source_kind=alert`, and run through the listener run loop with a
  synthesized Channel (`CanPost` only when there is a Slack thread)
- gate `GeneralSettings.EventClassificationEnabled` (nil = ON, read live via
  `LoadEventClassificationConfig`). Off: no classification call; webhook/listener → alert, mention/API →
  request (`ClassificationOffVerdict`); the listener still runs its extraction call, mentions/API skip it
- rules `GeneralSettings.EventClassificationPrompt` (nil/blank = `DefaultEventClassificationRules`;
  PUT stores nil for "" or the default text; GET adds `event_classification_prompt_default`). The class
  list + JSON contract are appended in code — same pattern as the correlation prompt
- migration `migrateBackfillEvents`: one `backfill` event per pre-existing alert (stamping
  `alerts.event_uuid`) and per non-alert run; batched and idempotent
- retention deletes events past the cutoff unless they still point at an existing incident
- feed: `GET /api/events` is one SQL query on `events` (filters `kind`, `source`, legacy `type`),
  enriched with the alert's live status/correlation and the incident title/status. Change/info rows
  have no status. The page is `/events` (`/feed` redirects, keeping the query)

Key files: `internal/services/event_service.go`, `internal/database/models_events.go`,
`internal/handlers/alert_reported.go`, `internal/handlers/slack_events.go`,
`internal/handlers/api_events.go`, `docs/plans/2026-10-26-events-store-and-classification.md`
