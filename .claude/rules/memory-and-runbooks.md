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
  deletes per run, charged only by a write that created or updated a row and a delete that removed
  one (`hasQuota` before, `takeQuota` after); the gate `MemoryGenerationEnabled` is read per write
  (fail open). `memory.list` `since` takes ISO-8601 or Unix seconds or milliseconds (`unixArg`)
- the upsert updates only when type/description/body/evidence/claim/evidence_note differ (NULL-safe),
  so `updated_at` (manifest order) stays stable
- the manifest is rendered from the DB (`RenderMemoryManifest`): capped at 150 entries per scope by
  `UpdatedAt` descending, feedback first (at most 50 of the 150, unused fact slots go back to
  feedback), line and byte caps. SKILL.md embeds its scope's manifest, so `FinishRun` regenerates
  every SKILL.md (and the disk mirror) when the memories fingerprint (count + newest
  `updated_at`) changed, and keeps the old fingerprint after a failed write so the next run
  retries (the regeneration does not depend on the mirror refresher being wired); AGENTS.md is
  rendered per run. SKILL.md, AGENTS.md, scripts and runbook files are written with
  `writeFileAtomic` (temp file + rename), never a plain `os.WriteFile`
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
  causal (not for `feedback`: its `**Why:**` line says "because" by format), causal is always
  unverified, `confirmed` is rewritten to unverified with a note.
  Operator writes (REST, from-text, Slack feedback) may set any grade; empty = observed
  (unverified for causal); `created_by=agent` + `confirmed` is a 400. File summaries are
  observed/factual. The upgrade backfill (`migrateMemoryEvidence`, once, in one transaction, when
  the column is added): operator rows and file summaries observed, every other row unverified
  with note `legacy: not assessed`; `updated_at` stays. The one-time disk import that follows
  leaves the evidence columns of an existing row alone when the file has no grade
  (`upsertByNameNoSyncOpts(keepEvidence)`), so it cannot undo the backfill.
  A rewrite is not a check (`MergeStoredMemoryEvidence`, gateway `mergeStoredEvidence`: proposal
  approval and `memory.write` on an existing row): grade omitted + same text = stored grade,
  claim and note stay; grade omitted + changed text = stored unverified stays unverified, stored
  confirmed falls to the agent default with the note `operator confirmation reset`; `observed`
  on a stored unverified row needs a non-empty `evidence_note` (else it stays unverified,
  `note_on_evidence` says so); note omitted = stored note stays, except the legacy note on
  changed text. `memory.write` reports the stored grade for `unchanged`/`refused_protected`.
  The manifest has an `evidence` column; the recall text says unverified = a lead to check.
  Proposal snapshots carry the grade; a snapshot without it (older) does not compare it
