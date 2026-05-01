# Claude Code Instructions for Akmatori

## Project Overview

Akmatori is an AI-powered AIOps agent for ingesting alerts, running investigations, and helping operators respond through the API, web UI, and Slack.

## Architecture

- Main stack: API server, PostgreSQL, frontend, MCP gateway, and worker/runtime services via Docker Compose
- Backend: Go
- Frontend: React + TypeScript + Vite
- Database: PostgreSQL with GORM
- Main areas:
  - `cmd/akmatori/`: API entrypoint
  - `internal/handlers/`: HTTP and websocket handlers
  - `internal/services/`: business logic
  - `internal/database/`: models and persistence
  - `internal/alerts/`: adapters and extraction
  - `internal/slack/`: Slack manager and channel handling
  - `internal/output/`: structured output parsing and Slack formatting
  - `internal/setup/`: first-run setup and credential resolution
  - `mcp-gateway/`: separate Go module for MCP tooling
  - `web/`: frontend
  - `tests/fixtures/`: shared test data

## Non-Negotiable Workflow

After any code change, run tests for the area you touched. Before committing, run broader verification.

### Useful commands

```bash
make test                # main Go tests
make test-all            # main tests + mcp-gateway
make test-adapters       # adapter tests only
make test-mcp            # mcp-gateway tool tests only
make test-coverage       # coverage.out + coverage.html
make verify              # go vet + tests in both modules
```

### Package-level checks

```bash
go test ./internal/handlers/...
go test ./internal/services/...
go test ./internal/database/...
go test ./internal/middleware/...
go test ./internal/utils/...
cd mcp-gateway && go test ./...
```

## Code Style

- Match existing Go conventions and naming.
- Prefer small, focused changes.
- Reuse helpers in `internal/testhelpers/` instead of inventing ad hoc fixtures.
- For HTTP handlers, use `httptest` and assert status plus response body.
- Prefer table-driven tests for parsing, validation, and mapping logic.
- Check returned errors unless there is a documented reason not to.
- Keep docs concise and operational; avoid long tutorial sections in this file.

## Testing Patterns

### Handler tests

- Build requests with `httptest.NewRequest`
- Record with `httptest.NewRecorder`
- Assert status code, JSON body, and error shape
- Cover invalid JSON, validation failures, and partial-update behavior
- For settings endpoints, assert masked secrets in responses rather than raw stored values

### Service tests

- Prefer deterministic unit tests over broad integration setup when possible
- Cover success path, edge cases, and dependency failures
- Use table-driven cases for normalization and transformation logic

### Adapter and parser tests

- Load realistic payloads from `tests/fixtures/` when available
- Add focused fixture files rather than giant inline JSON blobs
- Verify both parsed fields and fallback behavior
- Cover both native references and already-transformed asset links when testing context parsing

### Database tests

- Keep tests isolated and explicit about setup
- Prefer `testhelpers.SetupSQLiteTestDB(...)` for in-memory GORM setup and automatic `database.DB` cleanup
- Assert persisted fields, merge/update behavior, and query filters

## High-Value Areas

If you need a place to improve quality, prioritize these directories first:

1. `internal/handlers/`
2. `internal/services/`
3. `internal/alerts/`
4. `internal/slack/`
5. `internal/output/`
6. `internal/database/`

## Important Project Patterns

### Setup flow

`internal/setup/` resolves credentials with this priority:

1. environment variable
2. database value
3. generated value or setup-required mode

This affects JWT secret and admin password initialization.

### API responses

Use helpers from `internal/api/` for consistent JSON responses and validation errors.

### Slack behavior

Slack integration supports channel-based alert ingestion, thread follow-ups, and hot-reloadable settings. When changing Slack code, preserve:

- thread context behavior
- settings reload behavior
- secret masking in API responses
- partial update semantics for saved credentials

### Settings API conventions

For provider and Slack settings handlers in `internal/handlers/api.go`:

- GET responses should mask secrets and tokens
- PUT handlers use pointer fields so omitted values are left unchanged
- validate URLs and enum-like fields before persisting
- keep response shape stable for the frontend settings screens

### Context file references

`internal/services/context_service.go` is the source of truth for uploaded context assets:

- enforce the filename regex and allowed extension list there
- keep the 10 MB upload ceiling unless product requirements change
- preserve both `[[file.ext]]` references and transformed `assets/file.ext` links when parsing content

### Output parsing

`internal/output/` extracts structured blocks from agent output. Changes here should preserve clean output and structured result parsing.

## Current Notes Worth Preserving

- Swagger/OpenAPI is served from `/api/docs` and `/api/openapi.yaml`
- OpenAPI source lives in `docs/openapi.yaml`
- `make verify` runs vet and tests for both the main module and `mcp-gateway`
- `tests/fixtures/` is the preferred home for reusable payload samples
- Recent test coverage work includes Slack settings API flows, proxy/aggregation settings handlers, context reference parsing helpers, and alert webhook integration behavior

## Coverage and Quality Work

When improving tests:

- target uncovered branches, not just lines
- add assertions with clear failure messages
- prefer one focused behavior per test
- avoid brittle timing assumptions
- keep mocks minimal and honest

Useful commands:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
go vet ./...
staticcheck ./...
golangci-lint run
```

## Docker / Runtime Checks

If a change affects running services, rebuild only what changed instead of everything.

```bash
docker-compose build akmatori-api && docker-compose up -d akmatori-api
docker-compose build mcp-gateway && docker-compose up -d mcp-gateway
docker-compose build frontend && docker-compose up -d frontend
```

Use full rebuilds only when necessary.

## Before Committing

1. Run the most relevant package tests
2. Run `go test ./...`
3. If `mcp-gateway/` changed, test it too
4. For doc changes to this file, verify size stays under 30000 bytes:
   ```bash
   wc -c CLAUDE.md
   ```
5. Keep commit scope narrow and message clear

## Do Not Bloat This File

This file is a compact operator guide, not a full handbook. Prefer linking intent to code locations over embedding long examples. If adding new guidance:

- keep it short
- remove outdated text nearby
- avoid duplicate sections
- prefer bullets over large tables
