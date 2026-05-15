# Replace QMD with pi-mono subagents for runbook and memory search (read + write)

## Overview

Delete the QMD container and the gateway-side runbook/memory proxies. Mount the runbook directory read-only and the memory directory read-write into the agent container, then use pi-mono subagents (via the pi-subagents extension) to scope recon/edit work to those folders. The incident-manager prompt invokes a `subagent` tool with agent name `runbook-searcher`, `memory-searcher`, or `memory-writer`. Each subagent runs in its own isolated `pi` subprocess. The agent itself records new long-lived memory via `memory-writer`; on incident completion the API re-ingests the memory directory into Postgres, replacing the standalone `MemoryExtractor` flow.

## Context

- Files involved:
  - `agent-worker/package.json`, `agent-worker/Dockerfile`, `agent-worker/src/agent-runner.ts`
  - `docker-compose.yml`, `docker-compose.dev.yml`, `.env.example`, `.github/workflows/release.yml`
  - `qmd/` (entire directory)
  - `internal/database/db.go`, `internal/database/prompt_test.go`
  - `internal/executor/executor.go`, `internal/executor/executor_test.go`
  - `internal/services/runbook_service.go`, `internal/services/memory_service.go`, `internal/services/runbook_service_qmd_test.go`, `internal/services/memory_service_sync_test.go`, `internal/services/memory_service_test.go`, `internal/services/skill_prompt_service.go`, `internal/services/memory_prompt_test.go`, `internal/services/skill_service.go`, `internal/services/memory_extractor.go`, `internal/services/memory_extractor_test.go`
  - `cmd/akmatori/main.go`
  - `mcp-gateway/cmd/gateway/main.go`, `mcp-gateway/internal/tools/registry.go`, `mcp-gateway/internal/tools/memory/` (entire directory)
  - `CLAUDE.md`, `README.md`
- Related patterns:
  - `DefaultResourceLoader` in `agent-runner.ts:282` already configures `additionalSkillPaths` and `getAgentDir()`; flip `noExtensions: false` and load extensions/agents from there
  - pi-mono bundles a working subagent extension template at `agent-worker/node_modules/@earendil-works/pi-coding-agent/examples/extensions/subagent/` (used as reference for the published `pi-subagents` package)
  - existing volume-mount pattern in `docker-compose.yml:180-186, 221` mounts `runbooks` and `memory` into the agent container; memory currently `:ro` and must change to `:rw`
  - `MemoryService.SyncMemoryFiles()` already handles the DB-to-disk direction (`internal/services/memory_service.go:369`); the new ingestion is the reverse direction and mirrors its scope/file shape
  - `UpdateIncidentComplete` is where post-incident hooks fire (currently invokes `MemoryExtractor.Extract`); the new memory ingest replaces that call site
