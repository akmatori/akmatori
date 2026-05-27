# Response Formatter: JSON Schema Validation + Retry

## Overview
Add structured JSON output enforcement to the response formatter: the LLM is instructed to produce a specific JSON object, the result is validated in Go, and on failure one retry is issued with the validation errors appended. Success renders via the existing `output.FormatForSlack` path. Two consecutive failures fall back to the raw response, preserving all existing graceful-degradation gates.

## Context
- Files involved:
  - `internal/services/response_formatter.go` — all new logic (DTO, const, validate, render, retry)
  - `internal/database/models_settings.go` — updated `DefaultFormattingPrompt` to describe JSON contract
  - `internal/services/response_formatter_test.go` — updated + new tests
  - `internal/services/title_generator_test.go` — extend `fakeOneShotLLMCaller` with sequenced-response support
- Related patterns:
  - `internal/output/parser.go` — `FinalResult` struct is reused as the render target
  - `internal/output/slack_formatter.go` — `FormatForSlack(*ParsedOutput)` is the single renderer
  - All existing passthrough gates in `Format()` (nil caller, disabled, no LLM key, worker disconnected, empty output) must be preserved exactly
- Dependencies: none new

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add JSON DTO, validation, rendering, and retry to response_formatter.go

**Files:**
- Modify: `internal/services/response_formatter.go`

- [x] Add `formatterResult` struct with json tags: `Status`, `Summary string`; `ActionsTaken`, `Recommendations []string`
- [x] Add `formatterJSONInstruction` package-level const: appended to the system prompt, instructs the LLM to return a single JSON object with no fences, all four keys present, empty arrays allowed for list fields
- [x] Add `validateFormatterResult(raw string) (*formatterResult, []string)`: strips stray triple-backtick fences, calls `json.Unmarshal`, returns the parsed struct and a slice of validation-error strings; requires non-empty `status` and `summary` (empty `actions_taken`/`recommendations` arrays are valid)
- [x] Add `renderFormatterResult(r *formatterResult) string`: maps `formatterResult` to `output.FinalResult`, wraps it in `output.ParsedOutput{FinalResult: &fr}`, calls `output.FormatForSlack`; returns empty string on nil input
- [x] Update `Format()` to append `formatterJSONInstruction` to the system prompt before the `OneShotLLM` call; after receiving a non-empty response, call `validateFormatterResult`; on validation failure, issue one retry with the validation errors and "return only corrected JSON" appended to the user prompt; validate again; on second failure or retry-call error, return `rawResponse`; on success call `renderFormatterResult` and return the rendered string (empty render → raw fallback)
- [x] Preserve all existing early-return gates (nil caller, disabled, no LLM settings, no API key, no worker, empty output)
- [x] Run `make test` — must pass before Task 2

### Task 2: Update DefaultFormattingPrompt in models_settings.go

**Files:**
- Modify: `internal/database/models_settings.go`

- [x] Rewrite `DefaultFormattingPrompt` to describe the JSON contract: explain that the LLM should produce a JSON object matching the four-field schema, describe what belongs in each field, keep the tone/content guidance (factual, concise, preserves identifiers); note that the JSON instruction suffix is appended automatically so the prompt should focus on content/tone
- [x] Run `make test` — must pass before Task 3

### Task 3: Update and extend the test suite

**Files:**
- Modify: `internal/services/response_formatter_test.go`
- Modify: `internal/services/title_generator_test.go` (extend `fakeOneShotLLMCaller`)

- [x] Add sequenced-response support to `fakeOneShotLLMCaller` in `title_generator_test.go`: add a `responses []func(ctx context.Context) (string, error)` field; in `OneShotLLM` use the call index to pick from `responses` (falling back to `respond` when the slice is exhausted or nil)
- [x] Update `TestResponseFormatter_HappyPathReturnsLLMOutput`: LLM now returns valid JSON; assert `callCount()==1` and that the result contains rendered Slack sections (status emoji / `*Resolved*`, `*Summary*`, etc.) instead of raw JSON; assert system prompt contains `formatterJSONInstruction` suffix
- [x] Update `TestResponseFormatter_EmptyPromptUsesDefaultPrompt`: same — LLM returns valid JSON, result is rendered output
- [x] Add `TestResponseFormatter_RetryOnValidationFailure`: first call returns invalid JSON, second call returns valid JSON; assert `callCount()==2`, retry user prompt contains validation error strings, final result is rendered output
- [x] Add `TestResponseFormatter_FallbackAfterTwoValidationFailures`: both calls return invalid JSON; assert `callCount()==2`, result is raw response
- [x] Add `TestResponseFormatter_FallbackOnRetryCallError`: first call returns invalid JSON, second call returns `errors.New("boom")`; assert `callCount()==2`, result is raw response
- [x] Add `TestResponseFormatter_MissingRequiredFieldTriggersRetry`: first call returns JSON with empty `summary`, second call returns valid JSON; assert `callCount()==2`, final result is rendered
- [x] Add unit tests for `validateFormatterResult`: valid JSON, invalid JSON, fenced JSON (backtick-stripped), missing `status`, missing `summary`, empty arrays ok, completely empty string
- [x] Add unit tests for `renderFormatterResult`: nil input returns empty string, valid input returns non-empty rendered string containing status and summary content
- [x] Verify all existing passthrough tests still pass without modification
- [x] Run `make test` — must pass before Task 4

### Task 4: Verify acceptance criteria

- [x] Run `make test` (full Go test suite)
- [x] Run `make verify` (pre-commit gate)
- [x] Confirm retry path exercised: `callCount()==2` in retry tests, `callCount()==1` in happy-path tests
- [x] Confirm no existing passthrough test needed modification beyond the happy-path output shape change
