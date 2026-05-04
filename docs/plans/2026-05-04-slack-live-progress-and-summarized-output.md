# Slack live progress + summarized final output + provider-agnostic one-shot LLM calls

## Overview

Bring akmatori's Slack experience closer to openclaw's: replace the static "Thinking..." placeholder with a live, condensed progress stream rendered via Slack's native `chat.appendStream` API, and replace the giant truncated final message with a single LLM-summarized message that fits Slack's 12k/40k character limits while still pointing to the Akmatori UI for full detail.

To make the summarizer provider-agnostic (Anthropic, Google, OpenRouter, custom — not just OpenAI), introduce a worker-side one-shot LLM path that reuses pi-mono's underlying `pi-ai` SDK via the existing agent worker WebSocket. As part of the same change, migrate the two existing OpenAI-only callsites — `internal/services/title_generator.go` and `internal/alerts/extraction/extractor.go` — to use the same one-shot path so they no longer fall back when a non-OpenAI provider is active.

Output-format/template configuration (concern #3 from the original request) remains intentionally out of scope.

## Approach

- Worker-side: add an `oneshot_llm_request` / `oneshot_llm_response` WebSocket message pair handled in `agent-worker/src/orchestrator.ts` that imports `complete` from `@mariozechner/pi-ai` (already an explicit dep in `agent-worker/package.json`). This unlocks every provider akmatori supports without re-implementing per-provider HTTP clients.
- API-side: add an `OneShotLLM(...)` method on `AgentWSHandler` that correlates request/response by `request_id` over the existing worker WebSocket, with a clean error when no worker is connected so callers can fall back deterministically.
- Slack progress: add a small `SlackProgressStreamer` that calls `slack.Client.AppendStream(...)` from `OnOutput` and falls back to the existing `chat.update` path when `StartStream` was not used. Condense agent OnOutput into compact status lines (running/ran/thinking) before appending — we do NOT mirror full transcripts to Slack.
- Slack final message: add `internal/services/slack_summarizer.go` that calls `OneShotLLM` with a tight system prompt to compress the formatted result under a configurable byte budget, with a deterministic byte-truncation fallback when the worker is unavailable or the LLM returns over-budget output.
- Migrate `internal/services/title_generator.go` AND `internal/alerts/extraction/extractor.go` from their hand-rolled OpenAI HTTP clients to the same `OneShotLLMCaller` interface, so all three callsites share one provider-agnostic implementation.

## Context

- Files to read for orientation:
  - `/opt/openclaw/extensions/slack/src/streaming.ts` (reference) — Slack startStream/appendStream/stopStream wrapper pattern we are mirroring
  - `internal/handlers/alert_slack.go` — current Slack message lifecycle (StartStream / progress UpdateMessage / final UpdateMessage with byte truncation)
  - `internal/handlers/alert.go` — `slackMaxTextBytes`, footer construction
  - `internal/handlers/slack_processor.go` — current "Thinking..." flow on @mention
  - `internal/handlers/alert_processor.go` — `runSlackChannelInvestigation` flow
  - `internal/handlers/agent_ws.go` — `AgentWSHandler`, `IncidentCallback` registration, `SendToWorker` — model for request/response correlation
  - `internal/services/title_generator.go` — current OpenAI-only call to migrate
  - `internal/alerts/extraction/extractor.go` — current OpenAI-only call to migrate
  - `internal/handlers/slack.go` — wires `extraction.NewAlertExtractor()` into the SlackHandler; constructor needs to accept an `OneShotLLMCaller`
  - `internal/output/parser.go` and `internal/output/slack_formatter.go` — structured FINAL_RESULT block shape and Slack formatter
  - `agent-worker/src/orchestrator.ts`, `src/agent-runner.ts`, `src/ws-client.ts`, `src/types.ts` — current WS message types and `resolveModel`/`applyProxyConfig` logic to reuse
- New helpers introduced:
  - `agent-worker/src/oneshot-llm.ts` — pi-ai `complete(...)` wrapper with `{ requestId, system, user, maxTokens, temperature, llmSettings, proxyConfig }` input
  - `internal/handlers/slack_progress.go` — `SlackProgressStreamer` with `AppendStatus` + condensed parser
  - `internal/services/slack_summarizer.go` — `SlackSummarizer` with byte-budget enforcement
  - `internal/output/slack_budget.go` — deterministic byte-truncation fallback shortener
  - `agent-worker/src/ws-client.ts` — add a `sendOneshotResponse(...)` helper that mirrors `sendOutput`/`sendCompleted`/`sendError`
- Related patterns:
  - openclaw uses Slack SDK's `chatStream()` (chat.startStream / chat.appendStream / chat.stopStream) for the live updating message
  - akmatori's `slack-go/slack v0.20.0` already supports `AppendStream(channel, ts, opts...)` (verified in `~/go/pkg/mod/github.com/slack-go/slack@v0.20.0/chat.go`)
  - akmatori already calls `StartStream`/`StopStream` in `alert_slack.go` — only `AppendStream` integration is new
  - The agent worker imports `complete` from `@mariozechner/pi-ai` (already an explicit dep at `^0.72.1`) and reuses `resolveModel` from `agent-runner.ts`
  - `IncidentCallback` registration in `agent_ws.go` is the precedent for the request/response correlation we need for `oneshot_llm`
  - `ProxySettings.LLMEnabled` gates proxy usage for LLM calls; the worker's existing `applyProxyConfig` is reused
- Dependencies: none new. `@mariozechner/pi-ai ^0.72.1` is already explicit in `agent-worker/package.json`.

## Development Approach

- Testing approach: regular (write code, then table-driven tests for parsers/correlation logic and integration tests for the wired flows)
- Complete each task fully before moving to the next; each task includes test items
- CRITICAL: every task MUST include new/updated tests
- CRITICAL: all tests must pass before starting next task (`make test`, `make test-agent` for worker changes, and `make verify` before commits)
- Keep changes scoped — do NOT refactor unrelated Slack helpers, do NOT add output-template config, do NOT introduce multi-message replies

## Implementation Steps

### Task 1: Worker-side oneshot LLM handler using pi-ai

**Files:**
- Create: `agent-worker/src/oneshot-llm.ts`
- Create: `agent-worker/src/oneshot-llm.test.ts`
- Modify: `agent-worker/src/agent-runner.ts` — `resolveModel` and `mapThinkingLevel` are already exported; extract `applyProxyConfig` to a shared helper module so oneshot calls can reuse it
- Modify: `agent-worker/src/types.ts` — extend `WebSocketMessage` with optional fields used by oneshot: `request_id`, `system`, `user`, `max_tokens`, `temperature`, `summary` (response payload); extend message-type unions with `oneshot_llm_request` (API → worker) and `oneshot_llm_response` (worker → API)
- Modify: `agent-worker/src/ws-client.ts` — add `sendOneshotResponse(requestId, summary, error?)`
- Modify: `agent-worker/src/orchestrator.ts` — handle new message type `oneshot_llm_request`

- [x] Implement `runOneshotLLM(params)` in `oneshot-llm.ts` that takes `{ requestId, system, user, maxTokens, temperature, llmSettings, proxyConfig, signal }`, applies proxy via the shared helper, builds a pi-ai `Context` with one system message + one user message, resolves the model with `resolveModel(...)`, and calls `complete(model, context, { temperature, maxTokens, apiKey, timeoutMs: 30_000, signal })`. Return the assistant text from `result.content`.
- [x] In `orchestrator.handleMessage`, route `oneshot_llm_request` to a new `handleOneshotLLM(msg)` that validates `request_id`, extracts `llmSettings` (reuse `extractLLMSettings`), runs `runOneshotLLM` asynchronously, and sends `oneshot_llm_response` via `wsClient.sendOneshotResponse(...)` on success or with `error` on failure
- [x] Throw a clear error if `request_id`, `user`, or `llmSettings` is missing — surfaced as the `error` field on the response
- [x] Add unit tests for `runOneshotLLM` mocking pi-ai's `complete` (mock the module via vitest's `vi.mock("@mariozechner/pi-ai", ...)`): happy path returns assistant text, propagates `temperature`/`maxTokens` to `complete`, ctx-cancel propagates via AbortSignal, missing fields surface as Error
- [x] Add unit test for the orchestrator routing: a `oneshot_llm_request` message produces exactly one `oneshot_llm_response` send with the matching `request_id`
- [x] Run `make test-agent` — must pass before task 2