- Dependencies:
  - `pi-subagents` npm package (https://github.com/nicobailon/pi-subagents) installed in `agent-worker`
  - `pi` CLI binary from `@earendil-works/pi-coding-agent` (already in `node_modules/.bin`) reachable on PATH so subagent subprocesses can spawn
  - `ripgrep` and `fzf` available inside the agent container image (add to Dockerfile if not present)

## Development Approach

- **Testing approach**: Regular (code first, then tests). Tests in this area are prompt/wiring/sync tests, not TDD-friendly.
- Complete each task fully before moving to the next
- Order matters: build subagent infrastructure first, define agents, switch prompts, add file-to-DB ingestion, tear out QMD/extractor wiring, then delete the QMD service
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Wire pi-subagents into the agent worker and mount agent definitions

**Files:**
- Modify: `agent-worker/package.json` (add `pi-subagents` dep)
- Modify: `agent-worker/Dockerfile` (ensure `pi` bin on PATH; install pi-subagents in production stage; install `ripgrep` and `fzf` apt packages)
- Modify: `agent-worker/src/agent-runner.ts` (flip `noExtensions: false`)
- Modify: `docker-compose.yml` (add `./akmatori_data/agents:/home/agent/.pi/agent/agents:ro` and `./akmatori_data/extensions:/home/agent/.pi/agent/extensions:ro` mounts on `akmatori-agent`; change `./akmatori_data/memory:/akmatori/memory:ro` to `:rw` on the agent container only; update `init-dirs` to `mkdir -p` the new `agents` and `extensions` dirs)
- Create: pi-subagents extension placed at `/home/agent/.pi/agent/extensions/pi-subagents/index.ts` inside the image (via the Dockerfile install step)

- [x] add `pi-subagents` to `agent-worker/package.json` dependencies and run `npm install` to refresh the lock file
- [x] update `agent-worker/Dockerfile` runtime stage so the pi-subagents extension is materialised at `/home/agent/.pi/agent/extensions/pi-subagents/index.ts`, `pi` from `node_modules/.bin` is on `PATH`, and `apt-get install -y ripgrep fzf` (materialised at `/opt/pi-extensions/pi-subagents` instead — the host-mounted extensions dir would otherwise shadow `~/.pi/agent/extensions`; loaded via `additionalExtensionPaths`)
- [x] in `agent-runner.ts`, change `noExtensions: true` to `noExtensions: false` and verify the loader picks up the extension
- [x] add the agents and extensions mounts to `docker-compose.yml`, switch the agent's memory mount to `:rw`, and update `init-dirs` to create `agents/` and `extensions/`
- [x] write a vitest assertion in `agent-worker` that verifies the resource loader, given a tmpdir with one extension file, loads it (no longer skips by default)
- [x] run `make test-agent` — must pass before task 2

### Task 2: Define runbook-searcher, memory-searcher, and memory-writer subagents

**Files:**
- Create: `akmatori_data/agents/runbook-searcher.md` (frontmatter: name, description, tools, model; body instructs scoped grep/find/read inside `/akmatori/runbooks/`)
- Create: `akmatori_data/agents/memory-searcher.md` (same shape, scoped to `/akmatori/memory/`)
- Create: `akmatori_data/agents/memory-writer.md` (frontmatter with read + write + edit tools; body explains memory file format, scope dirs, idempotent upserts by `name:` slug, refuses to escape `/akmatori/memory/`)
- Modify: `docker-compose.yml` (verify runbooks RO and memory RW mounts are in place for the agent — no change expected beyond task 1)

- [ ] write `runbook-searcher.md` with `tools: read, grep, find, ls, bash, rg, fzf` and a system prompt that: cd's into `/akmatori/runbooks/`, runs rg/find against the alert summary, returns top-3 candidate file paths + 5-line snippets, and refuses to leave that directory
- [ ] write `memory-searcher.md` mirroring the same shape, scoped to `/akmatori/memory/`, returning top hits with file paths and brief excerpts
- [ ] write `memory-writer.md` with `tools: read, edit, write, grep, ls, rg`, a system prompt that: receives a `task` describing what to remember + the originating incident UUID, searches `/akmatori/memory/<scope>/` for an existing file with that semantic name first (idempotency), upserts using the existing markdown+frontmatter shape produced by `MemoryService.formatMemoryFile`, refuses paths outside `/akmatori/memory/`, and emits a short summary of files written/changed
- [ ] add a Go-side unit test that opens all three files and asserts the frontmatter parses and the body references the correct mount path
- [ ] run `make test` — must pass before task 3

### Task 3: Switch incident-manager and skill prompts to call subagents (search + write)

**Files:**
- Modify: `internal/database/db.go` (`DefaultIncidentManagerPrompt`) — replace the qmd.query/qmd.get block with `subagent({agent: "runbook-searcher", task: "<alert original message>"})` guidance
- Modify: `internal/executor/executor.go` — same replacement
- Modify: `internal/services/skill_prompt_service.go` — replace `memory.search`/`memory.get` recall instruction with `subagent({agent: "memory-searcher", task: "…"})`, AND add an end-of-investigation instruction to call `subagent({agent: "memory-writer", task: "<full reasoning log and instruction to save only important infomation that will help to speed up troubleshooting next time>", scope: "…", incident: "<uuid>"})` when the agent learns durable cross-incident facts (hosts, recurring patterns, tool quirks)
- Modify: `internal/database/prompt_test.go`, `internal/executor/executor_test.go`, `internal/services/memory_prompt_test.go` — drop `qmd.*` / `memory.search` / `memory.get` references; add positive assertions for `runbook-searcher`, `memory-searcher`, and `memory-writer`

- [ ] rewrite the "MANDATORY - Search runbooks FIRST" section to invoke `subagent` with the runbook-searcher agent; keep the fallback-to-`/akmatori/runbooks/` line as a hard fail path
- [ ] rewrite the memory recall instruction in `skill_prompt_service.go` to invoke `subagent` with the memory-searcher agent
- [ ] add a new "Record durable findings" section in `skill_prompt_service.go` that instructs the agent to invoke `memory-writer` with the full reasoning log plus a save-only-important-info directive, naming the scope and incident UUID
- [ ] update all three prompt tests to assert the new subagent names and absence of `qmd.*` / `memory.*` tool names
- [ ] run `make test` — must pass before task 4

### Task 4: Add file→DB memory ingestion and replace MemoryExtractor with it

**Files:**
- Modify: `internal/services/memory_service.go` — add `IngestFromDisk(ctx) error` that walks `<memoryDir>/<scope>/*.md` (skipping `MEMORY.md`), parses frontmatter (reusing the existing format helpers), and `Upsert`s by stable identity (`name:` slug + scope + incident UUID); records `created_by=agent`; idempotent on re-run
- Modify: `internal/services/skill_service.go` — drop `memoryExtractor` field and `SetMemoryExtractor`; replace its callsite inside `UpdateIncidentComplete` with `memoryService.IngestFromDisk(ctx)` (best-effort, logged-only failures)
- Delete: `internal/services/memory_extractor.go`, `internal/services/memory_extractor_test.go`
- Modify: `internal/services/memory_prompt_test.go` — remove any extractor-prompt assertions; keep only the recall/write prompt assertions
- Modify: `cmd/akmatori/main.go` — drop `NewMemoryExtractor` + `SetMemoryExtractor` wiring
- Create: `internal/services/memory_service_ingest_test.go` — table-driven test that drops fixture .md files into a tmp memory dir and asserts they round-trip into the DB via `IngestFromDisk`, including: new files create rows, modified files update rows (by name+scope), files with same identity stay idempotent, files outside the scope dirs are rejected

- [ ] implement `MemoryService.IngestFromDisk` with strict path validation (`filepath.Clean` + scope-dir prefix check)
- [ ] swap the post-incident extractor invocation for `IngestFromDisk`
- [ ] delete `memory_extractor.go`, `memory_extractor_test.go`, and the extractor wiring in `main.go` + `skill_service.go`
- [ ] add the ingest test
- [ ] update `memory_prompt_test.go` to drop extractor-prompt assertions
- [ ] run `make test` — must pass before task 5

### Task 5: Remove QMD-related backend code

**Files:**
- Modify: `internal/services/runbook_service.go` (delete `qmdURL`, `SetQMDURL`, `triggerQMDReindex`, the goroutine call site)
- Modify: `internal/services/memory_service.go` (delete `qmdURL`, `SetQMDURL`, `triggerQMDReindex`, the call site, and the QMD-shaped comments in `formatMemoryFile`)
- Delete: `internal/services/runbook_service_qmd_test.go`
- Modify: `internal/services/memory_service_sync_test.go`, `internal/services/runbook_service_test.go` — drop QMD-reindex expectations
- Modify: `cmd/akmatori/main.go` — drop the two `os.Getenv("QMD_URL")` blocks and the slog line

- [ ] strip QMD reindex wiring from runbook + memory services and update their tests so they no longer expect reindex side effects
- [ ] remove QMD env handling from `cmd/akmatori/main.go`
- [ ] run `make test` — must pass before task 6

### Task 6: Remove QMD from MCP Gateway

**Files:**
- Delete: `mcp-gateway/internal/tools/memory/` (entire directory, incl. `memory.go` and `memory_test.go`)
- Modify: `mcp-gateway/internal/tools/registry.go` — remove `"qmd"` and `"memory"` from `builtInToolNamespaces`, delete `RegisterMemoryTools` and its proxy-namespace bookkeeping, drop the import of the memory package
- Modify: `mcp-gateway/cmd/gateway/main.go` — delete the `RegisterMemoryTools` call and the `if qmdURL := os.Getenv("QMD_URL")` block; remove the `strings` import if it becomes unused
- Modify: any registry / authorizer tests that referenced qmd or memory namespaces

- [ ] delete the memory tool package and all references
- [ ] delete the QMD system-proxy registration and memory-tool registration in gateway main
- [ ] update affected gateway tests to drop qmd/memory expectations
- [ ] run `make test-mcp` — must pass before task 7

### Task 7: Remove the QMD service from infrastructure and docs

**Files:**
- Modify: `docker-compose.yml` — remove the `qmd` service, the `qmd_cache` volume, the `qmd: service_healthy` depends_on under `mcp-gateway`, the `QMD_URL` env vars on api/mcp-gateway, and `qmd` from every NO_PROXY default
- Modify: `docker-compose.dev.yml` — remove the `qmd:` build block
- Modify: `.env.example` — drop `qmd` from the NO_PROXY comments
- Modify: `.github/workflows/release.yml` — drop QMD image build/push
- Delete: `qmd/` (entire directory: `Dockerfile`, `entrypoint.sh`, `patch-server.js`, `precache-models.mjs`, `qmd-config.yml`)
- Modify: `CLAUDE.md` — remove QMD bullets, replace with a short subagent-search-and-write note; verify `wc -c CLAUDE.md` stays under 30000
- Modify: `README.md` — strip any QMD mentions and document the new search + write flow

- [ ] remove QMD from `docker-compose.yml` / `docker-compose.dev.yml` / `.env.example` / release workflow
- [ ] delete the `qmd/` source directory
- [ ] update CLAUDE.md and README.md to describe the subagent-based runbook search, memory search, and memory write flows; remove the QMD/runbook-sync/reindex bullets
- [ ] run `make verify` — must pass before task 8

### Task 8: Verify acceptance criteria

- [ ] run `make verify` (full backend + lint gate)
- [ ] run `make test-agent` (agent-worker tests)
- [ ] run `make test-mcp` (gateway tests)
- [ ] rebuild affected containers: `docker-compose -f docker-compose.yml -f docker-compose.dev.yml build akmatori-api mcp-gateway akmatori-agent && docker-compose -f docker-compose.yml -f docker-compose.dev.yml up -d`
- [ ] `grep -rn "qmd\|QMD" --include='*.go' --include='*.ts' --include='*.yml' --include='*.md' .` returns no functional references (only changelog/plan history)
