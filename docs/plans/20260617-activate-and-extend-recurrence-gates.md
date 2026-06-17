# Activate and Extend Recurrence Gates (Phase 2)

## Overview

Phase 1 shipped alert fingerprinting, correlation/suppression gates, and live-reload config — but all gates are off and the suppression fast-path has zero signatures. This phase activates the dormant gates (Task 1), fixes the authoring bug that prevents suppression signatures from being written by memory-writer (Task 2), extends correlation to the dominant new waste pattern of recurring unresolved/blocked incidents beyond 30m (Task 3), adds a configurable fingerprint-gated wider lookback window (Task 4), and adds observability so operators can see gate effectiveness (Task 5).

## Context

- Files involved:
  - `akmatori_data/agents/memory-writer.md` (suppress: true guidance)
  - `internal/services/alert_correlator.go` (CorrelationConfig, fetchCandidates, verdict)
  - `internal/services/incident_service.go` (DefaultIncidentManagerPrompt, AppendCorrelatedAlert)
  - `internal/handlers/alert_processor.go` (gate routing, cheap recurrence update path)
  - `internal/handlers/api_memories.go` (PATCH suppress endpoint)
  - `internal/services/interfaces.go` (MemoryManager.SetSuppress)
  - `internal/services/memory_service.go` (SetSuppress impl)
  - `internal/database/models_settings.go` (AlertCorrelationFingerprintWindowMinutes, AlertCorrelationLongWindowDays)
  - `internal/api/types.go` (UpdateGeneralSettingsRequest new fields)
  - `internal/handlers/api_settings_general.go` (defaults + validation)
  - `web/src/components/settings/GeneralSettingsSection.tsx` (new setting inputs + warning badge)
  - new web components: suppression signatures panel, recurrence stats panel
- Related patterns:
  - Nullable pointer settings fields (nil → service default, set → operator override)
  - Fail-open gates (errors or disabled → spawn normally)
  - One-shot LLM via OneShotLLMCaller for cheap paths
  - memory-writer frontmatter with suppress: true ingested by IngestFromDisk
  - ProviderRegistry for Slack notifications
- Dependencies: None new

## Development Approach

- Regular: implement then tests per task; `make test` green before next task
- Task 1 is operational (no code changes); Tasks 2-5 are code changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Activate gates via settings API (config-only, no code changes)

**Files:** None

- [x] POST /api/settings/general with `{"alert_correlation_enabled": true, "alert_suppression_enabled": true, "alert_correlation_window_minutes": 30, "alert_correlation_threshold": 0.7, "alert_suppression_threshold": 0.8}` — no restart needed; live-reload active [manual op - not automatable]
- [x] Verify in API logs that the next alert goes through the correlation gate (not bypassed) [manual op - not automatable]
- [x] Record SQL baseline before and 12h after: `SELECT count(*) FROM alert_correlation_logs; SELECT count(*) FROM alert_suppression_logs; SELECT sum(correlated_count), count(*) FROM incidents WHERE started_at > now()-interval '12h';` [manual op - not automatable]
- [x] Note: suppression_logs will remain 0 until Task 2 ships (no signatures) — expected, fail-open [informational]

### Task 2: Fix suppression-signature authoring

The fast-path is starved by the authoring bug: memory-writer never emits suppress: true even when the verdict is "false positive / self-healing". Fix the authoring trigger and add operator affordance for human-in-the-loop control.

**Files:**
- Modify: `akmatori_data/agents/memory-writer.md`
- Modify: `internal/services/incident_service.go`
- Modify: `internal/handlers/api_memories.go`
- Modify: `internal/services/interfaces.go`
- Modify: `internal/services/memory_service.go`
- Create: `web/src/components/settings/SuppressionSignaturesSection.tsx`