### Task 2: API-side WebSocket message types and request/response correlation

**Files:**
- Modify: `internal/handlers/agent_ws.go`
- Modify: `internal/handlers/agent_ws_test.go`

- [x] Add new constants `AgentMessageTypeOneshotLLMRequest = "oneshot_llm_request"` and `AgentMessageTypeOneshotLLMResponse = "oneshot_llm_response"`
- [x] Extend `AgentMessage` with `RequestID string` (omitempty), `System string` (omitempty), `User string` (omitempty), `MaxTokens int`, `Temperature float64`, and `Summary string` (omitempty) — JSON tags use `request_id`, `system`, `user`, `max_tokens`, `temperature`, `summary`
- [x] Add a `pendingOneshot map[string]chan *AgentMessage` (with mutex) on `AgentWSHandler` for request → response correlation
- [x] Add a method `OneShotLLM(ctx context.Context, llm *LLMSettingsForWorker, system, user string, maxTokens int, temperature float64) (string, error)` that:
  - generates a `request_id` (uuid v4)
  - registers a buffered channel in `pendingOneshot[request_id]`
  - constructs `AgentMessage{Type: oneshot_llm_request, RequestID: ..., System: ..., User: ..., MaxTokens: ..., Temperature: ..., Provider/APIKey/Model/ThinkingLevel/BaseURL: from llm}`
  - includes `ProxyConfig` via the same `GetOrCreateProxySettings` pattern already in `StartIncident`/`ContinueIncident`
  - calls `SendToWorker(...)`, then waits for `<-channel` or `<-ctx.Done()`, with a final fallback timeout (e.g. 60s if ctx has no deadline)
  - on receipt: returns `msg.Summary`, or `error(msg.Error)` if non-empty
  - cleans up `pendingOneshot[request_id]` in a deferred block (always)
