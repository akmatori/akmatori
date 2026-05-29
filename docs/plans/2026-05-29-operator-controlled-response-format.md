# Operator-Controlled Response Format with Paste-Example Schema

## Overview
Replace the hardcoded four-key JSON contract in the response formatter with an operator-configurable schema derived from a pasted example JSON object. Operators paste an example JSON object into a new "Output shape" textarea in Settings, and the formatter derives the field contract from it, validates LLM responses against that contract (with one retry), and auto-renders to Slack mrkdwn. Existing installs fall back to the built-in four-key default with zero behavior change.

## Context
- Files involved:
  - `internal/database/models_settings.go` — add `OutputSchemaExample`, update `DefaultFormattingPrompt`
  - `internal/api/types.go` — add `OutputSchemaExample *string` to request type
  - `internal/handlers/api_settings_formatting.go` — add validation for new field
  - `internal/services/response_formatter.go` — remove `formatterJSONInstruction`/`formatterResult`/`validateFormatterResult`/`renderFormatterResult`, wire schema-driven path
  - `internal/services/formatter_schema.go` (new) — `fieldSpec`, `inferSchema`, `validateAgainstSpecs`, `buildSchemaInstruction`
  - `internal/output/schema_render.go` (new) — `RenderForSlack(parsed, specs)`
  - `web/src/components/settings/FormattingSettingsSection.tsx` — new textarea section
  - `internal/services/response_formatter_test.go` — rewrite around inferred specs
  - `internal/output/schema_render_test.go` (new)
  - `internal/handlers/api_settings_formatting_test.go` — new save-validation cases
  - `CLAUDE.md` — update Response formatting rules section
- Related patterns: existing retry pattern at response_formatter.go:209-237; `formattingSystemPromptMax` (8192 byte cap); `output.FormatForSlack` stays untouched (used elsewhere)
- Dependencies: none external

## Development Approach
- **Testing approach**: Regular (code first, then tests per task)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Settings model and API layer

**Files:**
- Modify: `internal/database/models_settings.go`
- Modify: `internal/api/types.go`
- Modify: `internal/handlers/api_settings_formatting.go`
- Modify: `internal/handlers/api_settings_formatting_test.go`

- [x] Add `OutputSchemaExample string` field (GORM `type:text`) to `FormattingSettings` struct
- [x] Update `DefaultFormattingSettings()` to leave `OutputSchemaExample` empty (Format() will default to built-in example at runtime)
- [x] Rewrite `DefaultFormattingPrompt` constant to be tone/content guidance only — remove all references to specific field names (status, summary, actions_taken, recommendations)
- [x] Add `OutputSchemaExample *string` to `UpdateFormattingSettingsRequest` in `internal/api/types.go`
- [x] In `handleFormattingSettings` PUT path: when `OutputSchemaExample` is non-nil, validate it parses as JSON (400 with inline parse error), top-level is an object (400), and length <= 8192 bytes (400); on pass, assign to settings
- [x] Surface `OutputSchemaExample` in GET response
- [x] Add handler tests: PUT with invalid JSON → 400, PUT with non-object top-level (array, scalar) → 400, PUT with oversize example → 400, round-trip GET/PUT preserves value
- [x] Run `make test` — must pass

### Task 2: Schema inference helpers

**Files:**
- Create: `internal/services/formatter_schema.go`

- [x] Define `fieldSpec` struct with `Name`, `Kind` (string enum: "string"|"number"|"bool"|"list_string"|"list_number"|"list_object"|"object"), and `Children []fieldSpec`
- [x] Implement `inferSchema(example string) ([]fieldSpec, error)` using `json.Decoder` token walk to preserve key order; detect Kind from Go type after unmarshal; recurse into nested objects and array-of-object elements; empty arrays default to `list_string`
- [x] Implement `buildSchemaInstruction(example string) string` that pretty-prints the operator example and wraps it in the "Return ONLY a single JSON object matching exactly this shape" instruction text
- [x] Implement `validateAgainstSpecs(parsed map[string]any, specs []fieldSpec) []string` that checks every spec key is present, each value's type matches Kind (recursively), and returns human-readable error strings; extra keys are tolerated (dropped on render)
- [x] Define built-in default example constant (four-key shape) used when `OutputSchemaExample` is empty
- [x] Unit-test `inferSchema`: scalars, list-of-strings, list-of-objects, nested object, empty array (→ list_string), non-object top-level returns error
- [x] Unit-test `validateAgainstSpecs`: all-passing, missing key, wrong type, nested mismatch, extra key tolerated, empty array passes
- [x] Run `make test` — must pass