- [x] Update memory-writer.md: when investigation verdict is "false positive / self-healing / safe to suppress", MUST write `suppress: true` in frontmatter plus explicit `Alert rule: <name>` and `Host pattern: <glob>` lines in the body
- [x] Update DefaultIncidentManagerPrompt in incident_service.go: when closing with an FP/self-healing verdict, explicitly instruct the memory-writer task to include `suppress: true`, the alert rule name, and the host pattern
- [x] Add `PATCH /api/memories/{id}/suppress` endpoint in api_memories.go with body `{"suppress": bool}`; route to new MemoryManager.SetSuppress; trigger SKILL.md regeneration
- [x] Add `SetSuppress(id uint, suppress bool) error` to MemoryManager interface in interfaces.go; implement in memory_service.go as a targeted single-field DB update that then calls regenerateSkillForMemoryScope
- [x] Web: SuppressionSignaturesSection.tsx — list active suppress=true memories (by scope), list benign-verdict memories without suppress=true as "candidate signatures", both rows have a flag/unflag toggle calling PATCH /api/memories/{id}/suppress
- [x] Tests: prompt string with FP verdict contains "suppress: true" expectation (unit test on the DefaultIncidentManagerPrompt text); PATCH /api/memories/{id}/suppress flips suppress column and triggers regen (handler test); suppressor end-to-end: seed a suppress=true memory, call Evaluate(), confirm suppression match returns correct signature_name
- [x] `make test` + `make test-web`

### Task 3: Long-window correlation for recurring blocked incidents + cheap recurrence update

Recurring real-but-blocked incidents recur over days (not 30m), so each currently costs a full ~400k-token re-investigation. Extend the candidate set to include fingerprint-matching unresolved/escalated incidents within 7d, and replace the full re-investigation with a cheap ~3k-token one-shot delta.

**Files:**
- Modify: `internal/services/alert_correlator.go`
- Modify: `internal/handlers/alert_processor.go`
- Modify: `internal/database/models_settings.go`
- Modify: `internal/services/incident_service.go`

- [x] Add `AlertCorrelationLongWindowDays *int` (default 7) to GeneralSettings; add to AutoMigrate
- [x] Add `LongWindowDays int` to CorrelationConfig; populate from AlertCorrelationLongWindowDays (default 7 when nil)
- [x] Add `IsLongWindowMatch bool` to CorrelationVerdict
- [x] In fetchCandidates: second query for fingerprint-matching incidents with status IN ('running','diagnosed') started within LongWindowDays; dedup by UUID against standard 30m candidates; tag matched rows so the LLM prompt labels them as "known open issue" candidates; set IsLongWindowMatch=true in the verdict when the chosen match came only from this long-window set
- [x] In alert_processor.go: when verdict.IsLongWindowMatch is true, call a new runRecurrenceUpdate(ctx, incidentUUID, incoming, verdict) instead of recordRecurrence
- [x] runRecurrenceUpdate: calls OneShotLLMCaller with a ~3k-token prompt ("still blocked — generate a 2-sentence delta for the Nth recurrence of this incident"); appends result via AppendCorrelatedAlert (existing atomic method, reuse reasoning field for the delta note); posts a short Slack thread reply to the incident's source channel via ProviderRegistry (reuse incident source channel resolution from alert_processor)
- [x] Guard: if incoming fingerprint is empty OR confidence < threshold, fall through to full spawn (fail-open); on OneShotLLMCaller error, fall through to full spawn
- [x] Tests: fingerprint + unresolved match at 4 days → IsLongWindowMatch=true, runRecurrenceUpdate called, no full spawn, correlated_count increments; confidence below threshold → full spawn; non-fingerprint at >30m → not long-window matched; resolved incident excluded from long-window candidates; LLM error in runRecurrenceUpdate → falls through to full spawn
- [x] `make test`

### Task 4: Fingerprint-gated adaptive correlation window

42% of recurrences arrive >30m apart, outside the current fixed window. Widen lookback only for fingerprint matches; keep narrow 30m for non-fingerprint title-only matching.

