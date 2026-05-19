# Akmatori: Unified Channels + Cron Jobs

## Overview

Introduce a first-class Channel concept (under provider Integrations) that triggers — alert sources, crons, the workspace default — reference by UUID. Replace the `slack_channel` AlertSourceInstance type and the global `SlackSettings.AlertsChannel` with explicit Channel rows. Then build cron jobs on top of that abstraction. Slack-only implementation in this iteration; Telegram provider remains a registry stub so the data model is ready when it lands.

## Context

- Files involved (backend):
  - `internal/database/models_settings.go` — extend/deprecate `SlackSettings`
  - `internal/database/models_alerts.go` — add `notification_channel_id` to `AlertSourceInstance`
  - `internal/database/models_incidents.go` — add `source_kind`, `source_uuid`
  - `internal/database/db.go` — migration / AutoMigrate hook
  - `internal/messaging/` (new) — `Provider`, `ProviderRegistry`, slack provider, telegram stub
  - `internal/services/channel_service.go` (new)
  - `internal/services/cron_runner.go` (new)
  - `internal/services/interfaces.go` — extend with `ChannelService`, `CronService`, `ProviderRegistry`
  - `internal/handlers/alert_slack.go` — route through provider
  - `internal/handlers/slack.go` — `LoadListenerChannels`
  - `internal/handlers/slack_processor.go` — extraction prompt from `Channel.ExtractionPrompt`
  - `internal/handlers/alert_processor.go` — pass channel resolution; emit `source_kind`
  - `internal/handlers/api.go` — new route registrations
  - `internal/handlers/api_integrations.go`, `api_channels.go`, `api_cron_jobs.go` (new)
  - `cmd/akmatori/main.go` — start `CronRunner`
- Files involved (frontend, `web/src/`):
  - `components/settings/IntegrationsManager.tsx` (new)
  - `components/channels/ChannelsManager.tsx` (new)
  - `components/channels/ChannelPicker.tsx` (new)
  - `components/alerts/AlertSourceForm.tsx` — replace slack_channel branch with `ChannelPicker`
  - `components/settings/SlackSettingsSection.tsx` — collapse into Integrations flow
  - `components/cron/CronJobsManager.tsx`, `components/cron/CronJobForm.tsx` (new)
- Related patterns:
  - Service-interface-at-handler-boundaries (`internal/services/interfaces.go`)
  - GORM AutoMigrate convention used today
  - `OneShotLLMCaller` for non-agent LLM calls
  - `alert_processor.go:28-98` agent spawn pattern (mirrored by cron agent mode)
  - `SlackProgressStreamer` / Slack byte-budget rules
- Dependencies:
  - `github.com/robfig/cron/v3` (new Go dep) for cron scheduling

## Development Approach

- **Testing approach**: Regular (code first, then tests) — backend uses GORM table-driven and handler integration tests; frontend uses vitest.
- Complete each task fully before moving to the next.
- Land in the order specified so each step is independently shippable; keep `SlackSettings.AlertsChannel` as a read fallback until Task 10.
- Preserve graceful degradation: provider absence (Telegram) and missing Channel must not crash the API.
- Migration must be read-old → write-new → don't-delete-old-until-verified, one transaction per step, idempotent on re-run.
- **CRITICAL: every task MUST include new/updated tests.**
- **CRITICAL: all tests must pass before starting the next task.**

## Implementation Steps

### Task 1: Schema + idempotent migration

**Files:**
- Modify: `internal/database/models_settings.go`, `internal/database/models_alerts.go`, `internal/database/models_incidents.go`, `internal/database/db.go`
- Create: `internal/database/models_channels.go` (`Integration`, `Channel`), `internal/database/models_cron.go` (`CronJob`)

- [x] add `Integration`, `Channel`, `CronJob` models with the fields specified in /tmp/plan.md (UUID public id, FK relationships, `is_default_post`, `can_post`, `can_listen`, `extraction_prompt`, `process_human_messages`, cron fields incl. `next_run_at`, `last_run_status`)
- [x] add `notification_channel_id *uint` (nullable FK to channels) on `AlertSourceInstance`
- [x] add `source_kind` and `source_uuid` columns on `Incident`
- [x] register new models in AutoMigrate; add partial-unique index ensuring at most one `is_default_post=true` per provider
- [x] write an idempotent backfill (single transaction per step) that: (a) if a `SlackSettings` row exists, inserts one `integrations` row (`provider=slack`, credentials from tokens) plus one `channels` row from `alerts_channel` with `is_default_post=true`; (b) for each `AlertSourceInstance` of type `slack_channel`, inserts a `channels` row with `can_listen=true` and the existing `extraction_prompt`/`process_human_messages`, then deletes that AlertSourceInstance; (c) marks the `slack_channel` row in `alert_source_types` deprecated (hidden from UI)
- [x] add tests: migration runs cleanly on empty DB; migration backfills correctly on a seeded DB; migration is a no-op on re-run
- [x] run `make test` — must pass before Task 2

