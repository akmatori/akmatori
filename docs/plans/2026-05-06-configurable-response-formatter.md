# Configurable Post-Processing Prompt for Final Incident Response

## Overview

Add a global, user-configurable formatting prompt that is applied after the agent finishes investigating an incident. A new `ResponseFormatter` service runs an extra one-shot LLM call with full reasoning context (the agent's raw response plus the complete `full_log` of reasoning lines and tool calls) and returns a reformatted response. The reformatted text replaces `incident.response`, so the UI and Slack always show the structured output. The original raw reasoning is preserved in `incident.full_log` for debugging.

When the formatter is disabled, the worker is disconnected, or the LLM call fails, the system falls back to the raw agent response (today's behaviour). Slack's existing byte-budget summarizer continues to run after formatting, so over-budget structured responses still fit Slack.

## Context

- Files involved:
  - `internal/database/models_settings.go` — add new `FormattingSettings` singleton model
  - `internal/database/db.go` (or wherever `AutoMigrate` is called — `cmd/akmatori/main.go`) — register the new model
  - `internal/services/response_formatter.go` (new) — analogous to `slack_summarizer.go`, uses `OneShotLLMCaller`
  - `internal/services/response_formatter_test.go` (new)
  - `internal/handlers/slack_processor.go` (~line 260) — call formatter before `finalizeSlackMessageBody` and before `UpdateIncidentComplete`
  - `internal/handlers/alert_processor.go` (~lines 321 and 481) — same call sites for webhook-alert and Slack-channel-alert flows
  - `internal/handlers/api_settings_formatting.go` (new) — REST `GET/PUT /api/settings/formatting`
  - `internal/handlers/api_settings_formatting_test.go` (new)
  - `internal/api/types.go` — add `UpdateFormattingSettingsRequest`
  - `cmd/akmatori/main.go` — wire formatter into handler constructors and register the API route
  - `web/src/components/settings/FormattingSettingsSection.tsx` (new)
  - `web/src/pages/Settings.tsx` — add the new section
  - `CLAUDE.md` — short note about the new service and settings table

- Related patterns:
  - `SlackSummarizer` (`internal/services/slack_summarizer.go`) — same shape: `OneShotLLMCaller` + deterministic fallback + ctx timeout + structured fallback when worker unavailable
  - `RetentionSettings` (`internal/database/models_settings.go`, `internal/handlers/api_settings_retention.go`) — singleton settings pattern with `GetOrCreate*` + `Update*` helpers
  - `RetentionSettingsSection.tsx` — UI pattern for a singleton settings section

- Dependencies: none external. Reuses existing `OneShotLLMCaller` (already implemented by `AgentWSHandler`).

- Key design decisions:
  - Single global formatting prompt (singleton table), not per-skill / per-alert-source. Simpler scope, matches the user's request to "specify the prompt".
  - Stored in DB, not env var, so it can be edited from the UI without a restart.
  - The formatted output replaces `incident.response`; `full_log` is unchanged. No new column on the `incidents` table.
  - Default prompt provided so toggling `enabled=true` produces sensible output without forcing the user to author a prompt.
  - Format runs once per incident, reused for both UI and Slack — consistent output, one extra LLM call total.

## Development Approach

- Testing approach: Regular (code first, then tests), matching the existing services pattern (e.g. `slack_summarizer_test.go`). Use `testhelpers` mocks for `OneShotLLMCaller`.
- Complete each task fully before moving to the next.
- CRITICAL: every task MUST include new/updated tests.
- CRITICAL: all tests must pass before starting next task (`make test` for Go, `make test-web` for the frontend).

## Implementation Steps

### Task 1: Database model and migration

**Files:**
- Modify: `internal/database/models_settings.go`
- Modify: `cmd/akmatori/main.go` (or wherever `AutoMigrate` is invoked)
- Modify: `internal/database/models_test.go`

- [x] Add `FormattingSettings` struct with: `ID`, `SingletonKey` (unique index, default `"default"`), `Enabled` bool (default false), `SystemPrompt` text (with a sensible default), `MaxTokens` int (default 1500), `Temperature` float (default 0.2), `CreatedAt`, `UpdatedAt`. `TableName() string => "formatting_settings"`.
- [x] Add `DefaultFormattingSettings()` returning a populated struct (default prompt: instruct the model to produce a clean, structured incident summary preserving status, actions taken, and recommendations).
- [x] Add `GetOrCreateFormattingSettings()` and `UpdateFormattingSettings()` helpers, mirroring the retention helpers.
- [x] Register the new model in `AutoMigrate`.
- [x] Add unit tests covering defaults, GetOrCreate idempotency, and Update round-trip.
- [x] Run `make test` — must pass.

### Task 2: ResponseFormatter service

**Files:**
- Create: `internal/services/response_formatter.go`
- Create: `internal/services/response_formatter_test.go`

- [x] Implement `ResponseFormatter` with constructor `NewResponseFormatter(caller OneShotLLMCaller) *ResponseFormatter`.
- [x] Implement `Format(ctx context.Context, rawResponse, fullLog string) string`:
  - Loads `FormattingSettings`; if disabled or empty prompt, return `rawResponse`.
  - Loads active `LLMSettings`; if missing or worker disabled, return `rawResponse`.
  - Builds user message containing both the raw response and the full reasoning log clearly delimited (e.g. `--- Raw response ---` and `--- Full reasoning ---` sections), with a soft byte cap to avoid blowing up context (e.g. 60KB, truncating `fullLog` from the start to keep the tail near the final response).
  - Issues `OneShotLLM` with the configured system prompt, `MaxTokens`, `Temperature`. Default 30s timeout if caller has no deadline.
  - On `ErrWorkerNotConnected`, any other error, or empty result, log at appropriate level and return `rawResponse`.
- [x] Tests: enabled+success path, disabled (passthrough), missing LLM settings (passthrough), `ErrWorkerNotConnected` (passthrough), generic error (passthrough), empty caller result (passthrough), context-deadline propagation, large `fullLog` truncation.
- [x] Run `make test` — must pass.

### Task 3: Wire formatter into incident finalization

**Files:**
- Modify: `internal/handlers/slack_processor.go`
- Modify: `internal/handlers/alert_processor.go`
- Modify: `cmd/akmatori/main.go`
- Modify: relevant `_test.go` files for the three handler flows

- [x] In `cmd/akmatori/main.go`, instantiate `ResponseFormatter` (passing the same `agentWSHandler` already used as `OneShotLLMCaller`) and pass it to `SlackHandler` and `AlertHandler` constructors.
- [x] Add `responseFormatter *ResponseFormatter` field on `SlackHandler` and `AlertHandler`; update constructors and any handler interfaces in `internal/services/interfaces.go` if needed. (Used setter `SetResponseFormatter` to mirror the existing `SetSlackSummarizer` pattern and avoid breaking ~30 existing tests that pass nils to the constructors.)
- [x] In `slack_processor.go::processMessage` (~line 260): after `<-done` and `progressStreamer.Flush()`, before building `fullLog`, call `formatted := h.responseFormatter.Format(ctx, response, taskHeader+lastStreamedLog)` and use `formatted` everywhere `response` was the input to `UpdateIncidentComplete` and `finalizeSlackMessageBody`. Keep the unformatted `response` in the `fullLog` (so the raw final response remains in `full_log`).
- [x] Apply the same change in `alert_processor.go::runInvestigation` (~line 321) and `runSlackChannelInvestigation` (~line 481).
- [x] Update existing handler tests to cover: formatter passthrough (default disabled), formatter applied (mock returns transformed text and we assert the DB row + Slack post show that text), formatter fallback on caller error. (Covered via `applyResponseFormatter` helper — the three call sites all delegate to it, so unit tests on the helper exercise the wiring without the WebSocket-worker complexity. Setter wiring also tested.)
- [x] Run `make test` — must pass.

### Task 4: REST API endpoint

**Files:**
- Create: `internal/handlers/api_settings_formatting.go`
- Create: `internal/handlers/api_settings_formatting_test.go`
- Modify: `internal/api/types.go` (add `UpdateFormattingSettingsRequest`)
- Modify: `cmd/akmatori/main.go` (register `/api/settings/formatting` route)
- Modify: `docs/` swagger spec if user-facing API surface is documented there

- [ ] Implement `GET /api/settings/formatting` returning the singleton record.
- [ ] Implement `PUT /api/settings/formatting` accepting partial updates of `enabled`, `system_prompt`, `max_tokens`, `temperature` with validation (`max_tokens` 1..8000, `temperature` 0..2, `system_prompt` length <= 8KB).
- [ ] Tests covering GET defaults, PUT happy path, validation rejections, and error paths matching the retention handler tests style.
- [ ] Run `make test` — must pass.

### Task 5: Web UI settings section

**Files:**
- Create: `web/src/components/settings/FormattingSettingsSection.tsx`
- Modify: `web/src/pages/Settings.tsx`

- [ ] Build a settings section mirroring `RetentionSettingsSection.tsx`: enable toggle, multiline prompt textarea (with the default prompt prefilled when empty), max tokens number input, temperature number input, Save button calling `PUT /api/settings/formatting`.
- [ ] Show inline help text explaining that the formatter receives the agent's full reasoning + final response and the prompt controls the output structure.
- [ ] Add the section to `Settings.tsx`.
- [ ] Add a vitest unit test for the component (rendering, submit payload), matching the existing settings-section tests if they exist, otherwise minimal smoke test.
- [ ] Run `make test-web` — must pass.

### Task 6: Verify acceptance criteria

- [ ] Run `make verify` — all Go tests + vet pass.
- [ ] Run `make test-all` — Go + agent-worker + web tests pass.
- [ ] Manual smoke test: enable the feature, set a custom prompt ("Respond as JSON with `status` and `summary` keys"), trigger a Slack mention, confirm DB `incident.response` and the Slack thread reply both reflect the structured output, and `incident.full_log` still contains the raw reasoning.

### Task 7: Update documentation

- [ ] Update `CLAUDE.md`: add a short bullet under Services for `ResponseFormatter` (file + purpose) and a one-liner under Settings noting the new `formatting_settings` table and `/api/settings/formatting` endpoint.
- [ ] Move this plan to `docs/plans/completed/`.
