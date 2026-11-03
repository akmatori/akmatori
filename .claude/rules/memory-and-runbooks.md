---
paths:
  - "**/internal/services/memory_*"
  - "**/internal/services/runbook_service.go"
  - "**/internal/services/knowledge_upgrade*"
  - "**/mcp-gateway/internal/tools/knowledge/**"
  - "**/akmatori_data/memory/**"
  - "**/akmatori_data/runbooks/**"
---

# Runbooks and cross-incident memory

Runbooks and memories live in Postgres. Agents reach them only through the built-in gateway
tools `runbooks.search` / `runbooks.get` and `memory.search` / `memory.list` / `memory.get` /
`memory.write` / `memory.delete` (`mcp-gateway/internal/tools/knowledge`, no LLM in the loop; see
the `mcp-gateway-tools` rule). The pi-subagents (runbook-searcher, memory-searcher, memory-writer)
are gone. `akmatori_data/runbooks/` and `akmatori_data/memory/` are still written by the API as a
read-only mirror (`SyncRunbookFiles`, `SyncMemoryFiles`; the agent mount is `:ro`) until the
mirror is removed.

- search is lexical BM25 (`search.go`): name + description (memories) or title (runbooks) weigh 3,
  the body 1; tokens keep hostnames, IPs and paths whole and also emit their parts; a long query
  (alert payload) keeps the 40 most selective terms; each call loads at most 5,000 newest rows
  (`truncated_corpus: true` above that). Pinned by a fixture corpus with 12 named queries
- prompts: incident-manager steps 2-3 and `PrependGuidance` call `runbooks.search` then
  `memory.search` with the Original alert text (2 narrower retries), then `*.get`. The record
  rules live in the `memory.write` tool description and `memoryWriteInstructionTemplate`: search
  first and update the memory that covers the subject; correct or delete a memory the new facts
  prove wrong; absolute dates; feedback bodies are rule + `**Why:**` + `**How to apply:**`; never
  save what runbooks, alerts or context files already say; never the `file-` prefix
- `memory.write` refuses a missing, null or non-string scope/name/type/description/body (the
  dispatcher does not enforce the schema; a missing body read as "" would erase it)
- `memory.write` validation = the API's `validate` (shared fixture
  `tests/fixtures/memory_validation_cases.json`, run by both modules) plus agent-only rules: a
  description over 500 bytes is CUT (UTF-8 safe, `description_truncated: true`), scope must be
  `global` or an existing non-system skill, no `file-` prefix
- protected rows: `created_by = operator` or `source_file_id IS NOT NULL`. The protection is in
  the UPDATE/DELETE `WHERE`, not a prior read (`refused_protected`). An operator upsert
  (`UpsertByName` with `created_by=operator`: proposal approval, Slack feedback, from-text) takes
  ownership of an agent row. Legacy rows with empty `created_by` are agent-writable
- writes need `X-Run-ID` of a running, non-proposal run whose `scope_uuid` equals `X-Incident-ID`;
  provenance `incident_uuid` = the run's incident (empty for request runs); caps 20 writes and 10
  deletes per run; the gate `MemoryGenerationEnabled` is read per write (fail open)
- the upsert updates only when type/description/body/evidence/claim/evidence_note differ (NULL-safe),
  so `updated_at` (manifest order) stays stable
- the manifest is rendered from the DB (`RenderMemoryManifest`): capped at 150 entries per scope by
  `UpdatedAt` descending, feedback first (at most 50 of the 150, unused fact slots go back to
  feedback), line and byte caps. SKILL.md embeds its scope's manifest, so `FinishRun` regenerates
  every SKILL.md (and the disk mirror) when the memories fingerprint (count + newest
  `updated_at`) changed, and keeps the old fingerprint after a failed write so the next run
  retries; AGENTS.md is rendered per run
- upgrade: `IngestDiskMemoriesOnce` imports the old disk files once (system setting
  `memory_disk_ingested_at`) before the first sync. It first copies the files to
  `akmatori_data/memory-import-backup/`, ignores the generation gate, skips invalid files (kept in
  the backup) and, on a DB failure, returns an error with NO sync and no flag (retry on next
  start); the startup sync and the `FinishRun` mirror sync wait for the flag; `DisableReservedNamespaceCollisions` disables a
  connector named `runbooks` / `memory`; `WarnRetiredKnowledgeReferences` logs skills, runbooks,
  memories and cron prompts that still name the subagents; seeds replace never-edited system cron
  prompts (`legacySystemCronPrompts`, append the old text whenever a default changes)
- disk mirror internals (until removed): files `<scope>/<slug>.md` + `MEMORY.md`, legacy
  `<id>-<slug>.md` read by ingest, tombstones (`deleted: true`) handled by ingest only
- every root-skill AGENTS.md ends with a "Reference files" list of the uploaded context files
  (`renderContextFilesSection`: agent path `/akmatori/context/<file>`, size, operator description;
  capped at 100 files / 8 KiB). The worker mounts that dir read-only and the agent reads
  reference files with the read tool
