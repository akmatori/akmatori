# Fix runbook search — give the agent the verbatim alert text and use multi-sub-query QMD calls

## Overview

Two linked changes in the API server, plus a documented one-time DB update for already-running deployments. The goal is to make QMD lex search effective for runbook discovery during incident investigations:

1. Render the verbatim original alert message and source identifiers into the investigation prompt so the agent has the same text that appears in runbook titles.
2. Rewrite the `DefaultIncidentManagerPrompt` runbook-search section to issue a single multi-sub-query `qmd.query` (verbatim 2×-weighted + keywords), with up-to-2 retry guidance on empty results.

No QMD embedding/vector infra changes (lex stays the only enabled type). No auto-overwrite-on-boot of operator-customized prompts — the upgrade path for the user's local docker-compose deployment is documented in the PR/commit message.

## Context

- Files involved:
  - `internal/handlers/alert_processor.go` — `buildInvestigationPrompt` at line 169; both `processAlert` (webhook flow) and `ProcessAlertFromSlackChannel` route through it
  - `internal/database/db.go` — `DefaultIncidentManagerPrompt` runbook-search section at lines 427-447
  - `internal/handlers/alert_test.go` — existing `TestAlertHandler_buildInvestigationPrompt` table test at line 288 (extend, do not duplicate)
- Related patterns:
  - `alerts.NormalizedAlert.RawPayload` is already `map[string]interface{}`; the alert extractor already stores `original_message` there (see `internal/alerts/extraction/extractor.go:193`)
  - Source type/instance names are accessible via `instance.AlertSourceType.Name` and `instance.Name` (already used in `processAlert` Context JSONB at line 57-58)
  - Slack budget byte-truncation lives in `internal/output/slack_budget.go` — for the prompt we use simple byte-cut + ellipsis since this is internal text, not Slack-bound
- Dependencies: none new; QMD v2.1.0 BM25 weights title/body/path columns (already deployed)
- User-confirmed scope: prompt + alert context only; retries capped at 3 total `qmd.query` calls

## Development Approach

- Testing approach: Regular (code first, then tests) for prompt-rendering helper; the existing table test in `alert_test.go` is already structured for extension.
- Complete each task fully before moving to the next.
- Default prompt change in `db.go` is a string constant — verified by reading the constant in tests where applicable; no behavioral test required for the constant itself, but the surrounding `InitializeSystemSkill` flow stays unchanged.
- CRITICAL: every task MUST include new/updated tests
- CRITICAL: all tests must pass before starting next task

## Implementation Steps

### Task 1: Render verbatim alert text + source in investigation prompt

Files:
- Modify: `internal/handlers/alert_processor.go`
- Modify: `internal/handlers/alert_test.go`

- [x] Add a small unexported helper `extractOriginalMessage(payload map[string]interface{}, max int) string` near `buildInvestigationPrompt` that returns the string value of `payload["original_message"]` trimmed and byte-truncated with an ellipsis when it exceeds `max`; returns "" when missing/empty/non-string
- [x] In `buildInvestigationPrompt`, after the existing `Description: …` line and before the `Please:` checklist, append a `Source: <source_system> / <source_instance>` line (use `instance.AlertSourceType.Name` and `instance.Name`); skip the line if both are empty
- [x] Append an `Original alert text:\n<truncated>` block when `extractOriginalMessage(alert.RawPayload, 1500)` returns non-empty; include a leading blank line so it sits as its own paragraph
- [x] Extend `TestAlertHandler_buildInvestigationPrompt` in `internal/handlers/alert_test.go` with two new table cases: (a) `RawPayload` containing `original_message` → assert the rendered prompt contains `"Source:"`, `"Original alert text:"`, and the first portion of the message text; (b) absent/empty `original_message` → assert the rendered prompt does NOT contain `"Original alert text:"` and matches existing behavior aside from the optional `Source:` line
- [x] Add a third case asserting truncation at the 1500-byte cap with the ellipsis suffix on a long message
- [x] Run `go test ./internal/handlers/ -run BuildInvestigationPrompt` — must pass before Task 2
- [x] Run `make test` — must pass before Task 2

### Task 2: Rewrite DefaultIncidentManagerPrompt runbook-search section

Files:
- Modify: `internal/database/db.go`