- post-incident memory pass (`incident_memory_extractor.go`, `incident_memory_extraction_input.go`,
  `incident_memory_extraction_prompt.go`, `memory_extraction_apply.go`; plan
  `docs/plans/2026-11-02-post-incident-memory-extractor.md`): alert investigations do NOT write
  memory. The gateway `requireRun` refuses `memory.write` / `memory.delete` for a run with
  `incident_uuid` (after the proposal refusal); the incident AGENTS.md (`scopeKindIncident`,
  `memoryIncidentRunInstruction`) tells the agent to put the timeline, the findings marked
  observed/hypothesis, `Operator asked to remember:` and `Memory contradicted:` in the final
  answer; request AGENTS.md keeps the write guidance; a SKILL.md (shared) carries the neutral
  `memorySharedWriteNote`. Queue on the incident row: every close path (`closeIncidentValues`:
  `CloseIncident`, monitor sweep, stale close) sets `memory_extraction = pending` in the UPDATE
  that closes; the merger sets the child `skipped` (the survivor's pass reads the family's runs).
  Worker `Run`: ticker 30 s + `Kick()` (wired into the close paths by `SetExtractionKicker`),
  claim = conditional UPDATE (`claimableWhere`: closed alert incident, pending with no future
  `retry_at` OR running past the 15-min lease, no run still running) that also counts the
  attempt; `memory_extraction_at` is the claim time (µs-truncated so it round-trips) and the
  lease; transient failures (no LLM, timeout, invalid JSON, DB error) re-queue with backoff
  2 min / 10 min / 1 h and `failed` after 3 attempts; startup `ResetInterrupted` turns running
  into pending. Outcomes `succeeded | no_output | skipped | failed` (+ `memory_extraction_error`,
  `_note` = `redacted N secrets`, `memory_timeline` for the UI). Input: facts (close reason,
  alerts with `resolved_by` = `source | operator | sweep`, runs' self-reports and times), the
  original alert text, `full_log` of every finished family run (32 KiB each, head 8 + tail 24,
  96 KiB total, newest kept), final answers (16 KiB), feedback memories, the global and skill
  manifests; the whole prompt goes through `RedactSecrets`. Output: strict JSON, at most 5
  memories; Go grades them (`buildExtractedMemories` → `ApplyAgentMemoryEvidence`, causal always
  unverified, cause/effect order check appended to the note, unknown scope → global, bad name
  slugified, secrets redacted). `MemoryService.ApplyExtraction` writes in one transaction: the
  outcome UPDATE carries the claim guard (RowsAffected 0 = `ErrExtractionStale`, nothing written),
  a name this incident wrote before (also under a `-N` suffix) is updated in place with the
  protection predicate in the SQL, a name owned by anyone else gets `-2`, `-3`…; a partial
  failure rolls everything back. `POST /api/incidents/{uuid}/extract-memory` (`Rerun`): closed +
  not pending/running → pending, attempts reset, 202; pending/running → 409; not closed or
  merged → 409; missing → 404. `POST /api/incidents/extract-memory-backfill` `{days}` (default
  30, max 365) → `Backfill(since)`: queues the closed alert incidents resolved since then whose
  `memory_extraction` is empty (closed before the pass existed), 202 `{queued, since}`. The pass
  also reads the heads of the spilled tool outputs (`<incidents dir>/<uuid>/tool_outputs/*`,
  `readSpilledToolOutputs` through `os.Root`, regular files only, newest first, 8 KiB per file,
  32 KiB and 8 files total; `SetIncidentsDir` wires the root)
- retention: a `closed` or `merged` incident expires by its close time (`COALESCE(resolved_at,
  completed_at, updated_at)`), like a completed one by `completed_at`; the delete transaction
  appends `source incident deleted` to the evidence note of its agent memories
  (`noteSourceIncidentDeleted`: not operator rows, not confirmed, never twice) and keeps the rows
  for Dreaming to review
- Slack remember command (`slack_remember.go`): a thread reply `remember: X`, `/remember X` or
  `!remember X` (with or without the bot mention) is stored as an operator feedback memory on the
  thread's work with the note "operator asked to remember it", with no classifier and no LLM;
  ack = 👍, plus the short text on the mention path. Checked before the classifier in both paths
- usage tracking (`memory_usage.go`, plan D8): `memory_uses` (`memory_id`, `run_id`, `incident_uuid`,
  `kind` = `search | get | cited`, `at`; `ON DELETE CASCADE`, plus an explicit delete in
  `DeleteMemory` and the gateway `memory.delete` for SQLite) and the denormalised
  `memories.use_count` / `last_used_at`. The gateway records a `search` row per hit (moves
  `last_used_at` only) and a `get` row per full read (`use_count + 1`) with the calling run's id
  and incident (`recordUses`, best effort); `FinishRun` parses the final answer's `Memory used:`
  block (`ParseMemoryUsed`: refs on the heading line and the lines after it until a blank line;
  `[[scope/name]]` exact, `[[name]]` global first) and records `cited` rows
  (`RecordCitedMemories`, wired by `SetMemoryCiter`). The manifest ranks by `manifestRank` = the
  later of `last_used_at` and `updated_at` (a new memory is visible at once, a used one stays in
  the 150 cap). `memory.list` / `memory.search` return `use_count` and `last_used_at`; `memory.list`
  takes `unused_since` (`last_used_at IS NULL OR < t`). REST `GET /api/memories?used_in_incident=`
  lists the memories an incident's runs used, newest use first. Retirement is Dreaming's job
  (the prompt: unverified agent memories unused for 90 days, not created in the last 90 days,
  never confirmed/operator); no code deletes for lack of use. UI: `usageLabel` on the list and
  detail, "memories the runs used" on the incident Memory section
- Dreaming (`dreamingCronPrompt`) carries the evidence rules: a merged memory takes the LOWEST
  grade of its cluster (an operator-confirmed member is kept as is and never lowered), never
  writes `confirmed`, keeps causal claims unverified, lists the source incidents in the note, and
  rewrites a contradicted agent memory only on a specific newer observation, and retires unused
  unverified agent memories (above). The previous texts are in `legacySystemCronPrompts` (append
  again on the next change)
- UI: `EvidenceBadge` (green confirmed, grey observed, amber unverified; tooltip = claim + note)
  on the memory list (with an evidence `SegmentedControl` filter) and detail; the edit form sets
  evidence, claim and the note (`evidence_note` always sent, so an emptied field clears it); the
  incident Summary tab of a closed incident shows `IncidentMemorySection` (extraction state from
  `memoryExtractionHelpers.ts`, "Run memory pass" button → `incidentsApi.extractMemory`, the
  timeline, the memories from this incident); the page polls every 5 s while the pass is
  pending/running
- secrets: `RedactSecrets` (`secret_redaction.go`, gateway mirror `knowledge/redact.go`, shared
  fixture `tests/fixtures/secret_redaction_cases.json`) replaces PEM private key blocks, URL
  passwords, `Bearer` tokens (8+ chars), `sk-…` keys, `AKIA…` ids and `key[:=]value` credentials
  (api_key/token/secret/password, value 8+ chars) with `[REDACTED_SECRET]`. Applied to the
  extractor input and output, to `memory.write` (response `redacted: N`) and to proposal approval
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