- [x] In `handleMessage`, route `AgentMessageTypeOneshotLLMResponse` to a new `handleOneshotLLMResponse(msg)` that does a non-blocking send on `pendingOneshot[msg.RequestID]` (drops on no listener, logs at debug)
- [x] Return `ErrWorkerNotConnected` from `OneShotLLM` when no worker is connected; callers fall back to deterministic behavior
- [x] Add table-driven tests in `agent_ws_test.go` using a fake worker WebSocket: (a) request → response round-trip returns expected summary, (b) error response propagates as Go error, (c) ctx cancellation unblocks the call and cleans up the pending entry, (d) `ErrWorkerNotConnected` when no worker, (e) multiple concurrent requests are routed to the right channels
- [x] Run `make test` — must pass before task 3

### Task 3: Migrate TitleGenerator to use the worker oneshot path

**Files:**
- Modify: `internal/services/title_generator.go`
- Modify: `internal/services/title_generator_test.go`

- [x] Define a new interface in `internal/services` (avoids import cycle since handlers already imports services):
  ```go
  type OneShotLLMCaller interface {
      OneShotLLM(ctx context.Context, llm *LLMSettingsForWorker, system, user string, maxTokens int, temperature float64) (string, error)
  }
  ```
  (The `LLMSettingsForWorker` type is currently in `handlers`; lift it to `services` or duplicate a minimal shape — choose during implementation based on call graph.)
