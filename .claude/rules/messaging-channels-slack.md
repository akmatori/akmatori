---
paths:
  - "**/internal/slack/**"
  - "**/internal/messaging/**"
  - "**/internal/handlers/slack_*.go"
  - "**/internal/handlers/alert_slack.go"
  - "**/internal/handlers/api_channels.go"
  - "**/internal/handlers/api_integrations.go"
  - "**/internal/services/channel_service.go"
  - "**/internal/services/slack_summarizer.go"
---

# Channels, Integrations, and outbound routing

Operators configure a messaging `Integration` (provider credentials) and `Channel` rows under it.
Triggers (alert sources, cron jobs, workspace default) reference Channels by UUID. Slack is
implemented; Telegram is a registry stub.

- never call Slack APIs directly from handlers or services — resolve a `Channel` and go through
  `ProviderRegistry.Get(channel.Integration.Provider)` (`PostMessage` / `PostThreadReply` /
  `UpdateMessage`). Never legacy `SlackSettings.AlertsChannel`. New providers register in
  `internal/messaging/` and are picked up by provider name.
- alert routing: `ChannelService.ResolveForAlertSource(asi)` — explicit `notification_channel_id`
  wins, else the provider's `is_default_post=true` Channel
- at most one `is_default_post=true` per provider (partial-unique index + service-layer check)
- inbound listening reads `Channel.ExtractionPrompt` + the `ProcessBotMessages`/`ProcessHumanMessages`
  source gates (`slack.go` dispatch); `process_bot_messages` backfills true on upgrade
  (preMigrate in db.go)
- `Channel.CanPost`/`CanListen` gate which triggers may reference a channel; `can_listen=true,
  can_post=false` = silent listener: alerts investigated, results UI-only, no replies/reactions/banner
  (listener flow + `incidentThreadPostable`)
- the `slack_channel` AlertSourceInstance type is deprecated and UI-hidden; do not reintroduce it
- Telegram requests surface `ErrNotImplemented` from the registry — never silently no-op

## Slack investigation UX

- long investigations use the Slack typing/banner flow, no placeholder reply
- typing state = `assistant.threads.setStatus` plus the hourglass reaction
- progress banner content comes from the latest reasoning line via `SlackProgressStreamer`
- final thread output is summarized to fit Slack byte limits; any new Slack-facing summary or
  banner text must truncate safely and degrade cleanly
- mention handling is classify-first: confident operator feedback is stored as memory; other
  mentions continue the investigation
- feedback ack: non-mention confident feedback → 👍 only; @mention confident feedback → emoji +
  short text; both use injectable `feedbackAcker`, best-effort
- session resume is NOT used — Slack chat starts a fresh agent session per turn

Key files: `internal/handlers/slack_processor.go` (message/mention handling, reads
`Channel.ExtractionPrompt`), `internal/handlers/slack_progress.go` (reasoning-line streaming),
`internal/handlers/alert_slack.go` (outbound routing), `internal/services/channel_service.go`
(`ResolveDefault`, `ResolveForAlertSource`), `internal/services/slack_summarizer.go`,
`internal/messaging/` (`Provider`, `ProviderRegistry`, slack provider, telegram stub)