### Task 2: Provider abstraction + ChannelService

**Files:**
- Create: `internal/messaging/provider.go`, `internal/messaging/registry.go`, `internal/messaging/slack_provider.go`, `internal/messaging/telegram_stub.go`
- Create: `internal/services/channel_service.go`
- Modify: `internal/services/interfaces.go`, `internal/slack/manager.go` (expose the underlying client needed by the slack provider)

- [x] define `Provider` interface (`Name`, `PostMessage`, `PostThreadReply`, `UpdateMessage`) and `ProviderRegistry` interface (`Get`, `List`)
- [x] implement slack provider as a thin wrapper over the existing `internal/slack/` client
- [x] add telegram stub that is either absent from the registry or returns `ErrNotImplemented` — make the gap explicit, not silent
- [x] implement `ChannelService` with CRUD + `ResolveDefault(provider)` + `ResolveForAlertSource(asi *AlertSourceInstance)` (falls back to default when `notification_channel_id` is nil)
- [x] extend `internal/services/interfaces.go` with `ChannelService` and `ProviderRegistry` handler dependencies
- [x] add unit tests for `ChannelService` (default resolution, fallback path, partial-unique-index enforcement) and registry (`Get` returns provider, returns error for unknown)
- [x] run `make test` — must pass before Task 3

### Task 3: Route outbound through Channel + Provider

**Files:**
- Modify: `internal/handlers/alert_slack.go`, `internal/handlers/alert_processor.go`

- [x] replace direct `GetSlackSettings().AlertsChannel` lookup with `ChannelService.ResolveForAlertSource(asi)` → `ProviderRegistry.Get(channel.Integration.Provider).PostMessage(ctx, channel, ...)`
- [x] keep `SlackSettings.AlertsChannel` as a read-only fallback when no Channel rows exist (graceful degradation); log a one-time deprecation warning
- [x] set `incidents.source_kind="alert"` and `source_uuid=<alert_source_instance.uuid>` on creation
- [x] update existing tests in `internal/handlers/alert_slack_integration_test.go` and add new tests covering: alert source with explicit `notification_channel_id` routes to that channel; alert source without channel uses default; missing default falls back to legacy `SlackSettings.AlertsChannel`
- [x] run `make test` — must pass before Task 4

### Task 4: Integrations + Channels CRUD API

**Files:**
- Create: `internal/handlers/api_integrations.go`, `internal/handlers/api_channels.go`
- Modify: `internal/handlers/api.go`, `docs/` (OpenAPI spec)

- [x] implement REST handlers: `GET/POST /api/integrations`, `GET/PUT/DELETE /api/integrations/{uuid}`
- [x] implement REST handlers: `GET /api/channels` (with `?integration_uuid=&can_post=&can_listen=` filters), `POST /api/channels`, `GET/PUT/DELETE /api/channels/{uuid}`
- [x] validation: at most one `is_default_post=true` per provider (service-layer check matching the DB partial-unique index); `external_id` non-empty for slack; provider must be a known registry name on create
- [x] register routes in `internal/handlers/api.go`; keep `/api/settings/slack` returning 308 redirect to `/api/integrations` for one release
- [x] add handler tests (parallel to `api_handler_test.go` style) covering CRUD happy paths, validation errors, default-post uniqueness
- [x] update OpenAPI spec under `docs/`
- [x] run `make test` — must pass before Task 5

### Task 5: Frontend — Integrations, Channels, ChannelPicker, AlertSourceForm

**Files:**
- Create: `web/src/components/settings/IntegrationsManager.tsx`, `web/src/components/channels/ChannelsManager.tsx`, `web/src/components/channels/ChannelPicker.tsx`
- Modify: `web/src/components/alerts/AlertSourceForm.tsx`, `web/src/components/settings/SlackSettingsSection.tsx` (collapse into Integrations), router config