- [x] Inject via constructor: `NewTitleGenerator(c OneShotLLMCaller) *TitleGenerator`
- [x] Inside `GenerateTitle`, load `database.GetLLMSettings()`, build the worker LLM settings struct (reuse existing helper), and call `caller.OneShotLLM(ctx, settings, systemPrompt, userPrompt, 50, 0.3)` (preserve current 50 max_tokens / 0.3 temperature)
- [x] Remove `openAIRequest`/`openAIResponse` types and the `httpClient`; remove the early-return `if settings.Provider != LLMProviderOpenAI { fallback }` check — title generation now works with whichever provider is active
- [x] Preserve every existing graceful-fallback path: missing API key → fallback title; OneShotLLM error (incl. `ErrWorkerNotConnected`) → log warn + fallback title; empty / over-length result → trim/fallback
- [x] Update the constructor wiring at every callsite (search `NewTitleGenerator(`); for the API server it will pass the AgentWSHandler instance, which satisfies the interface via its `OneShotLLM` method
- [x] Update existing TitleGenerator tests: replace HTTP mocks with a fake `OneShotLLMCaller`; add a test confirming a non-OpenAI provider (Anthropic) round-trips correctly (no longer falls back)
- [x] Verify all callers of TitleGenerator still compile (`internal/services/...`, `internal/handlers/...`, `cmd/akmatori/...`)
- [x] Run `make test` — must pass before task 4

### Task 4: Migrate AlertExtractor to use the worker oneshot path

**Files:**
- Modify: `internal/alerts/extraction/extractor.go`
- Modify: `internal/alerts/extraction/extractor_test.go` and any sibling test files (`extractor_api_test.go`, `extractor_edge_test.go` — confirm exact filenames during implementation)
- Modify: `internal/handlers/slack.go` — `NewSlackHandler` accepts an `OneShotLLMCaller` and passes it to `extraction.NewAlertExtractor(caller)`
- Modify: `cmd/akmatori/main.go` — wire the AgentWSHandler instance into `NewSlackHandler` so it satisfies the OneShotLLMCaller interface

