# Events store and event classification

Status: phases 1, 2 and 4 implemented on branch `feat/events-store` (2026-10-27). Phases 3 and 5
open. Phase 4 was pulled forward: its on/off switch is the safe way to stop classification on an
install without a release.
Decided: backfill yes, re-send reuse yes, all inbound items are events, cron may report alert events,
DB layout redesign yes (phase 5), page renamed to Events at `/events`.

### What changed from the draft during implementation

- **Re-sends fold into one row** instead of writing a new row with `classification=reused`: the
  earlier row gets `repeat_count` and `last_seen_at`. Without this an Alertmanager group re-sent every
  `repeat_interval` would flood the feed. The `reused` value is gone.
- **Backfilled rows** use `classification=backfill`, not `fallback`, so a backfilled row never looks
  like a failed classification.
- **Allowed classes per source**: webhook and Slack listener → alert, change, info (fallback alert).
  New-thread Slack mention and API task → alert, request (fallback request: the behaviour from before
  events; a change report through a mention is answered as a request). Mention follow-ups in a known
  thread, cron ticks and proposal chat → request by source, no LLM call.
- **Mentions and API tasks use the extraction call** (`ExtractAndClassify`) so a reported alert also
  gets its alert fields (name, host) from the same single call.

## Problem

Today every inbound item is an alert. A webhook payload and a Slack listener-channel message both
become an `alerts` row, and each alert starts an investigation or links to one. A message such as
"deployed nginx 1.27 to wa2" in a listener channel starts a full investigation. Also, Akmatori keeps
no record of changes, so the agent cannot answer "what changed just before this page?".

"Event" is not a stored object today. `GET /api/events` builds the feed from two tables: `alerts`
rows (`event_type=alert`) and non-alert incidents (`cron`, `slack_mention`, `manual`, `proposal`),
so incidents show up as events.

## Target model

Three objects, three tables:

| Object | Table | What it is |
|---|---|---|
| Event | `events` (new) | Every inbound item, as received, with a class: `alert`, `change`, `info` or `request` |
| Alert | `alerts` (exists) | An event of class `alert`, with the firing/resolved lifecycle |
| Incident | `incidents` (exists) | A production problem; opened ONLY from alerts, through the correlation gate |

Every inbound item writes one `events` row first: webhook items, Slack listener messages, Slack
@mentions, manual requests from the UI, cron ticks and proposal chat messages. The class says what
Akmatori does with it:

| Class | Meaning | Action |
|---|---|---|
| `alert` | Reports a problem | `alerts` row → correlation gate → link to an incident or open one |
| `change` | Reports a change (deploy, config, maintenance) | Stored; the agent can query it |
| `info` | Nothing to do | Stored |
| `request` | Asks the agent to do work, but reports no problem | Agent run; NOT an incident |

Examples: "@akmatori wa2 is down" is `alert` (a human-reported alert, correlated like any other).
"@akmatori what is the disk usage on host X" is `request`. A cron tick is `request`; when the cron
run finds a problem, it reports an `alert` event, so a cron check can open an incident only through
the correlation gate. A mention inside an incident's Slack thread continues that incident.

The `alerts` table keeps its lifecycle logic (firing index, monitor mode, stale close, correlator) and
gets no non-alert rows, so none of that logic needs a new filter.

## DB layout redesign (proposed 2026-10-27)

Today an `incidents` row is two things at once: a production problem (status, monitor window, merge,
alerts) and the storage for agent work (`session_id`, `working_dir`, `full_log`, `response`,
`tokens_used`, `last_skill_used`). That is the only reason cron, mention, manual and proposal runs are
incident rows. Split the two:

| Table | Holds | Change |
|---|---|---|
| `events` | Every inbound item and its class | New |
| `alerts` | Firing/resolved lifecycle of `alert` events | Add `event_uuid` |
| `incidents` | Problem only: title, status, monitor window, merge, close reason, Slack thread, result status | Remove the agent-work columns; only alert-sourced rows |
| `agent_runs` | One agent invocation: trigger event, log, response, session, workdir, tokens, model, cost | Exists today (metrics only). Add the agent-work columns, `event_uuid`, and make `incident_uuid` nullable |

- An incident has many runs: the first investigation and each Slack follow-up. The incident page
  shows the runs in time order, so `full_log` no longer grows on one row.
- A `request` event has one run, or more when someone replies in its Slack thread. It is never an
  incident.
- A run is linked to its trigger event (`event_uuid`) and, when it works on an incident, to that
  incident (`incident_uuid`).
- Retention: deleting an old incident or event clears the payload columns (`full_log`, `response`) of
  its runs and keeps the metric columns, so per-model stats survive, as today.
- MCP gateway: the allowlist key is an opaque scope id, so it becomes the run id. Small change.

Existing non-alert incident rows (cron, manual, slack_mention, proposal) migrate to an `events` row
(`kind=request`) plus their `agent_runs` row, and then leave `incidents`.

## Data model

`events`:

