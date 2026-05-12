# Sharpen Runbook Retry Guidance to Quote Verbatim Source Phrases

## Overview

When QMD's initial runbook search returns no usable hit, the agent's retry #1 currently rephrases the same structured summary instead of quoting the distinctive sender/source/channel phrase from the original alert text. This causes alerts like "New notification from stream-health monitor" to miss the matching runbook on the second try. The fix is a prompt change in two synchronized locations plus a regression test.

## Context

- Files involved:
  - `internal/executor/executor.go` — `PrependGuidance(...)` user-turn reminder (lines 140-170)
  - `internal/database/db.go` — `DefaultIncidentManagerPrompt` system prompt (lines 443-446)
  - `internal/executor/executor_test.go` — existing PrependGuidance tests pin retry-related structure
- Related patterns:
  - The two prompts are explicitly kept in sync per the comment at `executor.go:138`
  - Existing tests `TestPrependGuidance_ScopesRunbookSearchToRunbooksCollection` and `TestPrependGuidance_SingleQMDQueryWithOrderedTriplet` pin the triplet shape and collections filter — they must still pass
- Dependencies: none (prompt-only change; no SDK or schema change)

## Development Approach

- Testing approach: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Both prompts must change together — the `// keep them in sync` comment makes divergence a known-bad state
- CRITICAL: every task MUST include new/updated tests
- CRITICAL: all tests must pass before starting next task

## Implementation Steps

### Task 1: Update PrependGuidance retry block in executor.go

Files:
- Modify: `internal/executor/executor.go`

- [ ] Replace the existing "If results are empty, retry with a different angle..." block (lines 156-160) with the new wording from /tmp/plan.md: introduce the `If results are empty OR the top hit's title is not obviously related` condition, promote the verbatim-source-phrase angle to a conditional MUST gated on the presence of an `Original alert text:` block, and demote the question-rephrase / target-service angles to retry #2
- [ ] Keep the rest of `PrependGuidance` intact: current time line, single `qmd.query` triplet invocation, `"collections": ["runbooks"]` filter, `qmd.get` follow-up, and the trailing `Please help with the following incident or request:` framing
- [ ] Add test `TestPrependGuidance_RequiresSourcePhraseOnRetry` in `internal/executor/executor_test.go` asserting the output contains the substrings `"Original alert text:"`, `"retry #1 MUST"`, and `"verbatim"`
- [ ] Run `go test ./internal/executor/...` — new test passes and the two existing `TestPrependGuidance_*` tests still pass

### Task 2: Mirror the same change in DefaultIncidentManagerPrompt

Files:
- Modify: `internal/database/db.go`

- [ ] Replace the retry-angle block (lines 443-446) with the same wording substitution made in Task 1, preserving the surrounding indentation style of `DefaultIncidentManagerPrompt`
- [ ] Verify the `// keep them in sync` invariant: the retry guidance text in `db.go` matches `executor.go` (modulo formatting)
- [ ] Add or extend a test in `internal/database/` that asserts `DefaultIncidentManagerPrompt` contains the same `"Original alert text:"`, `"retry #1 MUST"`, and `"verbatim"` substrings (mirroring the executor test)
- [ ] Run `go test ./internal/database/...` and `go test ./internal/executor/...` — all green

### Task 3: Verify acceptance criteria

- [ ] Run `make verify` (go vet + full test suite) — must pass
- [ ] Run `golangci-lint run` on the touched packages — clean
- [ ] Manually diff `PrependGuidance` output against the new `DefaultIncidentManagerPrompt` retry block to confirm wording stays aligned

### Task 4: Update documentation and archive plan

- [ ] No CLAUDE.md update needed — the runbook-search shape and the `keep them in sync` invariant are already documented
- [ ] Move this plan to `docs/plans/completed/`

## Post-Completion Validation (manual, not automatable)

- Rebuild API container: `docker-compose build akmatori-api && docker-compose up -d akmatori-api` (agent-worker not affected — prompt is built API-side)
- Replay or trigger a fresh slack-channel investigation with the slack-channel "stream-health monitor alerts" payload (or the incident e3880769 context) and confirm retry #1 quotes the upstream channel sender phrase verbatim and the agent retrieves runbook #10
- Confirm non-alert flows (Slack mentions / DMs with no `Original alert text:` block) behave identically — the new clause is gated on that marker
