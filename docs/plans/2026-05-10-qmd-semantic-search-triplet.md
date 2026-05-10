# Enable QMD Semantic Search + Switch to {lex, vec, hyde} Triplet Pattern

## Overview

QMD's lex mode is BM25 over SQLite FTS5 with positive terms AND-joined: any query term not present in the corpus collapses results to zero. The recent incident eea37a1d missed its runbook because the verbatim alert text contained noise terms no runbook author writes. This plan enables QMD's full hybrid retrieval stack (BM25 + vector + HyDE, fused via Reciprocal Rank Fusion) by shipping the embedding + reranker GGUFs in the QMD image, running `qmd embed` on startup, and rewriting the runbook-search section of `DefaultIncidentManagerPrompt` and `PrependGuidance` to use the openclaw triplet shape.

Out of scope: replacing QMD; touching `memory.search` (separate code path in `mcp-gateway/internal/tools/memory/`).

## Context

- Files involved:
  - `qmd/Dockerfile` — pre-cache GGUFs; drop the third sed patch
  - `qmd/entrypoint.sh` — replace "skipped" stanza with `qmd embed`
  - `qmd/precache-models.mjs` (new) — build-time model puller using `pullModels` from `/opt/qmd/dist/llm.js`
  - `internal/database/db.go` (lines 427-454) — rewrite runbook-search block in `DefaultIncidentManagerPrompt`
  - `internal/executor/executor.go` (lines 133-170) — rewrite `PrependGuidance` to mirror the triplet shape
  - `internal/database/prompt_test.go` — pin new triplet assertions
  - `internal/executor/executor_test.go` — drop verbatim/keywords ordering; assert triplet
  - `CLAUDE.md` (line 312) — update Agent Access paragraph
- Related patterns: openclaw's `buildV2Searches` in `/opt/openclaw/extensions/memory-core/src/memory/qmd-manager.ts:1912` is the reference shape
- Dependencies:
  - `pullModels` already exists at `/opt/qmd/src/llm.ts:315` (idempotent, ETag-checked)
  - `DEFAULT_EMBED_MODEL_URI` (~300 MB) + `DEFAULT_RERANK_MODEL_URI` (~640 MB) are exported from the same module
  - The `qmd_cache` Docker volume currently shadows `/root/.cache/qmd/` — must be cleared once on existing installs

## Development Approach

- Testing approach: Regular (code first, then tests). Prompt changes drive test updates in lockstep — update production strings and assertions together.
- The incident-manager prompt is a Go const, not stored in DB — no migration needed.
- Each task fully complete (build/test green) before moving on.
- CRITICAL: every code-modifying task includes new/updated tests.
- CRITICAL: all tests must pass before starting next task.

## Implementation Steps

### Task 1: QMD sidecar — bake embedding + reranker models into image

Files:
- Create: `qmd/precache-models.mjs`
- Modify: `qmd/Dockerfile`
- Modify: `qmd/entrypoint.sh`

- [ ] create `qmd/precache-models.mjs` that imports `pullModels`, `DEFAULT_EMBED_MODEL_URI`, `DEFAULT_RERANK_MODEL_URI` from `/opt/qmd/dist/llm.js` and pulls only those two (skip generate model)
- [ ] in `qmd/Dockerfile`, after the `npm install && npm run build && npm link` line, add `COPY precache-models.mjs /tmp/precache-models.mjs` then `RUN node /tmp/precache-models.mjs && rm /tmp/precache-models.mjs`
- [ ] in `qmd/Dockerfile`, remove patch 3: the `sed -i 's/intent: params\.intent,$/intent: params.intent, rerank: false,/'` line AND the corresponding `grep -q 'intent: params.intent, rerank: false'` verification line. Keep patch 1 (bind host) and patch 2 (rerank schema default false) verbatim.
- [ ] in `qmd/entrypoint.sh`, replace the "Embedding (qmd embed) is skipped" comment block (lines ~13-15) with:
  ```
  echo "QMD: Generating vector embeddings (idempotent)..."
  qmd embed || echo "QMD: Embedding step failed; continuing with lex-only"
  ```
- [ ] rebuild the qmd image: `docker compose build qmd` — confirm build succeeds and that the precache step logs both model paths
- [ ] manual verification only (no automated test for image contents — this is a Dockerfile concern)

### Task 2: Rewrite runbook-search block in DefaultIncidentManagerPrompt

Files:
- Modify: `internal/database/db.go` (lines 427-454)