- `id`, `uuid`
- `source_kind` (`webhook` | `slack_listener` | `slack_mention` | `manual` | `cron` | `proposal`),
  `source_uuid` (alert source instance, channel, cron job or proposal),
  `source_type` (`alertmanager`, `zabbix`, `pagerduty`, `slack`, ...)
- `external_id`: the source's own id or fingerprint (Slack message ts, Alertmanager fingerprint)
- `received_at`, `occurred_at`
- `kind`: `alert` | `change` | `info` | `request`
- `classification`: `llm` | `reused` | `fallback` | `source` (how `kind` was set), plus `classification_confidence`
  and `classification_reasoning`
- `title`, `summary`, `host`, `service`, `labels` (JSONB), `raw_payload` (JSONB)
- `alert_uuid`: set when the event created or updated an `alerts` row

Indexes: `(kind, occurred_at)`, `(occurred_at)`, `(host)`, `(source_uuid, external_id)`.
`alerts` gets a nullable `event_uuid`. Retention cleanup also deletes old events.

## Write path

1. Webhook: `adapter.ParsePayload` gives N normalized items. Slack listener: one message gives one item.
2. Insert one `events` row per item.
3. Classify with one one-shot LLM call per event (`EventClassifier`, `OneShotLLMCaller`).
   - Input: source type, title, summary, host, status, labels, a cut of the raw text.
   - Output: `{"kind": "alert|change|info", "confidence": 0..1, "reasoning": "..."}`.
   - Slack listener: the classification goes into the existing extraction call (one more output
     field), so a Slack message still costs one LLM call.
   - Rules text is editable in Settings, like the correlation rules. The JSON contract stays in code.
4. `kind=alert` → the existing path (`processAlert` / `ProcessAlertFromListenerChannel`), which now
   also stores `event_uuid` on the alert row. Resolved notifications are class `alert` with status
   resolved and go to `processResolvedAlert` as today.
5. `kind=change` or `info` → stop. The event is stored and shown in the feed.

**Safety: fail to `alert`.** If the worker is disconnected, the call fails or times out (15 s), the
answer cannot be parsed, or the confidence is below 0.7, the event is `alert` with
`classification=fallback`. A dropped real alert is a missed incident, which is worse than one extra
investigation.

**Cost: no second call for a re-send.** Alertmanager re-sends a firing group every `repeat_interval`
with the same fingerprint. If the same `(source_uuid, external_id)` was classified in the last 24 h,
the event takes the same class (`classification=reused`) and makes no LLM call.

**Latency:** a classification call (1–5 s with a reasoning model) runs before the correlation gate,
so an investigation starts that much later.

## Read path

- `GET /api/events` reads from `events` (joined to the alert, incident and run links). Filters: `kind`
  and `source_kind`. All events stay in ONE tab, as today (route `/feed`, nav label "Events"). Each
  row shows its class and its result: the linked incident for `alert`, the run answer for `request`.
- The Incidents page shows only incidents (all alert-sourced after the redesign).
- Events page: show the class, the reasoning and the confidence. Add an action "Investigate as alert"
  on a `change`/`info` event: it sets `kind=alert` and runs the alert path.
- MCP gateway: a new built-in tool `events` with no credentials, the same pattern as `incidents`:
  - `events.search(kind?, since?, until?, host?, text?, limit?)`, newest first, summary fields only
  - `events.get(uuid)`, with the raw payload cut to a safe size
- Incident-manager prompt: "First check `events.search(kind=change, since=<60 min before the alert>)`
  for the same host or site."

## Migration

- AutoMigrate `events`, plus `alerts.event_uuid`.
- Backfill: one `events` row per existing `alerts` row (`kind=alert`, `classification=fallback`), so
  the Events feed keeps its history.

## Phases (one PR each)

1. Schema, write path, classifier with fallback and re-send reuse, alert link. Tests for each path.
2. `GET /api/events` from the new table, Events page (class, reasoning, "Investigate as alert").
3. Gateway `events` tool and incident-manager prompt guidance.
4. Editable classification rules in Settings (API and UI), like the correlation rules.
5. DB layout redesign: agent-work columns move to `agent_runs`; `request` events run without an
   incident row; migrate the existing non-alert incidents; incident page shows runs.

## Open questions

1. Backfill the existing alerts into `events`? **Decided 2026-10-26: yes.**
2. Remove non-alert incidents from the Events feed? (Recommended: yes.)
3. Re-send of the same alert within 24 h: reuse the old class with no LLM call? **Decided 2026-10-26: yes.**
4. Cron ticks and proposal chat messages: set `kind=request` from the source (`classification=source`),
   with no LLM call? (Recommended: yes. Their class is known before they arrive.)
5. Cron runs report problems as `alert` events. **Decided 2026-10-27: yes.**
6. Tasks split (non-alert runs out of `incidents`). Deferred on 2026-10-27, then replaced by the
   DB layout redesign above (agent work moves to `agent_runs`). Waiting for confirmation.
7. Nav label of the one events tab: keep "Events" or rename to "Feed"? (Recommended: keep "Events".)
