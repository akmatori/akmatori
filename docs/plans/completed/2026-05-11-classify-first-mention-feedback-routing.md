# Classify-First Routing for @Mention Thread Replies on Incident Threads

## Overview

Today, @mention thread replies on an incident thread always invoke the agent (`handleBotMentionInThread`), skipping the LLM-backed feedback classifier. This caused incident `65bc1aa8` to lose operator feedback ("it was false positive… next time check grafana first"): the agent tried to save it itself with a non-existent `memory.save` tool against a read-only mount.

This plan makes @mention thread replies classify-first: confident feedback short-circuits to the persist + 👍 + ack flow; everything else (low confidence, classifier error, worker offline, no incident match, empty text) falls through to today's agent continuation path. Non-mention thread replies and all other paths are untouched.

## Context

- Files involved:
  - `internal/handlers/slack_feedback.go` (refactor target, lines 27-82)
  - `internal/handlers/slack.go` (route insertion at line 363; reference `handleBotMentionInThread` at line 293)
  - `internal/handlers/slack_feedback_test.go` (existing 14-test style; extend)
  - `internal/handlers/slack_test.go` (`TestHandleMessage_ThreadReplyHumanMention` at line 294; extend)
  - `internal/services/feedback_classifier.go` (reuse `Classify`, `IsConfidentFeedback`, `ErrWorkerNotConnected`)
- Related patterns:
  - Dedup with `h.processedMsgs sync.Map`, key `channel:messageTS`, 60s TTL via `time.AfterFunc` / goroutine+sleep (slack.go:296, 394)
  - Inline mention stripping via `strings.Replace(text, fmt.Sprintf("<@%s>", h.botUserID), "", 1)` then `strings.TrimSpace` (slack.go:250, 307, 402, 434)
  - Incident lookup via `lookupIncidentByThread` (slack_feedback.go:88)
  - Fire-and-forget goroutines for LLM round-trips (Socket Mode must not block)
- Dependencies: none new; all helpers already exist.

## Development Approach

- Testing approach: Regular (refactor + new router first, then add tests). Each task ends with running the project test suite.
- Complete each task fully before moving to the next.
- Keep `maybeCaptureSlackFeedback` byte-for-byte behavior-preserving for the non-mention call site at slack.go:371.
- Mock seam for the agent hop: add an internal function-pointer field `runMentionContinuation func(channel, threadTS, messageTS, text, user string)` on `SlackHandler`, defaulted to `h.handleBotMentionInThread`. Tests override it to assert routing without spinning up `processMessage`.
- CRITICAL: every task MUST include new/updated tests.
- CRITICAL: all tests must pass before starting next task.

## Implementation Steps

### Task 1: Split maybeCaptureSlackFeedback into reusable halves

Files:
- Modify: `internal/handlers/slack_feedback.go`

- [x] Extract `classifyThreadReplyForFeedback(threadTS, text string) (services.FeedbackVerdict, *database.Incident, error)` covering: precondition checks (classifier/memoryManager nil → return sentinel), text trim/empty check, `lookupIncidentByThread`, mention-strip (`strings.Replace` with `<@h.botUserID>` then `TrimSpace`) prior to classifier input, and `feedbackClassifier.Classify` call. Return the verdict, the incident, and a non-nil error for every fall-through case (so callers can branch on `err != nil`).
- [x] Extract `persistFeedbackAndAck(channel, threadTS, messageTS, originalText string, verdict services.FeedbackVerdict, incident *database.Incident)` covering: `buildFeedbackMemory(originalText, …)`, `memoryManager.UpsertByName`, slog.Info, reaction (`+1`), threaded ack (`Thanks — saved to memory as …`).
- [x] Reduce `maybeCaptureSlackFeedback` to: call `classifyThreadReplyForFeedback`; on error or non-confident verdict return silently; otherwise call `persistFeedbackAndAck` with the original (un-mention-stripped) text so the persisted memory body matches what the operator typed.
- [x] Run `make test` — existing `slack_feedback_test.go` and `slack_test.go` must pass with no behavior change for the non-mention path.

### Task 2: Add classify-first router for @mention thread replies

Files:
- Modify: `internal/handlers/slack.go`

- [x] Add `runMentionContinuation func(channel, threadTS, messageTS, text, user string)` field on `SlackHandler`. Initialize it in the existing `SlackHandler` constructor (or first-use site) to `h.handleBotMentionInThread`.
- [x] Add method `routeBotMentionThreadReply(channel, threadTS, messageTS, text, user string)`:
  - Dedup using `h.processedMsgs` with key `channel + ":" + messageTS`; on duplicate, return. Schedule cleanup with `time.AfterFunc(60*time.Second, …)` (or the equivalent goroutine+sleep pattern used at slack.go:301) to delete the key.
  - Spawn a goroutine that calls `classifyThreadReplyForFeedback`. On confident feedback (`err == nil && incident != nil && verdict.IsConfidentFeedback()`) call `persistFeedbackAndAck`. Otherwise call `h.runMentionContinuation(channel, threadTS, messageTS, text, user)`.