- [x] Replace lines 427-447 of `DefaultIncidentManagerPrompt` with a multi-sub-query strategy section that: (a) instructs the agent to issue ONE `qmd.query` with TWO `searches[]` entries — sub-query 1 verbatim (2× weight) from the rendered "Original alert text" excerpt or summary, truncated to ~250 chars; sub-query 2 short keywords from the alert name; (b) keeps the `"collection": "runbooks"` requirement and the rationale comment about memory.search separation; (c) adds the up-to-2 retry guidance with example angles (source_system/sender phrases, target_service/host alone, single distinctive phrase); (d) caps total `qmd.query` calls at 3; (e) keeps the existing `gateway_call("qmd.get", ...)` follow-up when score > 0.7 and the QMD-error fallback to `/akmatori/runbooks/`
- [x] Preserve numbering and surrounding sections (item 2 stays "MANDATORY - Search runbooks FIRST"; items 3-6 unchanged)
- [x] Keep the verbatim `gateway_call(...)` example block formatting consistent with existing prompt style (indentation, code-fence-free, two-space leading indent inside the bullet)
- [x] Add a focused test in `internal/database/db_test.go` (or extend an existing test in that file if one references `DefaultIncidentManagerPrompt`) asserting the constant contains the new multi-sub-query markers: `"sub-query 1"` (or whatever exact phrasing is chosen), `"limit\": 5"`, `"collection\": \"runbooks\""`, and a max-3-retries cue. If no test file exists for `db.go` constants, create `internal/database/db_prompt_test.go` with a single `TestDefaultIncidentManagerPrompt_RunbookSearchSection` table-driven assertion
- [x] Run `go test ./internal/database/ -run IncidentManagerPrompt` — must pass before Task 3
- [x] Run `make verify` — must pass before Task 3

### Task 3: Verify acceptance criteria

- [x] Run `make verify` (golangci-lint + full Go test suite) — must pass
- [x] Run `make test-adapters` and `make test-mcp` — must pass (no changes there, but these are cheap and confirm no incidental break)
- [x] Confirm `internal/handlers/alert_processor.go` change covers both `processAlert` and `ProcessAlertFromSlackChannel` flows by grepping for callers of `buildInvestigationPrompt` (actual lines 251 in `runInvestigation` and 455 in `runSlackChannelInvestigation` — both flows route through the modified helper)
- [x] Confirm no new dependencies were added (`go.mod` unchanged)

### Task 4: Rebuild docker containers and verify they started

Files:
- None (deployment step)

- [ ] Run `docker-compose build akmatori-api` (only the API server changed; per CLAUDE.md other containers are unaffected by changes in `cmd/` and `internal/`)
- [ ] Run `docker-compose up -d akmatori-api` to recreate the container with the new image
- [ ] Run `docker-compose ps` and confirm `akmatori-api` is in the `Up` / `running` state with no restart loop
- [ ] Run `docker-compose logs --tail=100 akmatori-api` and confirm: no panic/fatal lines, the HTTP server log line is present, and DB migrations completed cleanly
- [ ] Hit `GET http://localhost:<api-port>/api/health` (or the equivalent health endpoint exposed by the deployment) and confirm 200 OK
- [ ] Confirm the other containers (`akmatori-agent`, `mcp-gateway`, `frontend`, `qmd`, `db`) remain `Up` after the API restart via `docker-compose ps`

### Task 5: Update documentation and stage deployment notes

Files:
- Modify: `CLAUDE.md` (only if the prompt-rendering pattern is worth documenting as a project convention; otherwise skip)

- [ ] Add a short note in the PR / commit body documenting the SQL upgrade path for already-deployed databases: `UPDATE skills SET prompt = '<new prompt text>' WHERE name = 'incident-manager';` — make clear this is operator-driven (NOT auto-applied on boot, to preserve customizations)
- [ ] Move this plan to `docs/plans/completed/` after merge

## Post-Completion (manual verification — out of automated task scope)

- Replay the original Slack-channel alert that produced incident `c1eff0bc`. In the new incident's execution log, confirm:
  - The investigation prompt includes `Source:` and `Original alert text:` sections containing `"New notification from stream-health monitor"`
  - The first `qmd.query` call now carries a `searches[]` entry with the verbatim alert excerpt as the first (2×-weighted) sub-query
  - The response includes the matching runbook in the top results
- Spot-check a non-Slack alert (Zabbix or Alertmanager webhook) where `raw_payload.original_message` is absent — prompt should match prior output aside from the optional `Source:` line