### Task 3: Auto-renderer

**Files:**
- Create: `internal/output/schema_render.go`
- Create: `internal/output/schema_render_test.go`

- [x] Implement `RenderForSlack(parsed map[string]any, specs []fieldSpec) string` that walks specs in order: string/number/bool → `*Title-Cased Key:* value\n`; list of scalars → `*Title-Cased Key:*\n • item\n` (omit section if empty list); list-of-objects → heading then each object as indented sub-block; nested object → heading then indented children
- [x] Implement title-case helper: split key on `_`/`-`, capitalize each word
- [x] Status emoji treatment: only when key is exactly `"status"` and value is one of `resolved|unresolved|escalate` — prepend ✅/⚠️/🚨 (same as existing `getStatusEmoji`); all other keys render as plain text
- [x] Return empty string when nothing renders (e.g. all arrays empty, no string fields)
- [x] Write `schema_render_test.go` covering: scalar types, list-of-strings (populated and empty), list-of-objects, nested object, mixed shapes, title-casing, status emoji for known values, non-status key with same values gets no emoji
- [x] Run `make test` — must pass

### Task 4: Response formatter refactor

**Files:**
- Modify: `internal/services/response_formatter.go`
- Modify: `internal/services/response_formatter_test.go`

- [x] Delete `formatterJSONInstruction` constant, `formatterResult` struct, `validateFormatterResult()`, and `renderFormatterResult()` from `response_formatter.go`
- [x] In `Format()`: load `settings.OutputSchemaExample`; if empty use built-in default example; call `inferSchema` to get specs; build `systemPrompt = operatorPrompt + buildSchemaInstruction(example)`; one-shot LLM call, `json.Unmarshal` to `map[string]any`, call `validateAgainstSpecs`; on failure append error list to user prompt and retry once (mirroring existing retry pattern)
- [x] Two consecutive failures → return `rawResponse`
- [x] On success call `output.RenderForSlack(parsed, specs)`; if render returns empty string → return `rawResponse`
- [x] Rewrite `response_formatter_test.go`: replace four-key-specific tests with table-driven cases — happy path with custom schema (severity/summary/hosts list), validation failure → retry → success, two failures → raw fallback, empty-render fallback, empty `OutputSchemaExample` uses built-in default and produces same Slack output as today
- [x] Run `make test` — must pass

### Task 5: Frontend changes

**Files:**
- Modify: `web/src/components/settings/FormattingSettingsSection.tsx`

- [ ] Add `outputSchemaExample` state variable and load it from API response in `loadSettings()`
- [ ] Include `output_schema_example` in `handleSave()` PUT body
- [ ] Add "Output shape" section below the system prompt textarea: heading, helper text ("Paste an example of the JSON object you want as the final summary…"), monospace textarea bound to `outputSchemaExample`
- [ ] Add client-side live JSON validation on blur: attempt `JSON.parse`; show a red helper line on parse error; disable Save button when invalid
- [ ] Add "Reset to default" button that refills the textarea with the built-in four-key example (hardcoded constant in the component matching the Go default)
- [ ] Disable the new textarea and reset button when the formatter toggle is off (mirrors existing prompt textarea disabled state)
- [ ] Run `make test-web` — must pass

### Task 6: Final verification and CLAUDE.md update

**Files:**
- Modify: `CLAUDE.md`

- [ ] Run `make test` (full Go suite)
- [ ] Run `make test-web`
- [ ] Update the "Response formatting" rules section in `CLAUDE.md`: remove reference to hardcoded four-key fields; describe `OutputSchemaExample` field, schema inference, operator-driven shape, auto-renderer; update note that empty `OutputSchemaExample` falls back to built-in four-key default