- [ ] replace the "sub-query 1 (verbatim, 2x weighted) / sub-query 2 (keywords)" block with the natural-language `{lex, vec, hyde}` triplet shape from /tmp/plan.md (all three sub-queries carry the same one-sentence alert summary)
- [ ] change `"collection": "runbooks"` (singular) → `"collections": ["runbooks"]` (plural array — matches QMD's documented `mcp/server.ts:309` shape)
- [ ] keep: the MANDATORY framing, the score > 0.7 gate, the qmd.get follow-up, the 3-total-call cap, the filesystem fallback on QMD error only, and the "empty results NOT a reason to skip" note
- [ ] drop: "verbatim 2x weighted" language, "Original alert text" excerpt instruction, the connection-refused keywords example
- [ ] add retry-angle hints: rephrase as a question, source_system/sender phrases, target_service/host alone
- [ ] run `go test ./internal/database/... -run RunbookSearch` — expected to FAIL here (we update tests in Task 4)

### Task 3: Rewrite PrependGuidance to mirror the triplet shape

Files:
- Modify: `internal/executor/executor.go` (lines 133-170)

- [ ] update the doc-comment on `PrependGuidance` to describe the `{lex, vec, hyde}` triplet shape (drop the "verbatim 2x-weighted" wording)
- [ ] rewrite the function body to emit the same triplet gateway_call example as `DefaultIncidentManagerPrompt`, using `"collections": ["runbooks"]`
- [ ] keep: the current-time prefix, the IMPORTANT framing, the 3-total-call cap, score > 0.7 gate, qmd.get follow-up, retry-angle hints, and the trailing `task` interpolation
- [ ] run `go test ./internal/executor/...` — expected to FAIL here (we update tests in Task 4)

### Task 4: Update prompt and executor tests for triplet shape

Files:
- Modify: `internal/database/prompt_test.go`
- Modify: `internal/executor/executor_test.go`

- [ ] in `prompt_test.go::TestDefaultIncidentManagerPrompt_MandatoryRunbookSearch`: replace the literal inline `gateway_call(...)` assertion (line 42) with three smaller assertions for `"type": "lex"`, `"type": "vec"`, `"type": "hyde"`. Update `"collection": "runbooks"` (line 47) → `"collections": ["runbooks"]`.
- [ ] in `prompt_test.go::TestDefaultIncidentManagerPrompt_RunbookSearchSection` (lines 88-113): drop sub-tests `sub-query 1 marker`, `sub-query 2 marker`, `verbatim weighting note`, `original alert text reference`. Add sub-tests `vec sub-query` (`"type": "vec"`), `hyde sub-query` (`"type": "hyde"`), and `natural-language` (or equivalent string from the new prompt). Keep `Cap total qmd.query calls at 3`, `up to 2 retries`, `score > 0.7`, `limit": 5`.
- [ ] in `executor_test.go`: rewrite the doc-comment (lines 8-17) to describe the triplet shape and the in-sync-with-system-prompt requirement.
- [ ] in `executor_test.go`: update assertions (lines 20-31). Drop `sub-query 1`, `sub-query 2`, `automatically`, `weighted 2x`. Add `"type": "vec"`, `"type": "hyde"`. Update `"collection": "runbooks"` → `"collections": ["runbooks"]`. Keep `Cap total qmd.query calls at 3`, `gateway_call("qmd.query"`, `gateway_call("qmd.get"`, `"type": "lex"`.
- [ ] delete the verbatim-before-keywords ordering check (lines 38-47) entirely — no longer applicable since all three sub-queries carry the same text.
- [ ] keep the `test task` round-trip check (lines 49-51).
- [ ] run `go test ./internal/database/... ./internal/executor/...` — must pass
- [ ] run `make verify` — must pass

### Task 5: Update CLAUDE.md Agent Access paragraph

Files:
- Modify: `CLAUDE.md` (line 312)

- [ ] replace the existing "Agent Access" paragraph with the new wording: instructs the agent to issue ONE `gateway_call("qmd.query", ...)` against `collections: ["runbooks"]` with THREE `searches[]` entries — `{lex, vec, hyde}`, each carrying the same natural-language alert summary; QMD fuses results via RRF; up to 2 retries; filesystem fallback only on QMD error; mention the QMD sidecar ships with embedding + reranker GGUFs pre-cached (~940 MB).
- [ ] no test required — docs-only change

### Task 6: Rebuild containers and verify the stack is healthy

Files: none (operational task)

- [ ] `docker compose down qmd && docker volume rm akmatori_qmd_cache && docker compose up -d qmd` — clear the stale `qmd_cache` volume so the pre-baked models from the new image actually propagate
- [ ] tail qmd logs and confirm the "Generating vector embeddings" stanza completes and the MCP server starts (`docker compose logs -f qmd`)
- [ ] `docker compose build akmatori-api && docker compose up -d akmatori-api` — pick up the new prompts
- [ ] `docker compose ps` — confirm all five containers (api, agent, mcp-gateway, postgres, qmd) report healthy/up
- [ ] call `qmd.status` over MCP and confirm `hasVectorIndex: true`, `needsEmbedding: 0`
- [ ] reproduce the eea37a1d alert text against `qmd.query` with the triplet and confirm the live-streaming runbook appears with non-zero score

### Task 7: Verify acceptance criteria

- [ ] run `make verify` from `/opt/akmatori` — full Go test suite + vet must pass
- [ ] run `go test ./internal/database/... -run RunbookSearch` — must pass
- [ ] run `go test ./internal/executor/...` — must pass
- [ ] confirm prompt changes are coherent: re-read `DefaultIncidentManagerPrompt` and `PrependGuidance` side-by-side for in-sync triplet wording

### Task 8: Update documentation and finalize

- [ ] verify `CLAUDE.md` Agent Access paragraph reflects the new triplet shape (done in Task 5)
- [ ] move this plan to `docs/plans/completed/`
