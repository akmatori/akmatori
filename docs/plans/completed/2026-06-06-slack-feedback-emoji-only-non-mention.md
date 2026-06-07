# Emoji-only acknowledgment for non-mention Slack feedback

## Overview

Today, a non-mention reply in an incident thread that the LLM classifier
accepts as operator feedback gets BOTH a 👍 reaction AND a threaded text reply
("Thanks — saved to memory as `...`. Future incidents will recall it."). The
operator only wants the emoji on the non-mention path — Akmatori should not
post text into a thread unless it was explicitly @mentioned.

Goal:
- Non-mention feedback → mark the message with the emoji reaction only (no text reply).
- Explicit @mention feedback → keep today's behavior (emoji + short text ack), per the iteration-1 answer ("same thing as today").
- Non-mention, non-feedback replies → already silent; unchanged.
- @mention non-feedback → full agent continuation; unchanged.

Net effect: Akmatori posts text into a thread only when @mentioned.

## Context

Files involved:
- `internal/handlers/slack_feedback.go` — `maybeCaptureSlackFeedback` (non-mention path, lines 27-41) and the shared `persistFeedbackAndAck` helper (lines 89-109). The 👍 reaction is at line 101; the text ack `PostMessage` is at lines 104-107.
- `internal/handlers/slack.go` — `routeBotMentionThreadReply` (line 343) calls the same `persistFeedbackAndAck` at line 356; `SlackHandler` struct (lines 20-56); `NewSlackHandler` (lines 61-78); the `runMentionContinuation` injectable seam (field at line 55, defaulted at line 76).
- `internal/handlers/slack_feedback_test.go` — existing tests build `SlackHandler` with `client == nil`, so the ack branch is currently untested.

Related patterns:
- The `runMentionContinuation` func field is the established seam for testing handler branches without a live `*slack.Client`. The new `feedbackAcker` interface mirrors it: a default adapter wired only when `client != nil`, overridable by tests.
- Best-effort ack: reaction/post failures must never roll back the persisted memory (preserve graceful degradation per CLAUDE.md).

Dependencies: none new.

## Development Approach

- **Testing approach**: Regular (code first, then tests).
- Preserve graceful degradation: a nil acker still persists memory, never panics, and only skips the Slack ack.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests.**
- **CRITICAL: all tests must pass before starting the next task.**

## Implementation Steps

### Task 1: Introduce an injectable feedbackAcker seam

**Files:**
- Modify: `internal/handlers/slack.go` (struct field + `NewSlackHandler` wiring)
- Modify: `internal/handlers/slack_feedback.go` (interface + default adapter)

- [x] Define `feedbackAcker` interface in `slack_feedback.go`:
      `AddReaction(name string, item slack.ItemRef) error` and
      `PostThreadText(channel, threadTS, text string) error`.
- [x] Add a default adapter backed by `*slack.Client`: `AddReaction` forwards to `client.AddReaction`; `PostThreadText` wraps `client.PostMessage(channel, slack.MsgOptionText(text, false), slack.MsgOptionTS(threadTS))` and discards the returned channel/ts.
- [x] Add a `feedbackAcker` field to the `SlackHandler` struct near `runMentionContinuation`.
- [x] In `NewSlackHandler`, set the field to the default adapter only when `client != nil` (leave nil otherwise, mirroring graceful degradation).
- [x] Tests: add a `fakeFeedbackAcker` recording reaction/post calls; assert `NewSlackHandler(nil, ...)` leaves the field nil and `NewSlackHandler(non-nil, ...)` wires the adapter.
- [x] Run `go test ./internal/handlers/...` — must pass before Task 2.

### Task 2: Split the ack so non-mention feedback is emoji-only

**Files:**
- Modify: `internal/handlers/slack_feedback.go`
- Modify: `internal/handlers/slack.go` (mention call site at line 356)

- [x] Split `persistFeedbackAndAck` into three helpers:
      - `persistFeedback(...)` — `UpsertByName` + log, returns the saved `*database.Memory` (or nil on failure).
      - `reactFeedback(channel, messageTS)` — emoji only, best-effort, guards on `feedbackAcker != nil`.
      - `postFeedbackTextAck(channel, threadTS, memName)` — text post, best-effort, guards on `feedbackAcker != nil`.
- [x] Non-mention path (`maybeCaptureSlackFeedback`): call `persistFeedback` + `reactFeedback` only — NO text post.
- [x] Mention path (`routeBotMentionThreadReply` at slack.go:356): call `persistFeedback` + `reactFeedback` + `postFeedbackTextAck` (unchanged net behavior).
- [x] Remove the now-dead combined `persistFeedbackAndAck` helper so there is one clear path per branch.
- [x] Route both ack helpers through the `feedbackAcker` seam (not `h.client` directly) so tests can assert call counts.
- [x] Tests with `fakeFeedbackAcker`:
      - non-mention confident feedback → 1 reaction, 0 text posts, 1 memory upsert.
      - mention confident feedback → 1 reaction, 1 text post, 1 memory upsert.
      - nil acker → memory upserted, 0 reactions, 0 posts, no panic.
      - non-feedback verdict → 0 of everything.
- [x] Run `go test ./internal/handlers/...` — must pass before Task 3.

### Task 3: Verify acceptance criteria

- [x] Trace both branches in the final code to confirm: non-mention = emoji only; mention feedback = emoji + text; mention non-feedback = agent continuation; non-mention non-feedback = silent.
- [x] Run full suite: `make test`.
- [x] Re-run `go test -coverprofile=coverage.out ./...` and confirm `internal/handlers` coverage holds (target 80%+ on the changed file). — slack_feedback.go at 84.9%.
- [x] Run linter: `golangci-lint run` (or `make verify`). — golangci-lint not installed in env; `go vet ./internal/handlers/...` clean (matches the `make verify` gate).

### Task 4: Update documentation

- [x] Update the "Slack investigation UX" rule in `CLAUDE.md`: non-mention confident feedback is emoji-only (no thread reply); @mention feedback keeps emoji + short text ack; Akmatori only posts text in a thread when @mentioned.
- [x] Verify `wc -c CLAUDE.md` stays under 30000 bytes. — 25085 bytes.
- [x] Move this plan to `docs/plans/completed/`.