**Files:**
- Modify: `internal/database/models_settings.go`
- Modify: `internal/services/alert_correlator.go`
- Modify: `internal/api/types.go`
- Modify: `internal/handlers/api_settings_general.go`
- Modify: `web/src/components/settings/GeneralSettingsSection.tsx`

- [x] Add `AlertCorrelationFingerprintWindowMinutes *int` (default 1440) to GeneralSettings; add to AutoMigrate
- [x] Add `FingerprintWindow time.Duration` to CorrelationConfig; populate from AlertCorrelationFingerprintWindowMinutes (default 24h when nil)
- [x] In fetchCandidates: when incoming fingerprint is non-empty, extend the date filter to `FingerprintWindow` for the fingerprint-filtered sub-query; non-fingerprint candidates keep the standard Window (30m); dedup by UUID when merging both result sets
- [x] Add AlertCorrelationFingerprintWindowMinutes to UpdateGeneralSettingsRequest in api/types.go; add default (1440) in GET handler; add validation (min 1, max 10080 = 7d) in PUT handler
- [x] Frontend: add "Fingerprint correlation window (minutes)" labeled numeric input in the alert correlation settings row in GeneralSettingsSection.tsx; load and save alongside existing correlation fields
- [x] Tests: fingerprint match at 2h correlates with FingerprintWindow=1440; non-fingerprint at 40m does not correlate (30m window); field persists and is returned on GET; nil field returns 1440 default; invalid value returns 400
- [x] `make test` + `make test-web`

### Task 5: Recurrence and gate-effectiveness observability

Close the loop so operators can see the 40% fingerprint redundancy and whether the gates are working.

**Files:**
- Create: `internal/handlers/api_recurrence_stats.go`
- Modify: `internal/handlers/api.go`
- Create: `web/src/components/settings/RecurrenceStatsPanel.tsx`
- Modify: `web/src/components/settings/GeneralSettingsSection.tsx`

- [x] Add `GET /api/stats/recurrence` returning: top-10 fingerprints by correlated_count (last 7d with alert_name + target_host), estimated tokens saved (correlated_count × 412000 per entry), gate hit-rates from alert_correlation_logs and alert_suppression_logs (last 24h and last 7d), candidate suppression signatures (incident_pattern/feedback memories from benign-verdict incidents without suppress=true, last 7d)
- [x] In GeneralSettingsSection.tsx: fetch /api/stats/recurrence on load; show a yellow warning badge next to each gate toggle when that gate is disabled AND fingerprint redundancy rate (sum(correlated_count) / total incident count, last 24h) exceeds 20%
- [x] RecurrenceStatsPanel.tsx: fingerprint groups table (rule + host + count + est. saved tokens), gate hit-rate numbers (correlation gate: N correlated / M total; suppression gate: N suppressed / M total), candidate signature list with "Mark as signature" quick-action that calls PATCH /api/memories/{id}/suppress; embed panel in settings page alongside SuppressionSignaturesSection
- [x] Tests: /api/stats/recurrence aggregate counts match direct DB queries with seeded data; warning condition fires when gate is false and redundancy >20%; candidate list excludes suppress=true memories
- [x] `make test` + `make test-web`

### Task 6: Verify acceptance criteria

- [x] `make verify` (full test suite + linter + coverage)
- [x] Confirm alert_correlation_logs and alert_suppression_logs are receiving rows after 24h with gates enabled [manual verification - requires live deployment]
- [x] Confirm suppressor starts matching after one suppress-flagged memory is toggled via the web panel [manual verification - requires live deployment]
- [x] Verify AlertCorrelationFingerprintWindowMinutes persists and correlator reads it without restart [manual verification - requires live deployment]
- [x] Verify RecurrenceStatsPanel shows non-zero gate hit-rates after 24h of operation [manual verification - requires live deployment]
