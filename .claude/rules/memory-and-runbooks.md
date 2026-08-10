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
- memory deletions: `memory-writer` accepts `Action: delete <slug>` and writes a tombstone
  (`name:` + `deleted: true` frontmatter only); `IngestFromDisk` deletes the matching row and the
  post-batch sync purges the tombstone and prior `<id>-<slug>.md` snapshot; unknown-slug tombstones
  are a no-op but still trigger sync

Key files: `internal/services/memory_service.go`, `internal/services/runbook_service.go`,
`akmatori_data/agents/` (`runbook-searcher`, `memory-searcher`, `memory-writer` definitions)
