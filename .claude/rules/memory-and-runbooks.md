---
paths:
  - "**/internal/services/memory_*"
  - "**/internal/services/runbook_service.go"
  - "**/internal/services/subagent_files*"
  - "**/akmatori_data/memory/**"
  - "**/akmatori_data/runbooks/**"
  - "**/akmatori_data/agents/**"
---

# Runbooks and cross-incident memory

Runbooks live in Postgres and sync to `akmatori_data/runbooks/` (read-only). Cross-incident
memory lives under `akmatori_data/memory/` (read-write). Agents reach both via pi-mono subagents
(`runbook-searcher`, `memory-searcher`, `memory-writer`).

- keep DB state and on-disk runbook files in sync (the runbook service writes both directions)
- the incident-manager prompt invokes `subagent({agent: "runbook-searcher", task: ...})` for SOP
  lookup — do not introduce direct grep loops in the main agent
- memory recall goes through `memory-searcher`; `memory-writer` persists durable findings near
  end-of-investigation
- memory-writer is invoked with `{agent: "memory-writer", task}` only — put scope and incident UUID
  in the first two task lines (`Scope: <slug>\nIncident UUID: <uuid>`)
- on incident completion the API runs `MemoryService.IngestFromDisk` to materialize new memory files
  into Postgres (idempotent by scope + `name:` slug); operator-authored rows carry
  `created_by: operator` in their frontmatter and ingest preserves that
- memory syncing is scope-aware and manifest-driven; upserts idempotent by `name:` slug + scope
- manifest capped at `manifestMaxEntriesPerScope` (150) entries per scope by `UpdatedAt` descending
- the upsert's conflict update fires ONLY when type/description/body/incident_uuid differ: ingest
  re-reads every unchanged `<slug>.md` file, and an unconditional update restamped the
  whole table after every incident, which made the manifest cap pick entries at random
- memory-writer writing rules (`akmatori_data/agents/memory-writer.md`, pinned by
  `TestSubagentDefinitionFiles`): read the scope and global `MEMORY.md` and update the memory that
  already covers a subject instead of adding a new name; correct or tombstone a memory the new
  facts prove wrong; absolute dates only; feedback bodies are rule + `**Why:**` + `**How to
  apply:**` (no invented reason); never use the `file-` prefix (API file summaries); do not save
  what runbooks, alerts or context files already say. `MemoryDrafter` gets today's date in its
  system prompt for the same reasons
- memory deletions: `memory-writer` accepts `Action: delete <slug>` and writes a tombstone
  (`name:` + `deleted: true` frontmatter only); `IngestFromDisk` deletes the matching row and the
  post-batch sync purges the tombstone; unknown-slug tombstones are a no-op but still trigger sync
- memory files are `<scope>/<slug>.md` — the same path the API syncs and the agent writes. Sync
  fchmods every file to 0666 so the agent (UID 1001) can overwrite API-written files (UID 1000).
  Ingest still accepts legacy `<id>-<slug>.md` files and prefers `<slug>.md` when both exist
- every root-skill AGENTS.md ends with a "Reference files" list of the uploaded context files
  (`renderContextFilesSection`: agent path `/akmatori/context/<file>`, size, operator description;
  capped at 100 files / 8 KiB). The worker mounts that dir read-only; `memory-searcher` stays
  scoped to `/akmatori/memory/`, so the main agent reads reference files itself
- memory bodies link with `[[slug]]` (same scope, else global), `[[scope/slug]]` and
  `[[file.ext]]` (context file). The raw `[[...]]` is what DB and disk store — never rewrite it
  into a path, ingest would save the path back. `MemoryService.ResolveRefs` resolves refs
  against the DB for views (`GET /api/memories/{id}` → `references`); anything that is not a
  slug, `scope/slug` or a valid context filename is `invalid` and never becomes a path
- memory API for the UI: `PUT /api/memories/{id}` clears the body only when the request has a
  `body` key (`UpdateMemory(id, m, setBody)`; other empty fields mean "unchanged");
  `GET /api/memories?incident_uuid=` lists memories that originated from an incident;
  `GET /api/memories/export` is a zip of `<scope>/<name>.md` in the on-disk format;
  `POST /api/memories/from-text` drafts an operator memory with `MemoryDrafter` (one-shot LLM,
  deterministic fallback: first line = description, text = body, type feedback) and creates it
  with `CreateWithUniqueName` (`-2`, `-3`… — never overwrites another memory)
- `GeneralSettings.MemoryGenerationEnabled` (nil = on) gates AGENT memory only. Off:
  `renderMemoryRecallSection` swaps "Record durable findings" for a do-not-write note (it
  overrides the root prompts, which still name memory-writer), and `IngestFromDisk` only runs
  `SyncMemoryFiles`, which discards agent creates, edits and tombstones (the dir is rw for the
  agent, so the prompt alone is not enough). Operator writes (UI, API, from-text, Slack feedback)
  still work. A change regenerates every SKILL.md; AGENTS.md is rendered per run. The gate fails
  open when the setting cannot be read
- memory types are `fact` (knowledge about hosts, services, tools, incidents, file summaries) and
  `feedback` (operator instructions about how the agent must work). The legacy `host`,
  `incident_pattern`, `tool_quirk` are accepted everywhere and stored as fact
  (`NormalizeMemoryType` in `validate`; `migrateLegacyMemoryTypes` rewrites old rows at startup);
  ingest stores an unknown type as fact instead of dropping the file. The manifest lists
  feedback first, newest first, at most `manifestMaxFeedbackEntries` (50) of the 150 slots;
  unused fact slots go back to feedback
- every uploaded context file gets a summary memory (`ContextFileSummarizer`, one job at a
  time in the background): text files are read directly (NUL byte = not text), PDFs go
  through `pdftotext` (poppler-utils in `Dockerfile.api`, 100 pages / 30 s / 1 MiB); the
  one-shot prompt gets at most 48 KiB, marked as data. Result: a global fact named
  `file-<filename slug>` (`-2`… when an unrelated memory has the name), `created_by: agent`,
  body starts with `Source: [[file]]`, linked by `memories.source_file_id`. That column is
  server managed: ingest's conflict update and `UpdateMemory` never touch it, the API
  request structs do not carry it. A rerun updates the linked memory in place (keeps an
  operator rename). No LLM = status `failed`, no memory (the file is still in AGENTS.md);
  under 200 bytes of text = `skipped`. The job re-checks the file row and sha256 before it
  writes, and deletes its memory when the file went away during the save. Deleting the file
  deletes its memories (`Forget`). Status lives on `context_files.summary_status`/`summary_error`;
  `POST /api/context/{id}/summarize` runs it again (also the backfill for old uploads);
  startup turns `pending` rows into `failed` (`ResetInterrupted`). Not
  gated by `MemoryGenerationEnabled`: an upload is an operator action
- `POST /api/context` caps the body with `http.MaxBytesReader` (10 MiB + 1 MiB form room, 413
  above it); `ParseMultipartForm`'s argument is only the in-memory threshold

Key files: `internal/services/memory_service.go`, `internal/services/memory_refs.go`,
`internal/services/memory_drafter.go`, `internal/services/context_summarizer.go`,
`internal/services/memory_source_files.go`, `internal/services/runbook_service.go`,
`akmatori_data/agents/` (`runbook-searcher`, `memory-searcher`, `memory-writer` definitions)