- [x] Define a local `OneShotLLMCaller` interface in `internal/alerts/extraction` with the same shape used by Task 3 (to avoid creating a new dependency from extraction → services). Verify cycle direction during implementation; duplicate the small interface in `extraction` if needed.
- [x] Replace `Extract`/`ExtractWithPrompt` HTTP-call body with a call to `caller.OneShotLLM(ctx, settings, "" /*system*/, userPrompt, 500, 0.1)`. Keep the same prompt template (`defaultExtractionPrompt`), the same `truncateMessage(messageText, 3000)` cap, and the same JSON-parsing logic (strip ```json fences, `json.Unmarshal` into `ExtractedAlert`)
- [x] Remove `openAIRequest`/`openAIResponse` types, the `httpClient` field, and the `if settings.Provider != "" && settings.Provider != database.LLMProviderOpenAI { fallback }` early-return so extraction now works with whichever provider is active
- [x] Update `NewAlertExtractor()` to take an `OneShotLLMCaller` argument; rework `NewAlertExtractorWithDeps` so the test seam swaps the caller (and the `LLMSettingsGetter`) instead of an `HTTPDoer`
- [x] Preserve every existing graceful-fallback path via `createFallbackAlert(...)`: missing API key, missing settings, OneShotLLM error (incl. `ErrWorkerNotConnected`), empty response, malformed JSON
- [x] Update existing extractor tests: replace `mockClient` (HTTPDoer) seams with a fake `OneShotLLMCaller`; preserve all behavioural assertions (severity normalization, status normalization, fallback paths, JSON-fence stripping, target host/service mapping). Add a test confirming a non-OpenAI provider (Anthropic) round-trips correctly (no longer falls back)
- [x] Run `make test` — must pass before task 5

### Task 5: Condensed live progress via chat.appendStream

**Files:**
- Create: `internal/handlers/slack_progress.go`
- Create: `internal/handlers/slack_progress_test.go`
- Modify: `internal/handlers/alert_slack.go` — add `appendSlackThreadStream` wrapper around `slackClient.AppendStream`
- Modify: `internal/handlers/alert.go` — keep existing constants; add `slackAppendInterval` (e.g. 2s) for native-stream throttle since `appendStream` is cheaper than `chat.update`

- [x] Implement a `SlackProgressStreamer` struct wrapping `(client, channel, threadTS, isStreaming, lastAppendAt)` that exposes `AppendStatus(text string)` — when `isStreaming` is true, calls `AppendStream` with markdown text; when false, falls back to the existing `chat.update` path (preserves behaviour for older Slack workspaces)
- [x] Implement a small parser that converts a delta of agent OnOutput text into condensed status lines: detect "🛠️ Running: ", "✅ Ran: ", and thinking markers ("🤔 …"); emit at most one short status line per tool transition; throttle to `slackAppendInterval` to avoid Slack rate limits
- [x] Add table-driven tests for the parser (markers, partial deltas, dedupe of consecutive identical statuses, stripping non-marker lines)
- [x] Add tests for `SlackProgressStreamer` using a mocked Slack client (verify `AppendStream` called when streaming, `UpdateMessage` when not, throttle window respected)
- [x] Run `make test` — must pass before task 6

### Task 6: Single-message summarized final output via the worker harness

**Files:**
- Create: `internal/services/slack_summarizer.go`
- Create: `internal/services/slack_summarizer_test.go`
- Create: `internal/output/slack_budget.go` — small helper exposing `WithinSlackBudget(text string, maxBytes int) bool` and a deterministic-fallback shortener
- Create: `internal/output/slack_budget_test.go`
- Modify: `internal/handlers/alert.go` — set `slackMaxTextBytes = 8000`; add `slackSummaryMargin` (e.g. 200 bytes) to leave room for the existing footer

- [x] Implement `SlackSummarizer` that depends on the same `OneShotLLMCaller` interface defined in Task 3 (constructor injection — no new SDK or HTTP client)
- [x] In `SummarizeForSlack(ctx, formattedText, maxBytes) (string, error)`:
  - if `formattedText` already fits the budget: return unchanged (no LLM call)
  - else load `database.GetLLMSettings()` and call `caller.OneShotLLM(ctx, settings, systemPrompt, userPrompt, 600, 0.2)`
  - prompt explicitly preserves status verdict, key actions, and the most actionable recommendation; instructs the model to keep the result under the byte budget and end with a short note pointing to the Akmatori incident UI
  - if the LLM returns over-budget output, run the deterministic fallback below
- [x] Provide a deterministic fallback in `slack_budget.go` for when the worker is unavailable / errors / returns over-budget output: collapse the body to the structured `[FINAL_RESULT]` summary line + the first action + the first recommendation, then byte-truncate with the existing footer
- [x] Wire summarizer in `cmd/akmatori/main.go` (mirror how TitleGenerator is constructed and injected into handlers); reuse the same `AgentWSHandler` instance as the OneShotLLMCaller
- [x] Add table-driven tests for `SummarizeForSlack`: under-budget passthrough (no LLM call asserted via fake caller), over-budget with fake caller returning a fitting summary, over-budget with fake caller returning over-budget output (fallback used), caller error (fallback used), `ErrWorkerNotConnected` (fallback used)
- [x] Add tests for the deterministic fallback shortener: structured-block input, free-form input, very small budgets
- [x] Run `make test` — must pass before task 7

### Task 7: Wire condensed streaming + summarized single message into Slack flows

**Files:**
- Modify: `internal/handlers/slack_processor.go`
- Modify: `internal/handlers/alert_processor.go`
- Modify: `internal/handlers/alert_slack.go` — replace `truncateWithFooter` callsites in the final-message path with the summarizer flow (still preserving `buildSlackFooter` as the trailing footer)
- Modify or create: `internal/handlers/slack_flow_integration_test.go`, `internal/handlers/alert_slack_integration_test.go`

- [x] Replace the static "Thinking..." flow in `slack_processor.go` `processMessage` and `alert_processor.go` `runSlackChannelInvestigation`: construct a `SlackProgressStreamer`, call its `AppendStatus` from inside `OnOutput` (passes the new delta only, not the full accumulated log)
- [x] Replace the final `UpdateMessage(progressMsgTS, finalResponse)` truncation path with: format via `FormatForSlack`, then call `SummarizeForSlack(ctx, formatted, slackMaxTextBytes - footerLen)`, then append the existing `buildSlackFooter` and `UpdateMessage` once. Single message, no thread replies for final output.
- [x] In the alert-channel flow, ensure the same single-message logic runs whether we're updating a streaming message or posting a new (non-streamed) reply
- [x] Drop the now-unused "last 15 lines reasoning" prefix in `buildSlackResponse` (it was a workaround for the missing live stream — superseded by the live append; the summarizer is responsible for any final reasoning context that fits)
- [x] Add integration-style tests that mock both the Slack client and the OneShotLLMCaller and assert: (a) `AppendStream` is called with non-empty status text during a simulated investigation, (b) a long final response triggers the summarizer and produces exactly one `UpdateMessage` call with body+footer ≤ `slackMaxTextBytes`, (c) a short final response bypasses the summarizer and is posted as-is, (d) when the caller returns `ErrWorkerNotConnected` the deterministic fallback is used and a single message is still produced
- [x] Run `make test` — must pass before task 8

### Task 8: Verify acceptance criteria

- [ ] Run full Go test suite: `make test-all`
- [ ] Run agent-worker tests: `make test-agent`
- [ ] Run linter: `golangci-lint run`
- [ ] Run `make verify`
- [ ] Confirm existing tests in `alert_test.go` / `alert_handler_test.go` that assert message-byte caps still pass; update only those whose expected text changed because the "last 15 lines reasoning" prefix was removed and the cap moved from 3000 to 8000
- [ ] Verify `go test` coverage for `internal/services` (summarizer + title), `internal/output`, `internal/alerts/extraction`, and `internal/handlers` Slack/oneshot paths is ≥ 80% (`go test -coverprofile=coverage.out ./internal/services/... ./internal/output/... ./internal/alerts/extraction/... ./internal/handlers/...`)

### Task 9: Update documentation

- [ ] Update CLAUDE.md: brief note in the Slack Integration section about the live progress stream (`AppendStream`) and the single-message summarizer; brief note in the Agent Worker Architecture / Services section that TitleGenerator, AlertExtractor, and the new SlackSummarizer all call pi-ai's `complete()` through the agent worker via the `oneshot_llm_request` WebSocket message; update the example `extractor := extraction.NewAlertExtractor()` snippet in the Alert Extraction section to include the new `OneShotLLMCaller` arg — keep it under ~10 lines per the "small pattern notes" rule
- [ ] Move this plan to `docs/plans/completed/`

## Post-Completion (manual verification, not automated)

- Manually exercise a long-investigation incident from a Slack alert channel and confirm: live "Running …" updates appear during the investigation, the final result lands as a single thread message under the 8000-byte cap, and the message ends with the existing footer linking to the Akmatori incident UI
- Switch the active LLM to a non-OpenAI provider (e.g. Anthropic) and confirm incident titles, AlertExtractor extractions, AND Slack summaries are all still generated correctly
- Stop the agent worker and confirm title generation, AlertExtractor, and Slack summaries fall back cleanly without crashing the API