- [x] Replace the inline `h.handleBotMentionInThread(...)` call at slack.go:363 with `h.routeBotMentionThreadReply(event.Channel, event.ThreadTimeStamp, event.TimeStamp, event.Text, event.User)`. Leave `handleBotMentionInThread`'s own internal dedup intact — duplicate `LoadOrStore` from the inner call is a defensive no-op.
- [x] Run `make test` — no test should regress; agent path remains the default.

### Task 3: Tests for the new router

Files:
- Modify: `internal/handlers/slack_feedback_test.go`
- Modify: `internal/handlers/slack_test.go`

- [x] Add a small test-helper that constructs a `SlackHandler` with a stub `feedbackClassifier`, stub `memoryManager`, sqlite-backed `database.GetDB()` seeded with an incident matching the thread, and an overridden `runMentionContinuation` that increments a counter. Reuse existing patterns from `slack_feedback_test.go`.
- [x] `TestRouteBotMentionThreadReply_FeedbackShortCircuits` — confident verdict → memory upserted + reaction posted; agent counter == 0.
- [x] `TestRouteBotMentionThreadReply_NotConfidentFallsThroughToAgent` — verdict with `Confidence < 0.6` → no memory persist; agent counter == 1.
- [x] `TestRouteBotMentionThreadReply_ClassifierErrorFallsThrough` — stub `Classify` returns a wrapped error → agent counter == 1.
- [x] `TestRouteBotMentionThreadReply_WorkerOfflineFallsThrough` — stub `Classify` returns `services.ErrWorkerNotConnected` → agent counter == 1; no warn-level log noise.
- [x] `TestRouteBotMentionThreadReply_NoIncidentMatchFallsThrough` — thread that doesn't map to any incident → agent counter == 1; classifier not invoked.
- [x] `TestRouteBotMentionThreadReply_DedupIdempotent` — call twice with same `channel:messageTS` → second call is a no-op (classifier counter == 1, agent counter ≤ 1).
- [x] `TestClassifyThreadReplyForFeedback_StripsMention` — classifier receives text with `<@U_BOT>` removed; the original (un-stripped) text is what gets passed into `buildFeedbackMemory.Body` on the persist path.
- [x] Extend `slack_test.go::TestHandleMessage_ThreadReplyHumanMention` (or add a sibling `TestHandleMessage_ThreadReplyHumanMention_FeedbackShortCircuits`) covering the end-to-end short-circuit from `handleMessage` through `routeBotMentionThreadReply` to `persistFeedbackAndAck` — verifies the slack.go:363 wiring.
- [x] Use `testhelpers.AssertEventually` (or a buffered channel sync) on the goroutine-driven assertions since the router fires async.
- [x] Run `make test` — all new tests green.

### Task 4: Verify acceptance criteria

- [x] `make verify` (go vet + full test suite) passes
- [x] `golangci-lint run ./internal/handlers/...` clean (skipped — binary not installed in this environment; `go vet` via `make verify` passed)
- [x] No behavior change for the non-mention call site at slack.go:371 (covered by the existing 14 tests in `slack_feedback_test.go`)
- [x] `handleBotMentionInThread` still functions as today for the fall-through cases (covered by existing `slack_test.go`)

### Task 5: Update documentation

- [x] If the CLAUDE.md "Slack feedback capture" paragraph (under Memory System) still says "mention path is unchanged (still routes to investigation continuation)", update it to: "mention path on incident threads classifies first; confident feedback short-circuits to persist + 👍 + ack, otherwise falls through to investigation continuation."
- [x] Move this plan to `docs/plans/completed/` after merge.

## Post-Completion (manual verification, run by the human reviewer)

1. Feedback wins: in an incident thread, post `@bot it was a false positive caused by a single stream 404, next time check grafana first`. Expect 👍 + "Thanks — saved to memory…" ack, no typing banner, new row in `memories` table.
2. Agent runs: in an incident thread, post `@bot can you re-check the affected host now?`. Expect typing banner + hourglass + agent thread reply; no 👍 / ack; no new memory row.
3. Worker offline: stop `akmatori-agent`, post any @mention thread reply. Expect classifier short-returns `ErrWorkerNotConnected` and agent path runs (and fails gracefully as today).