- memory bodies link with `[[slug]]` (same scope, else global), `[[scope/slug]]` and
  `[[file.ext]]` (context file). The raw `[[...]]` is what DB and disk store — never rewrite it
  into a path. `MemoryService.ResolveRefs` resolves refs against the DB for views
  (`GET /api/memories/{id}` → `references`) and `memory.get` returns them for the agent (memories
  by scope + name + id, files by path); anything that is not a
  slug, `scope/slug` or a valid context filename is `invalid` and never becomes a path
- memory API for the UI: `PUT /api/memories/{id}` clears the body (or `evidence_note`) only when
  the request has that key (`UpdateMemory(id, m, MemoryUpdateSet{Body, EvidenceNote})`; other empty
  fields mean "unchanged"); `GET /api/memories?evidence=` filters by grade;
  `GET /api/memories?incident_uuid=` lists memories that originated from an incident;
  `GET /api/memories/export` is a zip of `<scope>/<name>.md` in the on-disk format;
  `POST /api/memories/from-text` drafts an operator memory with `MemoryDrafter` (one-shot LLM,
  deterministic fallback: first line = description, text = body, type feedback) and creates it
  with `CreateWithUniqueName` (`-2`, `-3`… — never overwrites another memory)
- evidence grades (`memory_evidence.go`, gateway `knowledge/evidence.go`, shared fixture
  `tests/fixtures/memory_evidence_cases.json`): `evidence` = `confirmed` (operator only) |
  `observed` (seen directly, not a cause) | `unverified` (unconfirmed cause or legacy row);
  `claim` = `causal` | `factual` | "" (not assessed); `evidence_note` ≤ 500 bytes. Ownership
  (`created_by`) is NOT verification. Agent rules (`ApplyAgentMemoryEvidence`: gateway
  `memory.write`, proposal approval, disk ingest, later the post-incident extractor): claim
  defaults to factual, causal wording (`causalPattern`, same regex in both modules) makes it
  causal, causal is always unverified, `confirmed` is rewritten to unverified with a note.
  Operator writes (REST, from-text, Slack feedback) may set any grade; empty = observed
  (unverified for causal). File summaries are observed/factual. The upgrade backfill
  (`migrateMemoryEvidence`, once, when the column is added): operator rows and file summaries
  observed, every other row unverified with note `legacy: not assessed`; `updated_at` stays.
  The manifest has an `evidence` column; the recall text says unverified = a lead to check.
  Proposal snapshots carry the grade; a snapshot without it (older) does not compare it
- `GeneralSettings.MemoryGenerationEnabled` (nil = on) gates AGENT memory only. Off:
  `renderMemoryRecallSection` swaps "Record durable findings" for a do-not-write note and the
  gateway refuses `memory.write` / `memory.delete`. The gate is checked at each write, so writes
  made before an operator turns it off stay (the old ingest discarded the whole run's writes).
  Operator writes (UI, API, from-text, Slack feedback, proposals, uploads) still work. A change
  regenerates every SKILL.md; AGENTS.md is rendered per run. The gate fails open when the setting
  cannot be read
- memory types are `fact` (knowledge about hosts, services, tools, incidents, file summaries) and
  `feedback` (operator instructions about how the agent must work). The legacy `host`,
  `incident_pattern`, `tool_quirk` are accepted everywhere and stored as fact
  (`NormalizeMemoryType` in `validate`; `migrateLegacyMemoryTypes` rewrites old rows at startup);
  an unknown type is an error (ingest of old disk files still stores it as fact)
- every uploaded context file gets a summary memory (`ContextFileSummarizer`, one job at a
  time in the background): text files are read directly (NUL byte = not text), PDFs go
  through `pdftotext` (poppler-utils in `Dockerfile.api`, 100 pages / 30 s / 1 MiB); the
  one-shot prompt gets at most 48 KiB, marked as data. Result: a global fact named
  `file-<filename slug>` (`-2`… when an unrelated memory has the name), `created_by: agent`,
  body starts with `Source: [[file]]`, linked by `memories.source_file_id`. That column is
  server managed: the upserts, `UpdateMemory` and the gateway never touch it, the API
  request structs do not carry it; it makes the memory protected from agent writes. A rerun updates the linked memory in place (keeps an
  operator rename). No LLM = status `failed`, no memory (the file is still in AGENTS.md);
  under 200 bytes of text = `skipped`. The job re-checks the file row and sha256 before it
  writes, and deletes its memory when the file went away during the save. Deleting the file
  deletes its memories (`Forget`). Status lives on `context_files.summary_status`/`summary_error`;
  `POST /api/context/{id}/summarize` runs it again (also the backfill for old uploads);
  startup turns `pending` rows into `failed` (`ResetInterrupted`). Not
  gated by `MemoryGenerationEnabled`: an upload is an operator action
- `POST /api/context` caps the body with `http.MaxBytesReader` (10 MiB + 1 MiB form room, 413
  above it); `ParseMultipartForm`'s argument is only the in-memory threshold

Key files: `mcp-gateway/internal/tools/knowledge/` (search, tools, validation),
`internal/services/memory_service.go`, `internal/services/memory_refs.go`,
`internal/services/memory_drafter.go`, `internal/services/context_summarizer.go`,
`internal/services/memory_source_files.go`, `internal/services/runbook_service.go`,
`internal/services/knowledge_upgrade.go`