- [x] build `IntegrationsManager` with "Add Slack" enabled and "Add Telegram" disabled with a "coming soon" tooltip; credentials form per provider
- [x] build `ChannelsManager` cross-provider table with role chips (post/listen/default badges); add/edit form with provider picker + `external_id` input
- [x] build `ChannelPicker` dropdown filtered by `can_post=true`, showing provider icon + display name
- [x] update `AlertSourceForm`: drop the `slack_channel`-specific branch; add `ChannelPicker` bound to `notification_channel_id`; leave webhook-source types unchanged
- [x] add routes `/settings/integrations`, `/settings/channels`; `/settings/slack` redirects to `/settings/integrations`
- [x] add vitest tests for `ChannelPicker` (filters, defaults), `ChannelsManager` (role chips render correctly), and the migrated `AlertSourceForm`
- [x] run `make test-web` — must pass before Task 6

### Task 6: Inbound listener migration

**Files:**
- Modify: `internal/handlers/slack.go` (the `LoadAlertChannels` function around lines 125-150), `internal/handlers/slack_processor.go`
- Modify: `internal/database/db.go` (or seed) to remove the `slack_channel` alert-source type from active types

- [x] rename `LoadAlertChannels` to `LoadListenerChannels`; source rows from `channels` where `can_listen=true`
- [x] change `slack_processor.go` extraction-prompt source from alert-source `Settings` JSONB to `Channel.ExtractionPrompt`; same for `ProcessHumanMessages`
- [x] hide `slack_channel` from the alert-source-types picker on the API/UI level (already-deprecated from Task 1 migration)
- [x] update tests in `internal/handlers/slack_test.go`, `slack_processor` tests, and `slack_integration_test.go` to seed `Channel` rows instead of `slack_channel` AlertSourceInstances
- [x] run `make test` — must pass before Task 7

### Task 7: Cron model + scheduler + oneshot mode

**Files:**
- Create: `internal/services/cron_runner.go`, `internal/handlers/api_cron_jobs.go`
- Modify: `internal/services/interfaces.go`, `internal/handlers/api.go`, `cmd/akmatori/main.go`, `go.mod`

- [x] add `github.com/robfig/cron/v3` to go.mod
- [x] implement `CronRunner` with `Start(ctx)` (loads enabled `CronJob` rows and registers each), `Reload(jobID)` (re-registers after CRUD), schedule validation at write-time, and `NextRunAt` computation
- [x] oneshot tick path: call `OneShotLLMCaller` with the cron's prompt, format the result, post to the cron's `Channel` via `ProviderRegistry`; update `LastRunAt`/`LastRunStatus`/`LastRunError`
- [x] CRUD endpoints: `GET/POST /api/cron-jobs`, `GET/PUT/DELETE /api/cron-jobs/{uuid}`, `POST /api/cron-jobs/{uuid}/run` (manual fire); validate cron expression at write time
- [x] wire `CronRunner.Start` in `cmd/akmatori/main.go` after DB + handlers, before HTTP listen; ensure shutdown cancels the runner
- [x] add tests: cron schedule validation rejects bad expressions; oneshot tick posts to the configured Channel; manual-fire endpoint returns success and runs the job; runner survives provider error and records `LastRunStatus=error`
- [x] run `make test` — must pass before Task 8

### Task 8: Cron agent mode

**Files:**
- Modify: `internal/services/cron_runner.go`, `internal/handlers/alert_processor.go` (extract reusable agent-spawn helper if not already factored)

- [x] agent tick path: spawn the incident-manager skill via `SkillService.SpawnIncidentManager`, mirroring `alert_processor.go:28-98`; create an `Incident` row with `source_kind="cron"` and `source_uuid=<cron_job.uuid>`; on completion, post the final summary to the cron's Channel
- [x] reuse global agent settings (skills, tool allowlist, LLM settings) — per-cron overrides are out of scope
- [x] add tests: agent-mode cron creates an Incident with the correct provenance; final summary lands on the configured Channel; tick failure records `LastRunStatus=error` without crashing the runner
- [x] run `make test` — must pass before Task 9

### Task 9: Frontend cron pages

**Files:**
- Create: `web/src/components/cron/CronJobsManager.tsx`, `web/src/components/cron/CronJobForm.tsx`
- Modify: router config

