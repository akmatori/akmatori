---
paths:
  - "**/internal/services/response_formatter.go"
  - "**/internal/services/formatting_rule_matcher.go"
  - "**/internal/services/formatting_expression.go"
  - "**/internal/services/formatter_schema.go"
  - "**/internal/handlers/api_formatting_rules.go"
  - "**/internal/output/**"
  - "**/web/src/components/settings/Formatting*"
  - "**/web/src/components/settings/formatting*"
  - "**/web/src/components/settings/matchExpression*"
---

# Response formatting (per-flow rules)

Ordered `FormattingRule` rows (`/api/formatting-rules`, CRUD + `PUT /reorder`) are the ONLY
formatting mechanism; no match → raw response. `/api/settings/formatting` is 410 Gone;
`migrateGlobalFormattingToRule()` converts one enabled legacy row into a catch-all rule
(rules-table-empty guard).

- match fields (empty = wildcard, ANDed): `match_source_kind`, `match_source_uuid` (trigger),
  `match_channel_uuid` (destination), `match_last_skill` — OR a `match_expression`
  (`==/!=/&&/||/!`, parens, `and/or/not`; either/or with simple fields, API-enforced;
  invalid stored expr → rule skipped; Go parser + TS mirror `matchExpression.ts` must stay in sync)
- `FormatForFlow(ctx, raw, fullLog, FormatFlow)` never errors — failures collapse to passthrough;
  skips error responses; cron formats only on match (`full_log` keeps raw); proposal chat never formats
- flow identity via `BuildFormatFlow(incidentUUID, channelUUID)`; Slack chat channel via `FindByExternalID`
- `Incident.LastSkillUsed`: worker latches the last `<skillsDir>/<name>/SKILL.md` read, sends
  `last_skill` on `agent_completed`; `agent_ws.go` persists it BEFORE `dispatchOnCompleted`
  (guard: `isCurrentRun`)
- blank rule fields fall back: prompt → `DefaultFormattingPrompt`, schema → four-key default
  (`status`/`summary`/`actions_taken`/`recommendations`), `MaxTokens<=0` → 1500; NO gorm default
  tags (explicit false/0 must persist)
- `inferSchema` derives specs from the example; schema instruction appended automatically (never
  repeat it in the prompt); `validateAgainstSpecs` + one retry, then raw; renders via
  `output.RenderForSlack` (empty → raw)
- rule editor `hydrateField`/`dehydrateField` keep backend fallbacks authoritative;
  `output.FormatForSlack` unchanged — keep it

Key files:
- `internal/services/response_formatter.go` — rule-driven rewrite stage (`FormatForFlow`)
- `internal/services/formatting_rule_matcher.go` — `FormatFlow`, `MatchFormattingRule`, `BuildFormatFlow`
- `internal/services/formatter_schema.go` — `inferSchema`, `buildSchemaInstruction`, `validateAgainstSpecs`
- `internal/output/schema_render.go` — schema-driven Slack mrkdwn rendering
- `internal/handlers/api_formatting_rules.go` — CRUD + reorder
- `web/src/components/settings/FormattingRulesSection.tsx` (shared fields in `FormattingConfigFields.tsx`)
- `web/src/components/settings/formattingSettingsHelpers.ts` — keep constants aligned with Go defaults
