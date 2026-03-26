---
# Fix Review Findings from LLM Proxy Rename (3dbaf30)

## Overview
Address critical and warning-level issues found during code review of the `openai` → `llm` proxy setting rename commit.

## Context
- Commit reviewed: 3dbaf30 ("feat: rename proxy setting from openai to llm for provider-agnostic naming")
- Files involved: `docs/openapi.yaml`, `agent-worker/package.json`, `internal/database/models.go`
- Related patterns: existing migration patterns in `internal/database/`

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Fix OpenAPI spec - rename openai to llm

**Files:**
- Modify: `docs/openapi.yaml`

- [x] Search `docs/openapi.yaml` for remaining `openai` references in the proxy settings schema
- [x] Rename the service key from `openai` to `llm` to match the new DB/API field names
- [x] Verify the schema matches the Go struct and frontend types
- [x] Run `make verify` - must pass before task 2

### Task 2: Add undici as explicit dependency

**Files:**
- Modify: `agent-worker/package.json`

- [x] Check the version of `undici` currently resolved in `node_modules`
- [x] Add `undici` to `dependencies` in `agent-worker/package.json` with a compatible version range
- [x] Run `npm install` in `agent-worker/` to update the lockfile
- [x] Verify existing agent-worker tests still pass: `make test-agent`
- [x] Run `make verify` - must pass before task 3

### Task 3: Make database migration transactional

**Files:**
- Modify: `internal/database/models.go` (or wherever the migration lives)

- [x] Find the migration that copies `openai_enabled` → `llm_enabled` and drops the old column
- [x] Wrap the UPDATE + DROP COLUMN in a database transaction
- [x] Change the data-copy warning log to return an error on failure (fail loudly)
- [x] Write a test verifying the migration handles partial failure gracefully
- [x] Run `make test` - must pass before task 4

### Task 4: Verify acceptance criteria

- [x] Manual test: save LLM proxy settings via the API using the new field name, confirm they persist correctly
- [x] Run full test suite: `make test-all`
- [x] Run linter: `golangci-lint run`
- [x] Verify test coverage meets 80%+

### Task 5: Update documentation

- [x] Update CLAUDE.md if internal patterns changed
- [x] Move this plan to `docs/plans/completed/`