- [x] build `CronJobsManager` list page (name, schedule, channel, mode, enabled, last-run badge)
- [x] build `CronJobForm`: name, description, schedule (preset dropdown + "advanced" raw cron input), prompt textarea, mode radio (oneshot/agent), `ChannelPicker`, enabled toggle, parsed next-run preview
- [x] add `/cron` route
- [x] add vitest tests for the form (schedule validation surfacing, next-run preview, mode toggle) and list page
- [x] run `make test-web` — must pass before Task 10

### Task 10: Cleanup — remove SlackSettings.AlertsChannel fallback

**Files:**
- Modify: `internal/handlers/alert_slack.go`, `internal/handlers/api_settings_slack.go`, `internal/database/models_settings.go`, `internal/services/interfaces.go` (and any remaining `GetSlackSettings().AlertsChannel` callers)

- [x] remove the legacy read-fallback path added in Task 3; require a Channel row (return a clear error if missing)
- [x] remove `/api/settings/slack` redirect (or downgrade to 410 Gone) and delete `SlackSettingsSection.tsx` if still present
- [x] note: `slack_settings` table drop is deferred to a follow-up release per /tmp/plan.md
- [x] update tests removed/changed by the fallback removal; add a test asserting missing-default-Channel surfaces a clear 4xx
- [x] run `make test` — must pass before Task 11

### Task 11: Verify acceptance criteria

- [x] run full backend suite: `make test`
- [x] run frontend suite: `make test-web`
- [x] run adapter and MCP suites: `make test-adapters`, `make test-mcp`
- [x] run linter / full gate: `make verify`
- [x] run `go test -coverprofile=coverage.out ./...` and verify coverage for `internal/services/channel_service.go` (85.2%), `internal/services/cron_runner.go` (81.1%), `internal/messaging/` (88.9% pkg; slack_provider 84.9%, registry 95.7%, telegram_stub 100%), `internal/handlers/api_integrations.go` (93.7%), `internal/handlers/api_channels.go` (81.4%), `internal/handlers/api_cron_jobs.go` (93.6%) — all at or above 80%

### Task 12: Update documentation

- [x] update `CLAUDE.md`: replace "Outbound posting is global" assumption; add Channels/Integrations/Cron sections to "Important Files by Responsibility"; add a Rules section on Provider abstraction and Channel resolution
- [x] update `README.md` user-facing sections on Slack setup to point at Integrations + Channels flow; document cron jobs feature briefly
- [x] update OpenAPI specs in `docs/` (already touched in Task 4) — final cross-check against shipped routes
- [x] verify `wc -c CLAUDE.md` stays under 30000 bytes

## Post-Completion (manual verification, per /tmp/plan.md)

- Channels migration: start API against an existing DB with one `SlackSettings` row and one `slack_channel` `AlertSourceInstance`; verify one `integrations` row, one `channels` row with `is_default_post=true`, one `channels` row with `can_listen=true`, the AlertSourceInstance gone, no behavior regression on inbound Slack mentions; re-run startup → no-op.
- Outbound routing: configure `#incidents` (default) and `#staging-alerts` (post, not default); create two webhook alert sources, one pointing at `#staging-alerts`, one without; fire webhooks; verify each lands in the correct channel.
- Inbound listening: send a human message into a listener channel; verify extraction uses that channel's `extraction_prompt` and an incident is created.
- Cron oneshot: cron `*/2 * * * *`, mode=oneshot, prompt "List the number of incidents today", channel `#staging-alerts`; verify a message arrives within one tick and `LastRunAt`/`LastRunStatus=ok` update.
- Cron agent: cron `0 9 * * *`, mode=agent, prompt "Check disk usage on all hosts and flag any > 90%"; trigger via `POST /api/cron-jobs/{uuid}/run`; verify Incident with `source_kind=cron`, investigation runs, final summary posts to the configured channel.
- Rebuild affected containers per CLAUDE.md table (akmatori-api, frontend).

## Out of Scope (follow-ups)

- Telegram provider implementation (registry stub only; data model ready).
- Multi-workspace UI (data model supports N integrations per provider; UI assumes 1).
- Channel discovery via `conversations.list`; manual entry only in MVP.
- Per-cron LLM-settings / skills / tool-allowlist overrides; crons use globals.
- Permissions / multi-tenant.
- Dropping the `slack_settings` table (planned for a release after Task 10 ships).
